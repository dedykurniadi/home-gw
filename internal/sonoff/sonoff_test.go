package sonoff

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const key = "d9a0069e-cc32-46f7-bab4-c2c4e9cd6a3f"

func TestCryptoRoundTrip(t *testing.T) {
	for _, in := range []string{`{"switch":"on"}`, "", strings.Repeat("x", 16), strings.Repeat("y", 33)} {
		iv, data, err := encrypt(key, []byte(in))
		if err != nil {
			t.Fatal(err)
		}
		out, err := decrypt(key, iv, data)
		if err != nil || string(out) != in {
			t.Fatalf("round trip %q: %q %v", in, out, err)
		}
	}
	iv, data, _ := encrypt(key, []byte(`{"switch":"on"}`))
	if out, err := decrypt("kunci-salah", iv, data); err == nil {
		t.Fatalf("key salah harus gagal, dapat %q", out)
	}
}

// fakeDevice meniru perangkat: mendekripsi request, mencatat path + isi, membalas error=0.
type fakeDevice struct {
	mu   sync.Mutex
	path string
	got  map[string]any
	id   string
}

func (f *fakeDevice) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req request
	json.NewDecoder(r.Body).Decode(&req)
	pt, err := decrypt(key, req.IV, req.Data)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.path, f.id = r.URL.Path, req.DeviceID
	if err != nil || !req.Encrypt {
		w.Write([]byte(`{"error":401}`))
		return
	}
	json.Unmarshal(pt, &f.got)
	w.Write([]byte(`{"seq":1,"error":0}`))
}

func startFake(t *testing.T) (*fakeDevice, string) {
	f := &fakeDevice{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	host, p, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port = p
	return f, host
}

func TestSetSwitch(t *testing.T) {
	f, host := startFake(t)
	d := Device{ID: "1000c47454", Key: key}
	if err := d.SetSwitch(host, true); err != nil {
		t.Fatal(err)
	}
	if f.path != "/zeroconf/switch" || f.got["switch"] != "on" || f.id != "1000c47454" {
		t.Fatalf("mini: %s %v", f.path, f.got)
	}
	d.Kind = "switches"
	if err := d.SetSwitch(host, false); err != nil {
		t.Fatal(err)
	}
	sw, _ := f.got["switches"].([]any)
	if f.path != "/zeroconf/switches" || len(sw) != 1 || sw[0].(map[string]any)["switch"] != "off" || f.got["operSide"] != float64(1) {
		t.Fatalf("powr: %s %v", f.path, f.got)
	}
	bad := Device{ID: "x", Key: "salah"}
	if err := bad.SetSwitch(host, true); err == nil {
		t.Fatal("key salah harus error")
	}
}

func mdnsPacket(rec string, txt []string) []byte {
	pkt := []byte{0, 0, 0x84, 0, 0, 0, 0, 1, 0, 0, 0, 0}
	for _, l := range strings.Split(rec, ".") {
		pkt = append(pkt, byte(len(l)))
		pkt = append(pkt, l...)
	}
	pkt = append(pkt, 0, 0, 16, 0x80, 1, 0, 0, 0, 120)
	var rd []byte
	for _, s := range txt {
		rd = append(rd, byte(len(s)))
		rd = append(rd, s...)
	}
	pkt = append(pkt, byte(len(rd)>>8), byte(len(rd)))
	return append(pkt, rd...)
}

func TestMDNSAndManager(t *testing.T) {
	iv, data, _ := encrypt(key, []byte(`{"switch":"on","rssi":-55}`))
	pkt := mdnsPacket("eWeLink_1000c47454._ewelink._tcp.local", []string{
		"id=1000c47454", "type=diy_plug", "encrypt=true", "iv=" + iv, "data1=" + data[:20], "data2=" + data[20:],
	})
	recs := txtRecords(pkt)
	if len(recs) != 1 || recs[0].Name != "eWeLink_1000c47454._ewelink._tcp.local" {
		t.Fatalf("txt: %+v", recs)
	}
	var changes int
	m := NewManager([]Device{{ID: "1000c47454", Name: "Teras", Key: key, MAC: "E0:98:06:C1:D4:C1"}}, nil)
	m.OnChange = func([]State) { changes++ }
	m.handleTXT(parseTXT(recs[0].Txt), net.ParseIP("10.142.27.206"))
	s := m.States()
	if len(s) != 1 || s[0].State != "on" || !s[0].Online || s[0].IP != "10.142.27.206" || changes != 1 {
		t.Fatalf("state: %+v changes=%d", s, changes)
	}
	// perangkat tak dikenal diabaikan, key salah tidak mengubah state
	m.handleTXT(map[string]string{"id": "zzz"}, nil)
	bad := parseTXT([]string{"id=1000c47454", "encrypt=true", "iv=" + iv, "data1=" + data[:16] + "AAAA" + data[20:]})
	m.handleTXT(bad, nil)
	if m.States()[0].State != "on" {
		t.Fatal("paket rusak tidak boleh mengubah state")
	}
}

func TestPowerScaling(t *testing.T) {
	iv, data, _ := encrypt(key, []byte(`{"switches":[{"outlet":0,"switch":"off"}],"power":35671,"voltage":"21733","current":172}`))
	m := NewManager([]Device{{ID: "100201b8d7", Name: "AC", Key: key, MAC: "aa", Kind: "switches"}}, nil)
	m.handleTXT(map[string]string{"id": "100201b8d7", "encrypt": "true", "iv": iv, "data1": data}, nil)
	s := m.States()[0]
	if s.State != "off" || s.Power == nil || *s.Power != 356.71 || *s.Voltage != 217.33 || *s.Current != 1.72 {
		t.Fatalf("powr: %+v", s)
	}
}

func TestSet(t *testing.T) {
	f, host := startFake(t)
	m := NewManager([]Device{{ID: "1000c47454", Name: "Teras", Key: key, MAC: "aa:bb"}},
		func(mac string) string {
			if mac == "aa:bb" {
				return host
			}
			return ""
		})
	if err := m.Set("nope", "on"); err != ErrUnknown {
		t.Fatalf("id tak dikenal: %v", err)
	}
	if err := m.Set("1000c47454", "ganti"); err == nil {
		t.Fatal("perintah tak valid harus ditolak")
	}
	if err := m.Set("1000c47454", "toggle"); err != nil { // state unknown -> on
		t.Fatal(err)
	}
	if f.got["switch"] != "on" || m.States()[0].State != "on" {
		t.Fatalf("toggle pertama harus on: %v %+v", f.got, m.States())
	}
	if err := m.Set("1000c47454", "toggle"); err != nil {
		t.Fatal(err)
	}
	if f.got["switch"] != "off" || m.States()[0].State != "off" {
		t.Fatalf("toggle kedua harus off: %v", f.got)
	}
}

func TestOnPowerHookAndDedupe(t *testing.T) {
	iv, data, _ := encrypt(key, []byte(`{"switches":[{"outlet":0,"switch":"on"}],"power":39012,"voltage":21800,"current":181}`))
	m := NewManager([]Device{{ID: "100201b8d7", Name: "AC", Key: key, MAC: "aa", Kind: "switches"}}, nil)
	type call struct {
		sw      string
		w, v, a float64
		q       bool
	}
	var calls []call
	m.OnPower = func(id string, _ time.Time, sw string, w, v, a float64, q bool) {
		calls = append(calls, call{sw, w, v, a, q})
	}
	kv := func(seq string) map[string]string {
		return map[string]string{"id": "100201b8d7", "encrypt": "true", "iv": iv, "data1": data, "seq": seq}
	}
	m.lastQuery.Store(time.Now().UnixNano()) // update langsung setelah query kita
	m.handleTXT(kv("7"), nil)
	m.handleTXT(kv("7"), nil) // paket kembar: diabaikan
	m.lastQuery.Store(time.Now().Add(-time.Minute).UnixNano())
	m.handleTXT(kv("8"), nil) // spontan
	if len(calls) != 2 {
		t.Fatalf("hook dipanggil %d kali: %+v", len(calls), calls)
	}
	if c := calls[0]; c.sw != "on" || c.w != 390.12 || c.v != 218 || c.a != 1.81 || !c.q {
		t.Fatalf("panggilan 1: %+v", c)
	}
	if calls[1].q {
		t.Fatal("update spontan tidak boleh dihitung sebagai balasan query")
	}
}

func TestHeartbeatSameSeqForwarded(t *testing.T) {
	iv, data, _ := encrypt(key, []byte(`{"switches":[{"outlet":0,"switch":"on"}],"power":39012,"voltage":21800,"current":181}`))
	m := NewManager([]Device{{ID: "100201b8d7", Name: "AC", Key: key, MAC: "aa", Kind: "switches"}}, nil)
	n := 0
	m.OnPower = func(string, time.Time, string, float64, float64, float64, bool) { n++ }
	kv := map[string]string{"id": "100201b8d7", "encrypt": "true", "iv": iv, "data1": data, "seq": "9"}
	m.handleTXT(kv, nil)
	m.handleTXT(kv, nil) // kembar dalam burst: dibuang
	if n != 1 {
		t.Fatalf("burst harus 1 panggilan, dapat %d", n)
	}
	m.devs["100201b8d7"].lastFwd = time.Now().Add(-30 * time.Second)
	m.handleTXT(kv, nil) // balasan query 30 detik kemudian, seq sama: heartbeat
	if n != 2 {
		t.Fatalf("heartbeat harus diteruskan, dapat %d", n)
	}
}

func ptr[T any](v T) *T { return &v }

func TestConfigure(t *testing.T) {
	f, host := startFake(t)
	m := NewManager([]Device{
		{ID: "1000c47454", Name: "Teras", Key: key, MAC: "aa:bb"},
		{ID: "100201b8d7", Name: "AC", Key: key, MAC: "cc:dd", Kind: "switches"},
	}, func(mac string) string { return host })

	if err := m.Configure("1000c47454", ConfigReq{Startup: ptr("stay")}); err != nil {
		t.Fatal(err)
	}
	if f.path != "/zeroconf/startup" || f.got["startup"] != "stay" {
		t.Fatalf("startup: %s %v", f.path, f.got)
	}
	if err := m.Configure("1000c47454", ConfigReq{Pulse: ptr("on"), PulseWidth: ptr(2000)}); err != nil {
		t.Fatal(err)
	}
	if f.path != "/zeroconf/pulse" || f.got["pulse"] != "on" || f.got["pulseWidth"] != float64(2000) {
		t.Fatalf("pulse: %s %v", f.path, f.got)
	}
	if err := m.Configure("1000c47454", ConfigReq{LED: ptr("off")}); err != nil {
		t.Fatal(err)
	}
	if f.path != "/zeroconf/sledonline" || f.got["sledOnline"] != "off" {
		t.Fatalf("led: %s %v", f.path, f.got)
	}
	if s := m.States()[0]; s.LED != "off" || s.Pulse != "on" || s.PulseWidth != 2000 || s.Startup != "stay" {
		t.Fatalf("state tidak diperbarui: %+v", s)
	}
	// lebar saja: pulse tetap memakai nilai sekarang
	if err := m.Configure("1000c47454", ConfigReq{PulseWidth: ptr(1000)}); err != nil || f.got["pulse"] != "on" || f.got["pulseWidth"] != float64(1000) {
		t.Fatalf("lebar saja: %v %v", err, f.got)
	}

	bad := []ConfigReq{
		{}, {Startup: ptr("nyala")}, {Pulse: ptr("x")}, {LED: ptr("")},
		{PulseWidth: ptr(499)}, {PulseWidth: ptr(750)}, {PulseWidth: ptr(3600000)},
	}
	for i, r := range bad {
		f.path = ""
		if err := m.Configure("1000c47454", r); !errors.Is(err, ErrInvalid) || f.path != "" {
			t.Fatalf("kasus %d harus ditolak sebelum dikirim: %v path=%q", i, err, f.path)
		}
	}
	f.path = ""
	if err := m.Configure("100201b8d7", ConfigReq{LED: ptr("off")}); !errors.Is(err, ErrInvalid) || f.path != "" {
		t.Fatalf("POWR harus ditolak: %v", err)
	}
	if err := m.Configure("nope", ConfigReq{LED: ptr("on")}); err != ErrUnknown {
		t.Fatalf("id tak dikenal: %v", err)
	}
}

func TestRichStateAndGapLogging(t *testing.T) {
	// payload nyata Mini "Teras" (hasil tcpdump + dekripsi)
	iv, data, _ := encrypt(key, []byte(`{"switch":"off","startup":"stay","pulse":"off","sledOnline":"on","fwVersion":"3.8.1","pulseWidth":500,"rssi":-55,"ssid":"wifi","bssid":"fc:dd:55:5a:7d:48"}`))
	m := NewManager([]Device{{ID: "1000c47454", Name: "Teras", Key: key, MAC: "E0:98:06:C1:D4:C1"}}, nil)
	type ev struct {
		id, st string
		t      time.Time
	}
	var evs []ev
	m.OnSwitch = func(id string, tm time.Time, st string) { evs = append(evs, ev{id, st, tm}) }
	m.SinceOf = func(id string) int64 { return 1234 }
	m.handleTXT(map[string]string{"id": "1000c47454", "encrypt": "true", "iv": iv, "data1": data, "seq": "366"}, net.ParseIP("10.142.27.206"))

	s := m.States()[0]
	if s.RSSI == nil || *s.RSSI != -55 || s.FW != "3.8.1" || s.Startup != "stay" || s.Pulse != "off" || s.PulseWidth != 500 ||
		s.LED != "on" || s.BSSID != "fc:dd:55:5a:7d:48" || s.Kind != "switch" || !s.Configurable || s.Since != 1234 || s.MAC != "e0:98:06:c1:d4:c1" || s.LastSeen == 0 {
		t.Fatalf("state: %+v", s)
	}
	if len(evs) != 1 || evs[0].st != "off" {
		t.Fatalf("OnSwitch: %+v", evs)
	}

	// belum lewat onlineWindow: tidak ada gap
	m.checkGaps()
	if len(evs) != 1 {
		t.Fatalf("gap terlalu cepat: %+v", evs)
	}
	// tidak terdengar lama: gap dicatat SEKALI, dengan waktu terakhir terdengar
	m.mu.Lock()
	seen := time.Now().Add(-10 * time.Minute)
	m.devs["1000c47454"].seen = seen
	m.mu.Unlock()
	m.checkGaps()
	m.checkGaps()
	if len(evs) != 2 || evs[1].st != "gap" || !evs[1].t.Equal(seen) {
		t.Fatalf("gap: %+v", evs)
	}
	// terdengar lagi: status ON/OFF dicatat lagi
	m.handleTXT(map[string]string{"id": "1000c47454", "encrypt": "true", "iv": iv, "data1": data, "seq": "367"}, nil)
	if len(evs) != 3 || evs[2].st != "off" {
		t.Fatalf("setelah terdengar lagi: %+v", evs)
	}
}
