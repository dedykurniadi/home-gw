package sqlstore

import (
	"path/filepath"
	"testing"
	"time"

	"homegw/internal/netdata"
)

func TestWifiHistStoreRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := db.WifiHist()
	items, err := store.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("harus kosong, dapat %d", len(items))
	}

	it1 := netdata.WifiHistoryItem{
		MAC:       "AA:BB:CC:DD:EE:01",
		IP:        "10.0.0.10",
		Name:      "Phone-User",
		Band:      "5G",
		LastSeen:  time.Now().Unix() - 100,
		Connected: 1200,
	}
	if err := store.Upsert(it1); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}

	it2 := netdata.WifiHistoryItem{
		MAC:       "aa:bb:cc:dd:ee:02",
		IP:        "10.0.0.11",
		Name:      "Laptop-User",
		Band:      "2.4G",
		LastSeen:  time.Now().Unix() - 50,
		Connected: 3600,
	}
	if err := store.Upsert(it2); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}

	all, err := store.All()
	if err != nil || len(all) != 2 {
		t.Fatalf("all: err=%v len=%d", err, len(all))
	}
	// Terurut last_seen DESC (it2 baru, it1 lama)
	if all[0].MAC != "aa:bb:cc:dd:ee:02" || all[1].MAC != "aa:bb:cc:dd:ee:01" {
		t.Fatalf("urutan salah: %+v", all)
	}

	// Update existing tanpa ubah IP jika kosong
	upd := netdata.WifiHistoryItem{
		MAC:       "aa:bb:cc:dd:ee:01",
		Name:      "Phone-User-Baru",
		Band:      "5G",
		LastSeen:  time.Now().Unix(),
		Connected: 1500,
	}
	if err := store.Upsert(upd); err != nil {
		t.Fatalf("upsert update: %v", err)
	}

	all, err = store.All()
	if err != nil || len(all) != 2 {
		t.Fatalf("all update: len=%d", len(all))
	}
	if all[0].MAC != "aa:bb:cc:dd:ee:01" || all[0].IP != "10.0.0.10" || all[0].Name != "Phone-User-Baru" {
		t.Fatalf("update data salah: %+v", all[0])
	}

	// Delete
	if err := store.Delete("aa:bb:cc:dd:ee:01"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	all, _ = store.All()
	if len(all) != 1 || all[0].MAC != "aa:bb:cc:dd:ee:02" {
		t.Fatalf("setelah delete: %+v", all)
	}
}
