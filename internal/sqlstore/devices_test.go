package sqlstore

import (
	"path/filepath"
	"testing"

	"owrt-dashboard/internal/sonoff"
)

func TestDeviceStore(t *testing.T) {
	d := openDB(t, filepath.Join(t.TempDir(), "devs.db"))
	st := d.Devices()

	devs, err := st.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 0 {
		t.Fatalf("harus kosong di awal, dapat %d", len(devs))
	}

	d1 := sonoff.Device{
		ID:       "1000c47454",
		Name:     "Lampu Teras",
		Key:      "1234567890abcdef1234567890abcdef",
		MAC:      "e0:98:06:c1:d4:c1",
		IP:       "10.142.27.206",
		Kind:     "switch",
		Watt:     12.5,
		StandbyW: 0.5,
	}
	if err := st.Save(d1); err != nil {
		t.Fatalf("Save d1: %v", err)
	}

	got, err := st.Get("1000c47454")
	if err != nil {
		t.Fatalf("Get d1: %v", err)
	}
	if got.ID != d1.ID || got.Name != d1.Name || got.Key != d1.Key || got.MAC != d1.MAC || got.Watt != d1.Watt {
		t.Fatalf("data tidak cocok: %+v", got)
	}

	// Update d1
	d1.Name = "Lampu Depan"
	d1.Watt = 15.0
	if err := st.Save(d1); err != nil {
		t.Fatalf("Save update: %v", err)
	}
	got, _ = st.Get("1000c47454")
	if got.Name != "Lampu Depan" || got.Watt != 15.0 {
		t.Fatalf("update gagal: %+v", got)
	}

	// Add d2
	d2 := sonoff.Device{
		ID:   "100201b8d7",
		Name: "AC Kamar",
		Key:  "fedcba0987654321fedcba0987654321",
		Kind: "switches",
	}
	if err := st.Save(d2); err != nil {
		t.Fatalf("Save d2: %v", err)
	}

	all, err := st.All()
	if err != nil || len(all) != 2 {
		t.Fatalf("All: %+v err=%v", all, err)
	}

	// Delete d1
	if err := st.Delete("1000c47454"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	all, err = st.All()
	if err != nil || len(all) != 1 || all[0].ID != "100201b8d7" {
		t.Fatalf("setelah hapus: %+v", all)
	}
}
