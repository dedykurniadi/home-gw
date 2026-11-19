package sonoff

import (
	"net"
	"testing"
)

func TestDiscoveryAndAdopt(t *testing.T) {
	m := NewManager(nil, nil)
	m.ResolveMAC = func(ip string) string {
		if ip == "10.142.27.206" {
			return "e0:98:06:c1:d4:c1"
		}
		return ""
	}

	// 1. Terima mDNS dari perangkat yang belum dikenal
	m.handleTXT(map[string]string{
		"id":      "1000c47454",
		"type":    "diy_plug",
		"encrypt": "true",
	}, net.ParseIP("10.142.27.206"))

	disc := m.Discovered()
	if len(disc) != 1 {
		t.Fatalf("harus ada 1 perangkat ditemukan, dapat %d", len(disc))
	}
	d0 := disc[0]
	if d0.ID != "1000c47454" || d0.IP != "10.142.27.206" || d0.MAC != "e0:98:06:c1:d4:c1" || d0.Type != "diy_plug" || !d0.Encrypt {
		t.Fatalf("data discovery tidak sesuai: %+v", d0)
	}

	// 2. Adopsi perangkat
	newDev := Device{
		ID:   "1000c47454",
		Name: "Lampu Teras",
		Key:  key,
		MAC:  d0.MAC,
		IP:   d0.IP,
		Kind: "switch",
	}
	m.AddDevice(newDev)

	// Setelah diadopsi, daftar discovered harus bersih dari ID ini
	if len(m.Discovered()) != 0 {
		t.Fatalf("perangkat sudah diadopsi masih ada di discovered: %+v", m.Discovered())
	}

	// Perangkat terdaftar di manager
	devs := m.Devices()
	if len(devs) != 1 || devs[0].ID != "1000c47454" {
		t.Fatalf("perangkat tidak ada di Devices: %+v", devs)
	}

	// Paket mDNS berikutnya dari perangkat yang sudah diadopsi sekarang diproses sebagai state
	iv, data, _ := encrypt(key, []byte(`{"switch":"on","rssi":-50}`))
	m.handleTXT(map[string]string{
		"id":      "1000c47454",
		"encrypt": "true",
		"iv":      iv,
		"data1":   data,
	}, net.ParseIP("10.142.27.206"))

	states := m.States()
	if len(states) != 1 || states[0].State != "on" || !states[0].Online {
		t.Fatalf("state perangkat setelah adopsi tidak cocok: %+v", states)
	}

	// 3. Hapus perangkat
	m.RemoveDevice("1000c47454")
	if len(m.Devices()) != 0 {
		t.Fatalf("perangkat belum terhapus: %+v", m.Devices())
	}
}
