package netdata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const arpSample = "IP address       HW type     Flags       HW address            Mask     Device\n" +
	"10.142.27.201    0x1         0x2         c4:65:16:ba:c0:91     *        br-lan\n" +
	"10.142.27.200    0x1         0x2         c4:65:16:ba:c0:91     *        br-lan\n" +
	"10.142.27.50     0x1         0x6         aa:bb:cc:dd:ee:ff     *        br-lan\n" +
	"10.142.27.60     0x1         0x0         11:22:33:44:55:66     *        br-lan\n" +
	"192.168.1.1      0x1         0x2         d8:9a:0d:c5:13:c6     *        wan\n"

func TestParsers(t *testing.T) {
	l := parseLeases("1793541761 e0:98:06:c2:b6:a9 10.142.27.167 ESP_C2B6A9 *\n1793523654 b4:9d:02:7e:2d:d9 10.142.27.133 * 01:b4\n")
	if len(l) != 2 || l[0].Host != "ESP_C2B6A9" || l[1].Host != "" {
		t.Fatalf("leases: %+v", l)
	}
	a := parseARP(arpSample, "br-lan")
	if len(a) != 3 {
		t.Fatalf("arp br-lan: %+v", a)
	}
	if all := parseARP(arpSample, ""); len(all) != 4 {
		t.Fatalf("arp semua: %+v", all)
	}
	freq, sig, err := parseHostapd([]byte(`{"freq":5765,"clients":{"12:73:5B:12:B3:36":{"signal":-75}}}`))
	if err != nil || freq != 5765 || sig["12:73:5b:12:b3:36"] != -75 || band(freq) != "5G" || band(2437) != "2.4G" {
		t.Fatalf("hostapd: %v %v %v", freq, sig, err)
	}
	iw := parseIw("Station 00:bd:3e:28:7a:ee (on phy0-ap0)\n\tconnected time: 91600 seconds\n")
	if iw["00:bd:3e:28:7a:ee"] != 91600 {
		t.Fatalf("iw: %+v", iw)
	}
	ssid, f, ch, err := parseHostapdStatus([]byte(`{"status":"COMPLETED","freq":5180,"channel":36,"bssid":"aa:bb:cc:dd:ee:ff","ssid":"HomeGW-5G"}`))
	if err != nil || ssid != "HomeGW-5G" || f != 5180 || ch != 36 {
		t.Fatalf("parseHostapdStatus error: ssid=%s f=%d ch=%d err=%v", ssid, f, ch, err)
	}
	iwInfoSample := "Interface wlan0\n\tssid HomeGW-5G\n\ttype AP\n\tchannel 36 (5180 MHz), width: 80 MHz, center1: 5210 MHz\n"
	iwSSID, iwCh, iwF, iwW := parseIwInfo(iwInfoSample)
	if iwSSID != "HomeGW-5G" || iwCh != 36 || iwF != 5180 || iwW != "80 MHz" {
		t.Fatalf("parseIwInfo: %s, %d, %d, %s", iwSSID, iwCh, iwF, iwW)
	}
	if freqToChannel(5180) != 36 || freqToChannel(2437) != 6 || freqToChannel(2412) != 1 {
		t.Fatalf("freqToChannel gagal")
	}
	if h := subnetHosts("10.142.27.0/24"); len(h) != 254 || h[0] != "10.142.27.1" || h[253] != "10.142.27.254" {
		t.Fatalf("subnet: %d", len(subnetHosts("10.142.27.0/24")))
	}
	if subnetHosts("10.0.0.0/16") != nil {
		t.Fatal("subnet besar harus ditolak")
	}
	if median([]float64{5, 1, 3}) != 3 || median([]float64{1, 3}) != 2 || median(nil) != -1 {
		t.Fatal("median salah")
	}
	if checksum(echo(1234, 5)) != 0 {
		t.Fatal("checksum echo salah")
	}
}

func byIP(rows []Client, ip string) *Client {
	for i := range rows {
		if rows[i].IP == ip {
			return &rows[i]
		}
	}
	return nil
}

func TestBuildRows(t *testing.T) {
	rows := buildRows(buildInput{
		Leases: parseLeases("1 e0:98:06:c1:d4:c1 10.142.27.206 ESP_C1D4C1 *\n1 12:73:5b:12:b3:36 10.142.27.159 MacBookPro *\n1 aa:aa:aa:aa:aa:aa 10.142.27.170 stale *\n"),
		ARP:    parseARP(arpSample+"10.142.27.206 0x1 0x2 e0:98:06:c1:d4:c1 * br-lan\n10.142.27.170 0x1 0x2 bb:bb:bb:bb:bb:bb * br-lan\n", "br-lan"),
		Wifi:   map[string]wifiInfo{"e0:98:06:c1:d4:c1": {"2.4G", -64}},
		Conn:   map[string]int64{"e0:98:06:c1:d4:c1": 5000},
		Static: []StaticHost{{Name: "NVR", IP: "10.142.27.100"}, {Name: "Intel AMT", IP: "10.142.27.200"}},
		Aliases: map[string]string{
			"e0:98:06:c1:d4:c1": "Teras",
		},
	})
	// satu MAC, dua IP (AMT + host OS) harus jadi dua baris
	if a, b := byIP(rows, "10.142.27.200"), byIP(rows, "10.142.27.201"); a == nil || b == nil || a.MAC != b.MAC {
		t.Fatalf("AMT/host harus dua baris dengan MAC sama: %+v %+v", a, b)
	}
	if r := byIP(rows, "10.142.27.200"); r.Name != "Intel AMT" || !r.Fixed {
		t.Fatalf("nama host tetap: %+v", r)
	}
	if byIP(rows, "192.168.1.1") != nil {
		t.Fatal("ARP WAN harus dibuang")
	}
	if r := byIP(rows, "10.142.27.100"); r == nil || r.MAC != "" || r.Name != "NVR" {
		t.Fatalf("host tetap tanpa ARP harus tampil: %+v", r)
	}
	r := byIP(rows, "10.142.27.206")
	if r.Name != "Teras" || r.Conn != "WiFi 2.4G" || r.Src != "DHCP" || r.Connected != 5000 || !r.HasSignal || r.Signal != -64 {
		t.Fatalf("sonoff: %+v", r)
	}
	if r := byIP(rows, "10.142.27.159"); r == nil || r.Src != "DHCP" || r.Name != "MacBookPro" || r.Conn != "LAN" {
		t.Fatalf("lease tanpa ARP: %+v", r)
	}
	// lease usang (MAC beda dengan ARP): nama lease tidak boleh dipakai
	if r := byIP(rows, "10.142.27.170"); r == nil || r.Name != "" || r.MAC != "bb:bb:bb:bb:bb:bb" {
		t.Fatalf("lease usang: %+v", r)
	}
	if r := byIP(rows, "10.142.27.50"); r.Src != "Static" {
		t.Fatalf("ARP statis: %+v", r)
	}
	for i := 1; i < len(rows); i++ {
		if ipKey(rows[i-1].IP) > ipKey(rows[i].IP) {
			t.Fatal("harus terurut menurut IP")
		}
	}
}

func TestDebounce(t *testing.T) {
	c := New(Config{NoPing: true})
	rows := func() []Client { return []Client{{IP: "10.0.0.5", RTT: -1}} }
	ok := map[string]time.Duration{"10.0.0.5": 20 * time.Millisecond}

	r := rows()
	c.applyPing(r, map[string]time.Duration{}, true)
	if r[0].Up {
		t.Fatal("host baru tanpa balasan harus down")
	}
	r = rows()
	c.applyPing(r, ok, true)
	if !r[0].Up || r[0].RTT != 20 {
		t.Fatalf("harus up: %+v", r[0])
	}
	for i := 1; i <= 2; i++ { // 2 kali gagal: masih up
		r = rows()
		c.applyPing(r, nil, true)
		if !r[0].Up {
			t.Fatalf("gagal ke-%d belum boleh down", i)
		}
	}
	r = rows()
	c.applyPing(r, nil, true) // gagal ke-3
	if r[0].Up || r[0].RTT != -1 {
		t.Fatalf("gagal ke-3 harus down: %+v", r[0])
	}
	// state host yang hilang dari tabel dibersihkan
	c.applyPing([]Client{}, nil, true)
	if len(c.states) != 0 {
		t.Fatal("state basi tidak dibersihkan")
	}
}

func TestPingLoopback(t *testing.T) {
	p, err := NewPinger()
	if err != nil {
		t.Skip("raw socket tidak tersedia di sini")
	}
	defer p.Close()
	if _, ok := p.Ping([]string{"127.0.0.1"}, 1, time.Second)["127.0.0.1"]; !ok {
		t.Fatal("tidak ada balasan dari loopback")
	}
}

func TestIsWifiChange(t *testing.T) {
	cases := map[string]bool{
		`{"assoc":{"address":"aa:bb"}}`:    true,
		`{"disassoc":{"address":"aa:bb"}}`: true,
		`{"deauth":{"address":"aa:bb"}}`:   true,
		`{"probe":{"address":"aa:bb"}}`:    false,
		`{"auth":{"address":"aa:bb"}}`:     false,
	}
	for in, want := range cases {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(in), &m); err != nil {
			t.Fatal(err)
		}
		if got := isWifiChange(m); got != want {
			t.Errorf("%s: got %v want %v", in, got, want)
		}
	}
}

func TestWiredHostUpByARPEvenIfPingBlocked(t *testing.T) {
	c := New(Config{NoPing: true})
	r := []Client{{IP: "10.0.0.5"}}
	c.arpUp = map[string]bool{"10.0.0.5": true}
	c.applyPing(r, map[string]time.Duration{}, true) // ICMP tidak menjawab, ARP menjawab
	if !r[0].Up {
		t.Fatal("harus up karena ARP")
	}
	c.arpUp = nil
	for i := 0; i < downAfter; i++ {
		c.applyPing(r, nil, true)
	}
	if r[0].Up {
		t.Fatal("harus down setelah 3 kali gagal")
	}
}

func TestProbeWiredFallbackReadsARP(t *testing.T) {
	f := filepath.Join(t.TempDir(), "arp")
	os.WriteFile(f, []byte("IP address HW type Flags HW address Mask Device\n127.0.0.1 0x1 0x2 aa:bb:cc:dd:ee:ff * br-lan\n127.0.0.2 0x1 0x0 00:00:00:00:00:00 * br-lan\n"), 0o644)
	c := New(Config{NoPing: true, ARPFile: f, WiredHosts: []string{"127.0.0.1", "127.0.0.2"}})
	c.arping = ""
	up := c.probeWired()
	if !up["127.0.0.1"] || up["127.0.0.2"] {
		t.Fatalf("up=%v", up)
	}
}

func TestWiFiHostUpImmediatelyEvenBeforePing(t *testing.T) {
	c := New(Config{NoPing: true})
	r := []Client{{IP: "10.0.0.99", MAC: "aa:bb:cc:dd:ee:ff", HasSignal: true, Signal: -55}}
	// Belum ada ping sama sekali (res kosong)
	c.applyPing(r, map[string]time.Duration{}, true)
	if !r[0].Up {
		t.Fatal("perangkat wifi dengan sinyal aktif harus langsung UP tanpa nunggu ping")
	}

	// quickPublish juga harus langsung tandai UP
	r2 := []Client{{IP: "10.0.0.99", MAC: "aa:bb:cc:dd:ee:ff", HasSignal: true, Signal: -55}}
	c.applyCurrentState(r2)
	if !r2[0].Up {
		t.Fatal("applyCurrentState harus langsung tandai perangkat wifi aktif sebagai UP")
	}
}

func TestBuildRowsIncludesWiFiStationsWithKnownIP(t *testing.T) {
	// Klien terhubung ke WiFi hostapd, tetapi belum tercatat di file leases atau ARP
	rows := buildRows(buildInput{
		Wifi:     map[string]wifiInfo{"11:22:33:44:55:66": {"5G", -48}},
		KnownIPs: map[string]string{"11:22:33:44:55:66": "10.142.27.88"},
	})
	if len(rows) != 1 {
		t.Fatalf("klien wifi harus masuk ke baris: len=%d", len(rows))
	}
	if rows[0].IP != "10.142.27.88" || rows[0].MAC != "11:22:33:44:55:66" || !rows[0].HasSignal || rows[0].Conn != "WiFi 5G" {
		t.Fatalf("data baris wifi salah: %+v", rows[0])
	}
}

func TestCollectorWifiHistory(t *testing.T) {
	c := New(Config{NoPing: true})
	// Simulasi 1 klien aktif di WiFi
	c.wifi = map[string]wifiInfo{"aa:bb:cc:dd:ee:01": {"5G", -50}}
	c.conn = map[string]int64{"aa:bb:cc:dd:ee:01": 300}
	r := []Client{{
		IP:        "10.0.0.50",
		MAC:       "aa:bb:cc:dd:ee:01",
		Name:      "MyPhone",
		Conn:      "WiFi 5G",
		HasSignal: true,
		Signal:    -50,
		Connected: 300,
	}}
	c.store(r)

	// Saat masih aktif, riwayat offline harus kosong
	if hist := c.WifiHistory(); len(hist) != 0 {
		t.Fatalf("klien aktif tidak boleh masuk riwayat offline: %+v", hist)
	}

	// Klien diskonek (hilang dari c.wifi)
	c.wifi = map[string]wifiInfo{}
	c.conn = map[string]int64{}
	r[0].HasSignal = false
	c.store(r)

	hist := c.WifiHistory()
	if len(hist) != 1 {
		t.Fatalf("setelah diskonek harus ada di riwayat: len=%d", len(hist))
	}
	if hist[0].MAC != "aa:bb:cc:dd:ee:01" || hist[0].Name != "MyPhone" || hist[0].Band != "5G" {
		t.Fatalf("data riwayat salah: %+v", hist[0])
	}

	// Hapus riwayat
	if err := c.DeleteWifiHistory("aa:bb:cc:dd:ee:01"); err != nil {
		t.Fatalf("hapus riwayat: %v", err)
	}
	if len(c.WifiHistory()) != 0 {
		t.Fatal("riwayat harus kosong setelah dihapus")
	}
}
