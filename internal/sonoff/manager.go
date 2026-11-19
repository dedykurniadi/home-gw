package sonoff

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"owrt-dashboard/internal/mdnslog"
)

const (
	onlineWindow = 150 * time.Second // dianggap online bila terdengar / dikontrol dalam waktu ini
	queryEvery   = 30 * time.Second
	ipFreshness  = 10 * time.Minute
)

// State adalah status satu perangkat untuk UI.
type State struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	State   string   `json:"state"` // "on", "off", atau "unknown"
	Online  bool     `json:"online"`
	IP      string   `json:"ip,omitempty"`
	Power   *float64 `json:"power,omitempty"`   // watt
	Voltage *float64 `json:"voltage,omitempty"` // volt
	Current *float64 `json:"current,omitempty"` // ampere

	Kind         string   `json:"kind"` // "switch" (Mini) atau "switches" (POWR)
	MAC          string   `json:"mac,omitempty"`
	RSSI         *int     `json:"rssi,omitempty"` // dBm
	FW           string   `json:"fw,omitempty"`
	SSID         string   `json:"ssid,omitempty"`
	BSSID        string   `json:"bssid,omitempty"`
	Startup      string   `json:"startup,omitempty"` // on | off | stay
	Pulse        string   `json:"pulse,omitempty"`   // on | off (inching)
	PulseWidth   int      `json:"pulse_width,omitempty"`
	LED          string   `json:"led,omitempty"` // on | off (Network Indicator)
	DayKWh       *float64 `json:"day_kwh,omitempty"`
	MonthKWh     *float64 `json:"month_kwh,omitempty"`
	LastSeen     int64    `json:"last_seen,omitempty"` // unix detik
	Since        int64    `json:"since,omitempty"`     // sejak kapan status ini (dari riwayat)
	Configurable bool     `json:"configurable"`        // pengaturan lewat dashboard didukung (Mini saja)
}

type devState struct {
	Device
	state   string
	ip      string
	seen    time.Time
	power   *float64
	voltage *float64
	current *float64
	warned  bool
	lastSeq string

	rssi                *int
	fw, ssid, bssid     string
	startup, pulse, led string
	pulseWidth          int
	dayKWh, monthKWh    *float64
	loggedGap           bool
	lastFwd             time.Time // kapan terakhir diteruskan ke OnPower
}

type Manager struct {
	// OnChange dipanggil (tanpa lock) setiap state berubah atau berkala.
	OnChange func([]State)
	// OnPower dipanggil (tanpa lock) untuk tiap pengumuman mDNS baru yang memuat daya.
	// viaQuery = datang tepat setelah query kita (bukan spontan dari perangkat).
	OnPower func(id string, t time.Time, sw string, w, v, a float64, viaQuery bool)
	// OnSwitch dipanggil (tanpa lock) bila status ON/OFF perangkat terdengar, atau "gap" bila
	// perangkat berhenti terdengar (waktu = terakhir terdengar). Pemanggil menyaring duplikat.
	OnSwitch func(id string, t time.Time, state string)
	// OnPacket (opsional) dipanggil (tanpa lock) untuk setiap paket mDNS dari perangkat yang dikenal,
	// termasuk paket kembar dan yang gagal didekripsi. OnQuery dipanggil tiap query dikirim.
	OnPacket func(mdnslog.Packet)
	OnQuery  func(time.Time)
	// SinceOf (opsional) memberi waktu mulai status sekarang (unix detik, 0 = tidak tahu).
	SinceOf func(id string) int64
	// QueryEvery = selang query mDNS; 0 = bawaan 30 detik.
	QueryEvery time.Duration
	resolve    func(mac string) string
	lastQuery  atomic.Int64 // unix nano query terakhir

	mu    sync.RWMutex
	devs  map[string]*devState
	order []string
}

// NewManager: resolve(mac) memberi IP terkini dari tabel ARP/DHCP (boleh nil).
func NewManager(devs []Device, resolve func(mac string) string) *Manager {
	m := &Manager{devs: map[string]*devState{}, resolve: resolve}
	for _, d := range devs {
		d.MAC = strings.ToLower(d.MAC)
		m.devs[d.ID] = &devState{Device: d, state: "unknown"}
		m.order = append(m.order, d.ID)
	}
	return m
}

func (m *Manager) States() []State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]State, 0, len(m.order))
	for _, id := range m.order {
		ds := m.devs[id]
		st := State{
			ID: id, Name: ds.Name, State: ds.state, IP: ds.ip,
			Online: !ds.seen.IsZero() && time.Since(ds.seen) < onlineWindow,
			Power:  ds.power, Voltage: ds.voltage, Current: ds.current,
			Kind: ds.Kind, MAC: ds.MAC, RSSI: ds.rssi, FW: ds.fw, SSID: ds.ssid, BSSID: ds.bssid,
			Startup: ds.startup, Pulse: ds.pulse, PulseWidth: ds.pulseWidth, LED: ds.led,
			DayKWh: ds.dayKWh, MonthKWh: ds.monthKWh, Configurable: ds.Kind != "switches",
		}
		if st.Kind == "" {
			st.Kind = "switch"
		}
		if !ds.seen.IsZero() {
			st.LastSeen = ds.seen.Unix()
		}
		if m.SinceOf != nil {
			st.Since = m.SinceOf(id)
		}
		out = append(out, st)
	}
	return out
}

func (m *Manager) notify() {
	if m.OnChange != nil {
		m.OnChange(m.States())
	}
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(x, 64)
		return f, err == nil
	}
	return 0, false
}

// handleTXT memproses satu pengumuman mDNS dari perangkat.
func (m *Manager) handleTXT(kv map[string]string, src net.IP) {
	id := kv["id"]
	rx := time.Now()
	m.mu.Lock()
	ds := m.devs[id]
	if ds == nil {
		m.mu.Unlock()
		return
	}
	pk := mdnslog.Packet{T: rx, ID: id, Seq: kv["seq"], Dec: "ok",
		ViaQ: rx.UnixNano()-m.lastQuery.Load() < int64(1500*time.Millisecond)}
	if src != nil {
		pk.IP = src.String()
	}
	emit := func() {
		if m.OnPacket != nil {
			m.OnPacket(pk)
		}
	}
	var sb strings.Builder
	for i := 1; ; i++ {
		v, ok := kv["data"+strconv.Itoa(i)]
		if !ok {
			break
		}
		sb.WriteString(v)
	}
	payload := []byte(sb.String())
	if kv["encrypt"] == "true" {
		pt, err := decrypt(ds.Key, kv["iv"], sb.String())
		if err != nil {
			if !ds.warned {
				ds.warned = true
				log.Printf("sonoff: %s (%s): gagal dekripsi mDNS: %v", ds.Name, id, err)
			}
			m.mu.Unlock()
			pk.Dec = "fail"
			emit()
			return
		}
		payload = pt
	}
	var j map[string]any
	if json.Unmarshal(payload, &j) != nil {
		m.mu.Unlock()
		pk.Dec = "bad"
		emit()
		return
	}
	if src != nil {
		ds.ip = src.String()
	}
	ds.seen = time.Now()
	if v, ok := j["switch"].(string); ok && (v == "on" || v == "off") {
		ds.state = v
	} else if sw, ok := j["switches"].([]any); ok {
		for _, e := range sw {
			if em, ok := e.(map[string]any); ok {
				if o, _ := num(em["outlet"]); o == 0 {
					if v, _ := em["switch"].(string); v == "on" || v == "off" {
						ds.state = v
					}
				}
			}
		}
	}
	// pengaturan & info perangkat (Mini: kolom datar; POWR: di dalam configure/pulses)
	str := func(dst *string, v any) {
		if x, ok := v.(string); ok && x != "" {
			*dst = x
		}
	}
	str(&ds.fw, j["fwVersion"])
	str(&ds.ssid, j["ssid"])
	str(&ds.bssid, j["bssid"])
	str(&ds.led, j["sledOnline"])
	str(&ds.startup, j["startup"])
	str(&ds.pulse, j["pulse"])
	if f, ok := num(j["pulseWidth"]); ok {
		ds.pulseWidth = int(f)
	}
	if f, ok := num(j["rssi"]); ok {
		r := int(f)
		ds.rssi = &r
	}
	if cf, ok := j["configure"].([]any); ok && len(cf) > 0 {
		if em, ok := cf[0].(map[string]any); ok {
			str(&ds.startup, em["startup"])
		}
	}
	if pl, ok := j["pulses"].([]any); ok && len(pl) > 0 {
		if em, ok := pl[0].(map[string]any); ok {
			str(&ds.pulse, em["pulse"])
			if f, ok := num(em["width"]); ok {
				ds.pulseWidth = int(f)
			}
		}
	}
	// nilai daya dilaporkan dalam satuan 1/100 (mis. power 35671 = 356,71 W)
	setf := func(dst **float64, key string) {
		if f, ok := num(j[key]); ok {
			v := f / 100
			*dst = &v
		}
	}
	setf(&ds.power, "power")
	setf(&ds.voltage, "voltage")
	setf(&ds.current, "current")
	setf(&ds.dayKWh, "dayKwh")
	setf(&ds.monthKWh, "monthKwh")

	// Paket yang sama (seq sama) bisa tiba berulang lewat beberapa jalur; catat sekali saja.
	dup := false
	if seq := kv["seq"]; seq != "" {
		dup = seq == ds.lastSeq
		ds.lastSeq = seq
	}
	pk.Dup, pk.Sw = dup, ds.state
	if ds.rssi != nil {
		r := *ds.rssi
		pk.RSSI = &r
	}
	var hook func()
	// Perangkat hanya mengumumkan saat nilai berubah (3 paket per pengumuman, seq sama). Balasan
	// query dengan seq lama tetap diteruskan sebagai heartbeat (nilai tidak berubah), tetapi
	// paket kembar dalam satu burst dibuang.
	now := time.Now()
	if m.OnPower != nil && ds.power != nil && (!dup || now.Sub(ds.lastFwd) > time.Second) {
		ds.lastFwd = now
		sw, w := ds.state, *ds.power
		var v, a float64
		if ds.voltage != nil {
			v = *ds.voltage
		}
		if ds.current != nil {
			a = *ds.current
		}
		viaQ := now.UnixNano()-m.lastQuery.Load() < int64(1500*time.Millisecond)
		hook = func() { m.OnPower(id, now, sw, w, v, a, viaQ) }
	}
	ds.loggedGap = false
	swState, swSeen := ds.state, ds.seen
	m.mu.Unlock()
	emit()
	m.notify()
	if m.OnSwitch != nil && (swState == "on" || swState == "off") {
		m.OnSwitch(id, swSeen, swState)
	}
	if hook != nil {
		hook()
	}
}

// checkGaps mencatat "gap" untuk perangkat yang berhenti terdengar (sekali per kejadian).
func (m *Manager) checkGaps() {
	if m.OnSwitch == nil {
		return
	}
	type g struct {
		id string
		t  time.Time
	}
	var gaps []g
	m.mu.Lock()
	for _, id := range m.order {
		ds := m.devs[id]
		if !ds.seen.IsZero() && !ds.loggedGap && time.Since(ds.seen) >= onlineWindow {
			ds.loggedGap = true
			gaps = append(gaps, g{id, ds.seen})
		}
	}
	m.mu.Unlock()
	for _, x := range gaps {
		m.OnSwitch(x.id, x.t, "gap")
	}
}

func (m *Manager) query(conn *net.UDPConn) {
	now := time.Now()
	m.lastQuery.Store(now.UnixNano())
	if m.OnQuery != nil {
		m.OnQuery(now)
	}
	sendQuery(conn)
}

// Run mendengarkan mDNS di interface LAN dan menyegarkan state berkala. Blok sampai ctx selesai.
func (m *Manager) Run(ctx context.Context, iface string) {
	conn, err := openMulticast(iface)
	if err != nil {
		log.Printf("sonoff: mDNS nonaktif (%v); kontrol tetap jalan, tetapi status tidak diketahui", err)
	} else {
		defer conn.Close()
		go func() { <-ctx.Done(); conn.Close() }()
		m.query(conn)
		go func() {
			buf := make([]byte, 9000)
			for {
				n, src, err := conn.ReadFromUDP(buf)
				if err != nil {
					return
				}
				for _, t := range txtRecords(buf[:n]) {
					if strings.Contains(t.Name, "_ewelink._tcp") {
						m.handleTXT(parseTXT(t.Txt), src.IP)
					}
				}
			}
		}()
	}
	every := m.QueryEvery
	if every <= 0 {
		every = queryEvery
	}
	tk := time.NewTicker(every)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			if conn != nil {
				m.query(conn)
			}
			m.checkGaps()
			m.notify() // supaya status "online" ikut kedaluwarsa
		}
	}
}

// candidates memberi alamat IP yang layak dicoba untuk perangkat id, berurutan:
// IP dari mDNS (bila segar), lalu resolver ARP/DHCP, lalu IP di config.
func (m *Manager) candidates(id string) (Device, string, []string, error) {
	m.mu.RLock()
	ds := m.devs[id]
	if ds == nil {
		m.mu.RUnlock()
		return Device{}, "", nil, ErrUnknown
	}
	dev, cur := ds.Device, ds.state
	var cands []string
	add := func(ip string) {
		if ip == "" {
			return
		}
		for _, c := range cands {
			if c == ip {
				return
			}
		}
		cands = append(cands, ip)
	}
	if ds.ip != "" && time.Since(ds.seen) < ipFreshness {
		add(ds.ip)
	}
	m.mu.RUnlock()
	if m.resolve != nil {
		add(m.resolve(dev.MAC))
	}
	add(dev.IP)
	if len(cands) == 0 {
		return dev, cur, nil, fmt.Errorf("IP perangkat %s belum diketahui", dev.Name)
	}
	return dev, cur, cands, nil
}

// tryAll menjalankan fn pada tiap kandidat sampai ada yang berhasil; mengingat IP yang berhasil.
func (m *Manager) tryAll(id string, cands []string, fn func(ip string) error) error {
	var lastErr error
	for _, ip := range cands {
		if lastErr = fn(ip); lastErr == nil {
			m.mu.Lock()
			if ds := m.devs[id]; ds != nil {
				ds.ip = ip
				ds.seen = time.Now()
			}
			m.mu.Unlock()
			return nil
		}
	}
	return lastErr
}

// Set mengirim perintah "on", "off", atau "toggle" ke perangkat id.
func (m *Manager) Set(id, cmd string) error {
	dev, cur, cands, err := m.candidates(id)
	if err != nil {
		return err
	}
	var on bool
	switch cmd {
	case "on":
		on = true
	case "off":
		on = false
	case "toggle":
		on = cur != "on"
	default:
		return fmt.Errorf("perintah tidak valid: %q", cmd)
	}
	if err := m.tryAll(id, cands, func(ip string) error { return dev.SetSwitch(ip, on) }); err != nil {
		return err
	}
	m.mu.Lock()
	m.devs[id].state = map[bool]string{true: "on", false: "off"}[on]
	m.mu.Unlock()
	m.notify()
	return nil
}

// ConfigReq: kolom kosong (nil) = tidak diubah.
type ConfigReq struct {
	Startup    *string // on | off | stay
	Pulse      *string // on | off
	PulseWidth *int    // ms, kelipatan 500 (500..3599500)
	LED        *string // on | off
}

func oneOf(v string, ok ...string) bool {
	for _, o := range ok {
		if v == o {
			return true
		}
	}
	return false
}

// Configure mengubah pengaturan perangkat (Mini saja). Nilai divalidasi ketat sebelum dikirim.
func (m *Manager) Configure(id string, r ConfigReq) error {
	dev, _, cands, err := m.candidates(id)
	if err != nil {
		return err
	}
	if dev.Kind == "switches" {
		return fmt.Errorf("%w: pengaturan %s belum didukung", ErrInvalid, dev.Name)
	}
	if r.Startup == nil && r.Pulse == nil && r.PulseWidth == nil && r.LED == nil {
		return fmt.Errorf("%w: tidak ada yang diubah", ErrInvalid)
	}
	if r.Startup != nil && !oneOf(*r.Startup, "on", "off", "stay") {
		return fmt.Errorf("%w: startup harus on, off, atau stay", ErrInvalid)
	}
	if r.Pulse != nil && !oneOf(*r.Pulse, "on", "off") {
		return fmt.Errorf("%w: pulse harus on atau off", ErrInvalid)
	}
	if r.LED != nil && !oneOf(*r.LED, "on", "off") {
		return fmt.Errorf("%w: led harus on atau off", ErrInvalid)
	}
	if w := r.PulseWidth; w != nil && (*w < 500 || *w > 3599500 || *w%500 != 0) {
		return fmt.Errorf("%w: lebar pulse 500-3599500 ms, kelipatan 500", ErrInvalid)
	}
	m.mu.RLock()
	curW := m.devs[id].pulseWidth
	m.mu.RUnlock()
	if curW == 0 {
		curW = 500
	}

	type step struct {
		path   string
		params map[string]any
	}
	var steps []step
	if r.Startup != nil {
		steps = append(steps, step{"startup", map[string]any{"startup": *r.Startup}})
	}
	if r.Pulse != nil || r.PulseWidth != nil {
		pv := ""
		if r.Pulse != nil {
			pv = *r.Pulse
		} else {
			m.mu.RLock()
			pv = m.devs[id].pulse
			m.mu.RUnlock()
			if pv == "" {
				pv = "off"
			}
		}
		w := curW
		if r.PulseWidth != nil {
			w = *r.PulseWidth
		}
		steps = append(steps, step{"pulse", map[string]any{"pulse": pv, "pulseWidth": w}})
	}
	if r.LED != nil {
		steps = append(steps, step{"sledonline", map[string]any{"sledOnline": *r.LED}})
	}
	for _, st := range steps {
		if err := m.tryAll(id, cands, func(ip string) error { return dev.SetConfig(ip, st.path, st.params) }); err != nil {
			return err
		}
	}
	// Perbarui tampilan segera; perangkat juga mengumumkan nilai barunya lewat mDNS (sumber kebenaran).
	m.mu.Lock()
	ds := m.devs[id]
	if r.Startup != nil {
		ds.startup = *r.Startup
	}
	if r.Pulse != nil {
		ds.pulse = *r.Pulse
	}
	if r.PulseWidth != nil {
		ds.pulseWidth = *r.PulseWidth
	}
	if r.LED != nil {
		ds.led = *r.LED
	}
	m.mu.Unlock()
	m.notify()
	return nil
}
