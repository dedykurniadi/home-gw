//go:build sqlite

package sqlstore

import (
	"path/filepath"
	"testing"

	"owrt-dashboard/internal/onoff"
	"owrt-dashboard/internal/powerlog"
)

// Jalankan di mesin yang punya driver:  go test -tags sqlite ./internal/sqlstore
func TestMergeBackfillOnly(t *testing.T) {
	dir := t.TempDir()
	ofs, err := onoff.NewFileStore(filepath.Join(dir, "onoff"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []onoff.Event{{T: 100, State: "on"}, {T: 200, State: "off"}, {T: 300, State: "on"}} {
		if err := ofs.Append("a", e); err != nil {
			t.Fatal(err)
		}
	}
	d := openDB(t, filepath.Join(dir, "t.db"))
	if err := d.OnOff().Append("a", onoff.Event{T: 250, State: "on"}); err != nil { // data baru di SQLite
		t.Fatal(err)
	}

	res, err := d.MergeOnOff(ofs, true) // simulasi: tidak menulis
	if err != nil || res["a"] != 2 {
		t.Fatalf("dry: %v %v", res, err)
	}
	if all, _ := d.OnOff().Load(0); len(all["a"]) != 1 {
		t.Fatalf("dry menulis: %v", all["a"])
	}
	res, err = d.MergeOnOff(ofs, false)
	if err != nil || res["a"] != 2 { // hanya 100 dan 200; 300 lebih baru dari 250 sehingga tidak disentuh
		t.Fatalf("merge: %v %v", res, err)
	}
	all, _ := d.OnOff().Load(0)
	if len(all["a"]) != 3 || all["a"][0].T != 100 || all["a"][2].T != 250 {
		t.Fatalf("hasil: %v", all["a"])
	}
	if res, _ = d.MergeOnOff(ofs, false); res["a"] != 0 { // diulang: tidak ada yang baru
		t.Fatalf("tidak idempotent: %v", res)
	}

	pfs, err := powerlog.NewFileStore(filepath.Join(dir, "power", "ac"))
	if err != nil {
		t.Fatal(err)
	}
	pfs.AppendMinutes([]powerlog.MinRow{{T: 60, Avg: 1, Cov: 60}, {T: 120, Avg: 2, Cov: 60}, {T: 180, Avg: 3, Cov: 60}})
	pfs.AppendEvents([]powerlog.Event{{T: 70, State: "comp"}, {T: 190, State: "idle"}})
	if err := d.Power("ac").AppendMinutes([]powerlog.MinRow{{T: 150, Avg: 9, Cov: 60}}); err != nil {
		t.Fatal(err)
	}
	r, err := d.MergePower("ac", pfs, false)
	if err != nil || r.Minutes != 2 || r.Events != 2 {
		t.Fatalf("power: %+v %v", r, err)
	}
	mins, _ := d.Power("ac").LoadMinutes(0)
	if len(mins) != 3 || mins[2].T != 150 || mins[2].Avg != 9 { // data SQLite tidak ditimpa
		t.Fatalf("menit: %+v", mins)
	}
	if r, _ = d.MergePower("ac", pfs, false); r.Minutes != 0 || r.Events != 0 {
		t.Fatalf("tidak idempotent: %+v", r)
	}
}
