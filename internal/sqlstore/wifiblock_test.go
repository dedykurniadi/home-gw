package sqlstore

import (
	"path/filepath"
	"testing"
	"time"

	"homegw/internal/netdata"
)

func TestWifiBlockStoreRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := db.WifiBlocks()
	items, err := store.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("harus kosong, dapat %d", len(items))
	}

	now := time.Now().Unix()
	it1 := netdata.WifiBlockItem{
		MAC:      "AA:BB:CC:DD:EE:01",
		IP:       "10.0.0.10",
		Name:     "Target-1",
		BannedAt: now - 100,
		BanUntil: now + 3600, // 1 jam ke depan
	}
	if err := store.Upsert(it1); err != nil {
		t.Fatalf("upsert 1: %v", err)
	}

	it2 := netdata.WifiBlockItem{
		MAC:      "aa:bb:cc:dd:ee:02",
		IP:       "10.0.0.11",
		Name:     "Target-2",
		BannedAt: now - 50,
		BanUntil: 0, // permanen
	}
	if err := store.Upsert(it2); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}

	all, err := store.All()
	if err != nil || len(all) != 2 {
		t.Fatalf("all: err=%v len=%d", err, len(all))
	}
	// Terurut banned_at DESC (it2 baru, it1 lama)
	if all[0].MAC != "aa:bb:cc:dd:ee:02" || all[1].MAC != "aa:bb:cc:dd:ee:01" {
		t.Fatalf("urutan salah: %+v", all)
	}

	// Delete
	if err := store.Delete("AA:BB:CC:DD:EE:01"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	all, _ = store.All()
	if len(all) != 1 || all[0].MAC != "aa:bb:cc:dd:ee:02" {
		t.Fatalf("setelah delete: %+v", all)
	}

	// Tambah entri kedaluwarsa untuk tes Prune
	itExpired := netdata.WifiBlockItem{
		MAC:      "aa:bb:cc:dd:ee:99",
		IP:       "10.0.0.99",
		Name:     "Expired-Target",
		BannedAt: now - 500,
		BanUntil: now - 10, // sudah lewat
	}
	_ = store.Upsert(itExpired)

	all, _ = store.All()
	if len(all) != 2 {
		t.Fatalf("harus ada 2 item sebelum prune, dapat %d", len(all))
	}

	// Prune
	if err := store.Prune(now); err != nil {
		t.Fatalf("prune error: %v", err)
	}

	all, _ = store.All()
	if len(all) != 1 || all[0].MAC != "aa:bb:cc:dd:ee:02" {
		t.Fatalf("setelah prune: %+v", all)
	}
}
