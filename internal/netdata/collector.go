// Paket netdata mengumpulkan data jaringan dari OpenWrt: client WiFi (hostapd + iw),
// DHCP lease, tabel ARP, sweep subnet, dan status ping.
package netdata

import (
	"context"
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
	WifiEvents    bool              // berlangganan event hostapd (ubus subscribe) agar konek/putus WiFi seketika
	ResolveOnly   bool              // hanya membaca ARP/DHCP untuk IPByMAC: tanpa ubus/WiFi (mode hanya-IoT)
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

	mu        sync.RWMutex
	clients   []Client
	macIP     map[string]string
	states    map[string]*hostState
	ifaces    []string
	ifacesAt  time.Time
	lastSweep time.Time
	kickCh    chan struct{}
	arping    string // path program arping, kosong bila tidak ada
	arpUp     map[string]bool
	wifi      map[string]wifiInfo
	conn      map[string]int64
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
	c := &Collector{cfg: cfg, states: map[string]*hostState{}, macIP: map[string]string{}, kickCh: make(chan struct{}, 1)}
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

func (c *Collector) Run(ctx context.Context) {
	if c.pinger != nil {
		defer c.pinger.Close()
	}
	c.refreshWifi()
	c.cycle()
	if c.cfg.WifiEvents && !c.cfg.ResolveOnly {
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
			// beri hostapd sebentar memperbarui daftar station, lalu baca; jeda minimum antar siklus kejadian
			time.Sleep(400 * time.Millisecond)
			c.refreshWifi()
			c.cycle()
			time.Sleep(600 * time.Millisecond)
		}
	}
}

// refreshWifi membaca ulang data WiFi: saat start, saat event konek/putus, atau tiap siklus bila wifi_events mati.
func (c *Collector) refreshWifi() {
	if c.cfg.ResolveOnly {
		return
	}
	c.wifi, c.conn = c.readWifi()
}

func sh(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

func (c *Collector) readWifi() (map[string]wifiInfo, map[string]int64) {
	wifi := map[string]wifiInfo{}
	conn := map[string]int64{}
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
		out, err := sh("ubus", "call", name, "get_clients")
		if err != nil {
			continue
		}
		freq, sig, err := parseHostapd([]byte(out))
		if err != nil {
			continue
		}
		for mac, s := range sig {
			wifi[mac] = wifiInfo{band(freq), s}
		}
		if iw, err := sh("iw", "dev", strings.TrimPrefix(name, "hostapd."), "station", "dump"); err == nil {
			for mac, t := range parseIw(iw) {
				conn[mac] = t
			}
		}
	}
	return wifi, conn
}

func (c *Collector) cycle() {
	if c.pinger != nil && time.Since(c.lastSweep) >= c.cfg.SweepInterval {
		// Sweep memaksa kernel menyelesaikan ARP, jadi host yang diam / memblokir ICMP tetap masuk tabel ARP.
		if hosts := subnetHosts(c.cfg.Subnet); len(hosts) > 0 {
			c.pinger.Ping(hosts, 1, 800*time.Millisecond)
		}
		c.lastSweep = time.Now()
	}
	leasesRaw, _ := os.ReadFile(c.cfg.LeasesFile)
	arpRaw, _ := os.ReadFile(c.cfg.ARPFile)
	rows := buildRows(buildInput{
		Leases:  parseLeases(string(leasesRaw)),
		ARP:     parseARP(string(arpRaw), c.cfg.LANIface),
		Wifi:    c.wifi,
		Conn:    c.conn,
		Static:  c.cfg.StaticHosts,
		Aliases: c.cfg.Aliases,
	})
	var res map[string]time.Duration
	if c.pinger != nil {
		ips := make([]string, 0, len(rows))
		for _, r := range rows {
			ips = append(ips, r.IP)
		}
		res = c.pinger.Ping(ips, 1, time.Second)
	}
	c.arpUp = c.probeWired()
	c.applyPing(rows, res, c.pinger != nil)
	c.store(rows)
}

// applyPing memperbarui status per IP: down hanya setelah 3 kali gagal beruntun,
// latency berupa median dari beberapa sampel terakhir.
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
		if c.arpUp[ip] {
			st.fails = 0 // menjawab ARP: hidup walau ICMP diblokir
		}
		if d, ok := res[ip]; ok {
			st.fails = 0
			st.rtts = append(st.rtts, float64(d.Microseconds())/1000)
			if len(st.rtts) > rttWindow {
				st.rtts = st.rtts[len(st.rtts)-rttWindow:]
			}
		} else if st.fails < downAfter {
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
		if r.MAC == "" {
			continue
		}
		if _, ok := macIP[r.MAC]; !ok || r.Src == "DHCP" {
			macIP[r.MAC] = r.IP
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return ipKey(rows[i].IP) < ipKey(rows[j].IP) })
	c.mu.Lock()
	c.clients = rows
	c.macIP = macIP
	c.mu.Unlock()
	if c.OnUpdate != nil {
		c.OnUpdate(append([]Client(nil), rows...))
	}
}
