// Paket netdata mengumpulkan data jaringan dari OpenWrt: client WiFi (hostapd + iw),
// DHCP lease, tabel ARP, sweep subnet, dan status ping.
package netdata

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

type StaticHost struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
}

type Config struct {
	LANIface      string            // filter ARP, mis. "br-lan" (kosong = semua)
	Subnet        string            // CIDR untuk sweep, mis. "10.142.27.0/24"
	PollInterval  time.Duration     // default 5s
	SweepInterval time.Duration     // default 60s
	LeasesFile    string            // default /tmp/dhcp.leases
	ARPFile       string            // default /proc/net/arp
	StaticHosts   []StaticHost      // selalu tampil, walau tidak ada di ARP
	Aliases       map[string]string // MAC -> nama
	NoPing        bool              // semua dianggap hidup (mode uji / non-root)
	WiredHosts    []string          // IP perangkat kabel: status up/down dari ARP (arping bila ada), bukan hanya ICMP
	WifiEvents     bool               // berlangganan event hostapd (ubus subscribe) agar konek/putus WiFi seketika
	WifiHistStore  WifiHistStore      // penyimpanan persisten SQLite untuk riwayat klien WiFi
	WifiBlockStore WifiBlockStore     // penyimpanan persisten SQLite untuk daftar pemblokiran WiFi
}

// WifiHistoryItem mencatat riwayat perangkat yang pernah terhubung ke WiFi.
type WifiHistoryItem struct {
	MAC       string `json:"mac"`
	IP        string `json:"ip"`
	Name      string `json:"name"`
	Band      string `json:"band"`
	LastSeen  int64  `json:"last_seen"`  // unix timestamp detik saat terakhir aktif
	Connected int64  `json:"connected"` // durasi terhubung saat sesi terakhir (detik)
}

type WifiHistStore interface {
	All() ([]WifiHistoryItem, error)
	Upsert(item WifiHistoryItem) error
	Delete(mac string) error
	Prune(olderThan time.Duration) error
}

// WifiBlockItem mencatat status pemblokiran/ban perangkat WiFi.
type WifiBlockItem struct {
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Name     string `json:"name"`
	BannedAt int64  `json:"banned_at"` // unix timestamp saat diblokir
	BanUntil int64  `json:"ban_until"` // unix timestamp berakhir, 0 = permanen
}

type WifiBlockStore interface {
	All() ([]WifiBlockItem, error)
	Upsert(item WifiBlockItem) error
	Delete(mac string) error
	Prune(now int64) error
}

type Client struct {
	IP        string  `json:"ip"`
	MAC       string  `json:"mac"`
	Name      string  `json:"name"`
	Conn      string  `json:"conn"` // "WiFi 5G", "WiFi 2.4G", "LAN"
	Src       string  `json:"src"`  // "DHCP" atau "Static"
	Signal    int     `json:"signal"`
	HasSignal bool    `json:"has_signal"`
	Connected int64   `json:"connected"` // detik, -1 bila tidak diketahui
	Up        bool    `json:"up"`
	RTT       float64 `json:"rtt"` // median ms, -1 bila tidak ada
	Fixed     bool    `json:"fixed"`
	TxRate    string  `json:"tx_rate,omitempty"` // kecepatan PHY kirim (mis. "866 Mbps")
	RxRate    string  `json:"rx_rate,omitempty"` // kecepatan PHY terima (mis. "780 Mbps")
	Bitrate   string  `json:"bitrate,omitempty"` // ringkasan kecepatan tautan
}

const (
	downAfter = 3 // tandai down setelah 3 kali gagal beruntun
	rttWindow = 5 // jumlah sampel untuk median
)

type hostState struct {
	fails int
	rtts  []float64
}

type Collector struct {
	cfg    Config
	pinger *Pinger
	// OnUpdate dipanggil dari goroutine collector setiap siklus.
	OnUpdate func([]Client)
	// OnWifiHistory dipanggil saat riwayat klien WiFi berubah.
	OnWifiHistory func([]WifiHistoryItem)
	// OnWifiAPs dipanggil saat konfigurasi/status Access Point WiFi terbaca.
	OnWifiAPs func([]WifiAP)
	// OnWifiBlocks dipanggil saat daftar pemblokiran WiFi berubah.
	OnWifiBlocks func([]WifiBlockItem)

	mu         sync.RWMutex
	clients    []Client
	macIP      map[string]string
	states     map[string]*hostState
	ifaces     []string
	ifacesAt   time.Time
	lastSweep  time.Time
	kickCh     chan struct{}
	arping     string // path program arping, kosong bila tidak ada
	arpUp      map[string]bool
	wifi       map[string]wifiInfo
	conn       map[string]int64
	wifiHist   map[string]WifiHistoryItem
	wifiBlocks map[string]WifiBlockItem
	wifiAPs    []WifiAP
}

func New(cfg Config) *Collector {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if cfg.SweepInterval <= 0 {
		cfg.SweepInterval = 60 * time.Second
	}
	if cfg.LeasesFile == "" {
		cfg.LeasesFile = "/tmp/dhcp.leases"
	}
	if cfg.ARPFile == "" {
		cfg.ARPFile = "/proc/net/arp"
	}
	c := &Collector{
		cfg:        cfg,
		states:     map[string]*hostState{},
		macIP:      map[string]string{},
		kickCh:     make(chan struct{}, 1),
		wifiHist:   map[string]WifiHistoryItem{},
		wifiBlocks: map[string]WifiBlockItem{},
	}
	if cfg.WifiHistStore != nil {
		if items, err := cfg.WifiHistStore.All(); err == nil {
			for _, it := range items {
				c.wifiHist[strings.ToLower(it.MAC)] = it
			}
		}
	}
	if cfg.WifiBlockStore != nil {
		now := time.Now().Unix()
		_ = cfg.WifiBlockStore.Prune(now)
		if items, err := cfg.WifiBlockStore.All(); err == nil {
			for _, it := range items {
				if it.BanUntil == 0 || it.BanUntil > now {
					c.wifiBlocks[strings.ToLower(it.MAC)] = it
				}
			}
		}
	}
	if len(cfg.WiredHosts) > 0 {
		c.arping, _ = exec.LookPath("arping")
	}
	if !cfg.NoPing {
		p, err := NewPinger()
		if err != nil {
			log.Printf("netdata: ping dinonaktifkan (butuh root): %v", err)
		} else {
			c.pinger = p
		}
	}
	return c
}

// Snapshot mengembalikan salinan daftar client terakhir.
func (c *Collector) Snapshot() []Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Client(nil), c.clients...)
}

// IPByMAC memberi IP terkini untuk sebuah MAC ("" bila tidak diketahui).
func (c *Collector) IPByMAC(mac string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.macIP[strings.ToLower(mac)]
}

// MACByIP memberi MAC terkini untuk sebuah IP ("" bila tidak diketahui).
func (c *Collector) MACByIP(ip string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, cl := range c.clients {
		if cl.IP == ip {
			return cl.MAC
		}
	}
	return ""
}

// SetAlias mengubah atau menambah alias nama untuk sebuah MAC.
func (c *Collector) SetAlias(mac, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cfg.Aliases == nil {
		c.cfg.Aliases = map[string]string{}
	}
	c.cfg.Aliases[strings.ToLower(mac)] = name
}

func (c *Collector) Run(ctx context.Context) {
	if c.pinger != nil {
		defer c.pinger.Close()
	}
	c.refreshWifi()
	c.cycle()
	if c.cfg.WifiEvents {
		go c.watchWifi(ctx)
	}
	t := time.NewTicker(c.cfg.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !c.cfg.WifiEvents {
				c.refreshWifi()
			}
			c.cycle()
		case <-c.kickCh:
			// beri hostapd waktu singkat (50ms) memperbarui daftar station
			time.Sleep(50 * time.Millisecond)
			select {
			case <-c.kickCh:
			default:
			}
			c.refreshWifi()
			// Fast-path: publikasikan update daftar klien WiFi seketika tanpa menunggu siklus ping
			c.quickPublish()
			c.cycle()
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// refreshWifi membaca ulang data WiFi: saat start, saat event konek/putus, atau tiap siklus bila wifi_events mati.
func (c *Collector) refreshWifi() {
	wifi, conn, aps := c.readWifi()

	// Enforce pemblokiran bila ada stasiun yang terblokir mencoba terhubung kembali
	now := time.Now().Unix()
	c.mu.Lock()
	for mac := range wifi {
		if blk, ok := c.wifiBlocks[mac]; ok {
			if blk.BanUntil == 0 || blk.BanUntil > now {
				var remainingMs int64 = 30 * 86400 * 1000
				if blk.BanUntil > 0 {
					remainingMs = (blk.BanUntil - now) * 1000
				}
				for _, name := range c.ifaces {
					payload := fmt.Sprintf(`{"addr":"%s","deauth":true,"reason":1,"ban_time":%d}`, mac, remainingMs)
					_, _ = sh("ubus", "call", name, "del_client", payload)
				}
				delete(wifi, mac)
				delete(conn, mac)
			} else {
				delete(c.wifiBlocks, mac)
				if c.cfg.WifiBlockStore != nil {
					_ = c.cfg.WifiBlockStore.Delete(mac)
				}
			}
		}
	}
	c.wifi, c.conn = wifi, conn
	c.wifiAPs = aps
	c.mu.Unlock()
	if c.OnWifiAPs != nil && len(aps) > 0 {
		c.OnWifiAPs(aps)
	}
}

// WifiAPs mengembalikan daftar snapshot AP WiFi yang aktif.
func (c *Collector) WifiAPs() []WifiAP {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]WifiAP(nil), c.wifiAPs...)
}

func sh(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

func (c *Collector) readWifi() (map[string]wifiInfo, map[string]int64, []WifiAP) {
	wifi := map[string]wifiInfo{}
	conn := map[string]int64{}
	var aps []WifiAP
	if c.ifaces == nil || time.Since(c.ifacesAt) > 60*time.Second {
		c.ifacesAt = time.Now()
		c.ifaces = []string{}
		if list, err := sh("ubus", "list"); err == nil {
			for _, name := range strings.Fields(list) {
				if strings.HasPrefix(name, "hostapd.") {
					c.ifaces = append(c.ifaces, name)
				}
			}
		}
	}
	for _, name := range c.ifaces {
		ifname := strings.TrimPrefix(name, "hostapd.")
		ap := WifiAP{Iface: ifname}

		// 1. Cek ubus call hostapd.<iface> get_status untuk SSID, Freq, dan Channel
		if stOut, err := sh("ubus", "call", name, "get_status"); err == nil {
			ssid, f, ch, _ := parseHostapdStatus([]byte(stOut))
			ap.SSID = ssid
			if f > 0 {
				ap.Freq = f
			}
			if ch > 0 {
				ap.Channel = ch
			}
		}

		// 2. Cek iw dev <ifname> info untuk melengkapi / cross-check
		if iwInfo, err := sh("iw", "dev", ifname, "info"); err == nil {
			ssid, ch, f, w := parseIwInfo(iwInfo)
			if ap.SSID == "" && ssid != "" {
				ap.SSID = ssid
			}
			if ap.Channel == 0 && ch > 0 {
				ap.Channel = ch
			}
			if ap.Freq == 0 && f > 0 {
				ap.Freq = f
			}
			if w != "" {
				ap.Width = w
			}
		}

		// 3. Baca clients via get_clients dari ubus (sinyal, phy rate, durasi koneksi)
		out, err := sh("ubus", "call", name, "get_clients")
		if err == nil {
			freq, stas, err := parseHostapd([]byte(out))
			if err == nil {
				if ap.Freq == 0 && freq > 0 {
					ap.Freq = freq
				}
				for mac, s := range stas {
					txStr := formatRate(s.TxRate)
					rxStr := formatRate(s.RxRate)
					bitrate := txStr
					if bitrate == "" {
						bitrate = rxStr
					}
					wifi[mac] = wifiInfo{
						Band:      band(freq),
						Signal:    s.Signal,
						TxRate:    txStr,
						RxRate:    rxStr,
						Bitrate:   bitrate,
						Connected: s.ConnectedTime,
					}
					if s.ConnectedTime > 0 {
						conn[mac] = s.ConnectedTime
					}
				}
			}
		}

		if ap.Freq > 0 {
			ap.Band = band(ap.Freq)
			if ap.Channel == 0 {
				ap.Channel = freqToChannel(ap.Freq)
			}
		}
		if ap.Width == "" && ap.Band != "" {
			switch ap.Band {
			case "5G":
				ap.Width = "80 MHz"
			case "6G":
				ap.Width = "160 MHz"
			default:
				ap.Width = "20 MHz"
			}
		}
		if ap.Band != "" || ap.SSID != "" {
			aps = append(aps, ap)
		}

		if iw, err := sh("iw", "dev", ifname, "station", "dump"); err == nil {
			for mac, t := range parseIw(iw) {
				conn[mac] = t
			}
		}
	}
	return wifi, conn, aps
}

// quickPublish menerbitkan snapshot daftar client segera (misal setelah event WiFi masuk/keluar)
// dengan status saat ini tanpa harus menunggu siklus ping selesai.
func (c *Collector) quickPublish() {
	leasesRaw, _ := os.ReadFile(c.cfg.LeasesFile)
	arpRaw, _ := os.ReadFile(c.cfg.ARPFile)
	c.mu.RLock()
	knownIPs := make(map[string]string, len(c.macIP))
	for k, v := range c.macIP {
		knownIPs[k] = v
	}
	c.mu.RUnlock()

	rows := buildRows(buildInput{
		Leases:   parseLeases(string(leasesRaw)),
		ARP:      parseARP(string(arpRaw), c.cfg.LANIface),
		Wifi:     c.wifi,
		Conn:     c.conn,
		Static:   c.cfg.StaticHosts,
		Aliases:  c.cfg.Aliases,
		KnownIPs: knownIPs,
	})
	c.arpUp = c.probeWired()
	c.applyCurrentState(rows)
	c.store(rows)
}

// applyCurrentState menerapkan status up dan RTT yang sudah ada ke baris baru,
// dan perangkat dengan HasSignal (WiFi aktif) selalu dianggap Up seketika.
func (c *Collector) applyCurrentState(rows []Client) {
	for i := range rows {
		ip := rows[i].IP
		if rows[i].HasSignal || c.arpUp[ip] {
			if st := c.states[ip]; st != nil {
				st.fails = 0
				rows[i].RTT = median(st.rtts)
			}
			rows[i].Up = true
			continue
		}
		st := c.states[ip]
		if st != nil {
			rows[i].Up = st.fails < downAfter
			if rows[i].Up {
				rows[i].RTT = median(st.rtts)
			}
		}
	}
}

func (c *Collector) cycle() {
	if c.pinger != nil && time.Since(c.lastSweep) >= c.cfg.SweepInterval {
		// Sweep memaksa kernel menyelesaikan ARP, jadi host yang diam / memblokir ICMP tetap masuk tabel ARP.
		if hosts := subnetHosts(c.cfg.Subnet); len(hosts) > 0 {
			c.pinger.Ping(hosts, 1, 600*time.Millisecond)
		}
		c.lastSweep = time.Now()
	}
	leasesRaw, _ := os.ReadFile(c.cfg.LeasesFile)
	arpRaw, _ := os.ReadFile(c.cfg.ARPFile)
	c.mu.RLock()
	knownIPs := make(map[string]string, len(c.macIP))
	for k, v := range c.macIP {
		knownIPs[k] = v
	}
	c.mu.RUnlock()

	rows := buildRows(buildInput{
		Leases:   parseLeases(string(leasesRaw)),
		ARP:      parseARP(string(arpRaw), c.cfg.LANIface),
		Wifi:     c.wifi,
		Conn:     c.conn,
		Static:   c.cfg.StaticHosts,
		Aliases:  c.cfg.Aliases,
		KnownIPs: knownIPs,
	})
	var res map[string]time.Duration
	if c.pinger != nil {
		ips := make([]string, 0, len(rows))
		for _, r := range rows {
			if r.IP != "" && r.IP != "-" {
				ips = append(ips, r.IP)
			}
		}
		res = c.pinger.Ping(ips, 1, 500*time.Millisecond)
	}
	c.arpUp = c.probeWired()
	c.applyPing(rows, res, c.pinger != nil)
	c.store(rows)
}

// applyPing memperbarui status per IP: down hanya setelah 3 kali gagal beruntun,
// latency berupa median dari beberapa sampel terakhir. Perangkat WiFi aktif selalu UP seketika.
func (c *Collector) applyPing(rows []Client, res map[string]time.Duration, pinging bool) {
	seen := map[string]bool{}
	for i := range rows {
		ip := rows[i].IP
		seen[ip] = true
		if !pinging {
			rows[i].Up = true
			continue
		}
		st := c.states[ip]
		if st == nil {
			st = &hostState{fails: downAfter} // host baru dianggap down sampai menjawab
			c.states[ip] = st
		}
		// Perangkat yang aktif terhubung di WiFi hostapd atau menjawab ARP langsung dianggap UP
		if rows[i].HasSignal || c.arpUp[ip] {
			st.fails = 0
		}
		if d, ok := res[ip]; ok {
			st.fails = 0
			st.rtts = append(st.rtts, float64(d.Microseconds())/1000)
			if len(st.rtts) > rttWindow {
				st.rtts = st.rtts[len(st.rtts)-rttWindow:]
			}
		} else if !rows[i].HasSignal && !c.arpUp[ip] && st.fails < downAfter {
			st.fails++
		}
		rows[i].Up = st.fails < downAfter
		if rows[i].Up {
			rows[i].RTT = median(st.rtts)
		} else {
			st.rtts = nil
		}
	}
	for ip := range c.states {
		if !seen[ip] {
			delete(c.states, ip)
		}
	}
}

func (c *Collector) store(rows []Client) {
	macIP := map[string]string{}
	for _, r := range rows {
		if r.MAC == "" || r.IP == "" || r.IP == "-" {
			continue
		}
		if _, ok := macIP[r.MAC]; !ok || r.Src == "DHCP" {
			macIP[r.MAC] = r.IP
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return ipKey(rows[i].IP) < ipKey(rows[j].IP) })
	c.mu.Lock()
	c.clients = rows
	for k, v := range macIP {
		c.macIP[k] = v
	}
	now := time.Now().Unix()
	for _, r := range rows {
		if r.HasSignal && r.MAC != "" {
			macLower := strings.ToLower(r.MAC)
			band := ""
			if strings.HasPrefix(r.Conn, "WiFi ") {
				band = strings.TrimPrefix(r.Conn, "WiFi ")
			}
			it := WifiHistoryItem{
				MAC:       macLower,
				IP:        r.IP,
				Name:      r.Name,
				Band:      band,
				LastSeen:  now,
				Connected: r.Connected,
			}
			c.wifiHist[macLower] = it
			if c.cfg.WifiHistStore != nil {
				go c.cfg.WifiHistStore.Upsert(it)
			}
		}
	}
	c.mu.Unlock()
	if c.OnUpdate != nil {
		c.OnUpdate(append([]Client(nil), rows...))
	}
	if c.OnWifiHistory != nil {
		c.OnWifiHistory(c.WifiHistory())
	}
}

// WifiHistory mengembalikan riwayat klien WiFi yang saat ini TIDAK aktif (sedang terputus).
func (c *Collector) WifiHistory() []WifiHistoryItem {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var out []WifiHistoryItem
	for mac, it := range c.wifiHist {
		if _, active := c.wifi[mac]; active {
			continue
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen > out[j].LastSeen })
	return out
}

// DeleteWifiHistory menghapus entri riwayat WiFi dari memori dan database.
func (c *Collector) DeleteWifiHistory(mac string) error {
	macLower := strings.ToLower(strings.TrimSpace(mac))
	c.mu.Lock()
	delete(c.wifiHist, macLower)
	c.mu.Unlock()
	if c.cfg.WifiHistStore != nil {
		_ = c.cfg.WifiHistStore.Delete(macLower)
	}
	if c.OnWifiHistory != nil {
		c.OnWifiHistory(c.WifiHistory())
	}
	return nil
}

// WifiBlocks mengembalikan daftar klien WiFi yang sedang diblokir.
func (c *Collector) WifiBlocks() []WifiBlockItem {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().Unix()
	var out []WifiBlockItem
	for mac, it := range c.wifiBlocks {
		if it.BanUntil > 0 && it.BanUntil <= now {
			delete(c.wifiBlocks, mac)
			if c.cfg.WifiBlockStore != nil {
				_ = c.cfg.WifiBlockStore.Delete(mac)
			}
			continue
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BannedAt > out[j].BannedAt })
	return out
}

// KickWifiClient memutuskan atau memblokir klien WiFi via ubus hostapd.<iface> del_client.
// banSeconds = 0: kick seketika (tanpa ban).
// banSeconds > 0: blokir selama sekian detik.
// banSeconds < 0: blokir permanen.
func (c *Collector) KickWifiClient(mac string, banSeconds int64) error {
	mac = strings.ToLower(strings.TrimSpace(mac))
	if mac == "" {
		return errors.New("mac address kosong")
	}

	c.mu.RLock()
	var clientName, clientIP string
	for _, cl := range c.clients {
		if strings.ToLower(cl.MAC) == mac {
			clientName = cl.Name
			clientIP = cl.IP
			break
		}
	}
	if clientIP == "" {
		clientIP = c.macIP[mac]
	}
	if clientName == "" && c.cfg.Aliases != nil {
		clientName = c.cfg.Aliases[mac]
	}
	if clientName == "" {
		if h, ok := c.wifiHist[mac]; ok {
			clientName = h.Name
			if clientIP == "" {
				clientIP = h.IP
			}
		}
	}
	ifaces := append([]string(nil), c.ifaces...)
	c.mu.RUnlock()

	if len(ifaces) == 0 {
		if list, err := sh("ubus", "list"); err == nil {
			for _, name := range strings.Fields(list) {
				if strings.HasPrefix(name, "hostapd.") {
					ifaces = append(ifaces, name)
				}
			}
		}
	}

	now := time.Now().Unix()
	var banMs int64
	var banUntil int64

	if banSeconds > 0 {
		banMs = banSeconds * 1000
		banUntil = now + banSeconds
	} else if banSeconds < 0 {
		// Permanen: di hostapd 30 hari (2592000000 ms), di store ban_until = 0
		banMs = 30 * 86400 * 1000
		banUntil = 0
	} else {
		banMs = 0
		banUntil = -1
	}

	payload := fmt.Sprintf(`{"addr":"%s","deauth":true,"reason":1,"ban_time":%d}`, mac, banMs)
	for _, iface := range ifaces {
		_, _ = sh("ubus", "call", iface, "del_client", payload)
	}

	if banUntil != -1 {
		item := WifiBlockItem{
			MAC:      mac,
			IP:       clientIP,
			Name:     clientName,
			BannedAt: now,
			BanUntil: banUntil,
		}
		c.mu.Lock()
		c.wifiBlocks[mac] = item
		c.mu.Unlock()
		if c.cfg.WifiBlockStore != nil {
			_ = c.cfg.WifiBlockStore.Upsert(item)
		}
		if c.OnWifiBlocks != nil {
			c.OnWifiBlocks(c.WifiBlocks())
		}
	}

	c.kick()
	return nil
}

// UnbanWifiClient membuka pemblokiran klien WiFi via ubus hostapd.<iface> del_client dan menghapus dari database.
func (c *Collector) UnbanWifiClient(mac string) error {
	mac = strings.ToLower(strings.TrimSpace(mac))
	if mac == "" {
		return errors.New("mac address kosong")
	}

	c.mu.RLock()
	ifaces := append([]string(nil), c.ifaces...)
	c.mu.RUnlock()

	if len(ifaces) == 0 {
		if list, err := sh("ubus", "list"); err == nil {
			for _, name := range strings.Fields(list) {
				if strings.HasPrefix(name, "hostapd.") {
					ifaces = append(ifaces, name)
				}
			}
		}
	}

	// Reset ban timer di hostapd
	payload := fmt.Sprintf(`{"addr":"%s","deauth":false,"reason":1,"ban_time":0}`, mac)
	for _, iface := range ifaces {
		_, _ = sh("ubus", "call", iface, "del_client", payload)
	}

	c.mu.Lock()
	delete(c.wifiBlocks, mac)
	c.mu.Unlock()

	if c.cfg.WifiBlockStore != nil {
		_ = c.cfg.WifiBlockStore.Delete(mac)
	}
	if c.OnWifiBlocks != nil {
		c.OnWifiBlocks(c.WifiBlocks())
	}
	return nil
}
