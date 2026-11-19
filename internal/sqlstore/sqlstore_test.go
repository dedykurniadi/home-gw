//go:build sqlite

package sqlstore

// Jalankan di mesin yang punya driver:  go test -tags sqlite ./internal/sqlstore

import (
	"path/filepath"
	"testing"
	"time"

	"owrt-dashboard/internal/onoff"
	"owrt-dashboard/internal/powerlog"
	"owrt-dashboard/internal/sched"
)

func openDB(t *testing.T, path string) *DB {
	t.Helper()
	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestPowerStoreRoundTrip(t *testing.T) {
	d := openDB(t, filepath.Join(t.TempDir(), "t.db"))
	st := d.Power("ac")
	if _, ok, err := st.LastRaw(); err != nil || ok {
		t.Fatalf("kosong: ok=%v err=%v", ok, err)
	}
	if err := st.AppendRaw([]powerlog.RawRow{{T: 1000.5, W: 350, V: 216, A: 1.6, Sw: "on", Src: "p"}, {T: 1030.5, W: 10, V: 216, A: 0.1, Sw: "on", Src: "q"}}); err != nil {
		t.Fatal(err)
	}
	r, ok, err := st.LastRaw()
	if err != nil || !ok || r.T != 1030.5 || r.W != 10 || r.Src != "q" || r.Sw != "on" {
		t.Fatalf("LastRaw: %+v ok=%v err=%v", r, ok, err)
	}
	mins := []powerlog.MinRow{{T: 960, Avg: 300, Min: 10, Max: 400, V: 216, A: 1.5, Cov: 60}}
	for i := 0; i < 2; i++ { // idempotent
		if err := st.AppendMinutes(mins); err != nil {
			t.Fatal(err)
		}
	}
	gm, err := st.LoadMinutes(0)
	if err != nil || len(gm) != 1 || gm[0].Avg != 300 || gm[0].Cov != 60 {
		t.Fatalf("LoadMinutes: %+v %v", gm, err)
	}
	evs := []powerlog.Event{{T: 100, State: "comp", W: 372.9}, {T: 100, State: "fan", W: 10, Prev: "comp", Dur: 5, KWh: 0.001}}
	for i := 0; i < 2; i++ {
		if err := st.AppendEvents(evs); err != nil {
			t.Fatal(err)
		}
	}
	ge, err := st.LoadEvents(0)
	if err != nil || len(ge) != 2 || ge[0].State != "comp" || ge[1].State != "fan" || ge[1].Dur != 5 || ge[1].KWh != 0.001 {
		t.Fatalf("LoadEvents: %+v %v", ge, err)
	}
	if err := st.Prune(time.Unix(2000, 0), time.Unix(2000, 0)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.LastRaw(); ok {
		t.Fatal("sampel mentah lama harus terhapus")
	}
	if gm, _ := st.LoadMinutes(0); len(gm) != 0 {
		t.Fatal("rekap menit lama harus terhapus")
	}
	if ge, _ := st.LoadEvents(0); len(ge) != 2 {
		t.Fatal("kejadian tidak boleh dihapus oleh Prune")
	}
	// perangkat lain tidak tercampur
	if _, ok, _ := d.Power("lain").LastRaw(); ok {
		t.Fatal("data antar-perangkat tercampur")
	}
}

func TestOnOffSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	base := time.Now().Truncate(time.Second).Add(-time.Hour)
	d := openDB(t, path)
	l, err := onoff.OpenStore(d.OnOff(), func() time.Time { return base.Add(30 * time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	l.Add("lampu", base, onoff.Off)
	l.Add("lampu", base.Add(10*time.Minute), onoff.On)
	l.Touch()
	d.Close()

	d2 := openDB(t, path)
	l2, err := onoff.OpenStore(d2.OnOff(), func() time.Time { return base.Add(40 * time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	last, ok := l2.Last("lampu")
	// saat dashboard mati, status tidak diketahui: ditandai gap sejak Touch terakhir
	if !ok || last.State != onoff.Gap {
		t.Fatalf("setelah restart harus gap: %+v ok=%v", last, ok)
	}
	h, ok := l2.History("lampu", 2)
	if !ok || h.Stats.OnSec == 0 {
		t.Fatalf("riwayat hilang: %+v", h.Stats)
	}
}

func TestMigrationFromFiles(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Truncate(time.Second).Add(-2 * time.Hour)
	now := func() time.Time { return base.Add(time.Hour) }

	// --- data lama berbentuk file
	ol, err := onoff.Open(filepath.Join(dir, "onoff"), now)
	if err != nil {
		t.Fatal(err)
	}
	ol.Add("lampu", base, onoff.Off)
	ol.Add("lampu", base.Add(5*time.Minute), onoff.On)
	ol.Touch()

	pl, err := powerlog.Open("ac", powerlog.Config{Dir: filepath.Join(dir, "power", "ac"), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	pl.Add(base, "on", 5, 216, 0.1, false)
	for s := 100; s <= 400; s += 30 {
		pl.Add(base.Add(time.Duration(s)*time.Second), "on", 390, 216, 1.8, true)
	}
	pl.Add(base.Add(430*time.Second), "on", 5, 216, 0.1, false)
	if err := pl.Flush(); err != nil {
		t.Fatal(err)
	}
	// pembanding: apa yang dimuat ulang dari file (menit terakhir yang belum final memang belum ditulis)
	plf, err := powerlog.Open("ac", powerlog.Config{Dir: filepath.Join(dir, "power", "ac"), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	want := plf.History(3)

	// --- impor ke SQLite (dua kali: kedua harus tidak menggandakan)
	d := openDB(t, filepath.Join(dir, "dashboard.db"))
	for i := 0; i < 2; i++ {
		ofs, _ := onoff.NewFileStore(filepath.Join(dir, "onoff"))
		if err := d.MigrateOnOff(ofs); err != nil {
			t.Fatal(err)
		}
		pfs, _ := powerlog.NewFileStore(filepath.Join(dir, "power", "ac"))
		if err := d.MigratePower("ac", pfs); err != nil {
			t.Fatal(err)
		}
	}

	ol2, err := onoff.OpenStore(d.OnOff(), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ol2.Last("lampu"); !ok {
		t.Fatal("riwayat ON/OFF tidak terimpor")
	}
	pl2, err := powerlog.Open("ac", powerlog.Config{Store: d.Power("ac"), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	got := pl2.History(3)
	if len(got.Events) != len(want.Events) || len(got.Minutes) != len(want.Minutes) {
		t.Fatalf("setelah impor: kejadian %d/%d, menit %d/%d", len(got.Events), len(want.Events), len(got.Minutes), len(want.Minutes))
	}
	if got.Stats.Cycles != want.Stats.Cycles || got.Stats.KWh != want.Stats.KWh {
		t.Fatalf("statistik berbeda: %+v vs %+v", got.Stats, want.Stats)
	}
}

func TestSchedLogRoundTrip(t *testing.T) {
	d := openDB(t, filepath.Join(t.TempDir(), "t.db"))
	st := d.SchedLog()
	if rs, err := st.Runs(10); err != nil || len(rs) != 0 {
		t.Fatalf("kosong: %v %v", rs, err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.AddRun(sched.Run{T: 100, Fire: 90, Scene: "s1", Label: "Pagi", OK: true}))
	must(st.AddRun(sched.Run{T: 200, Fire: 190, Scene: "s1", Label: "Pagi", Err: "Teras: offline",
		Fails: []sched.Fail{{Device: "a", Name: "Teras", Err: "offline"}}}))
	must(st.AddRun(sched.Run{T: 300, Fire: 290, Scene: "s2", Label: "Malam", OK: true}))
	rs, err := st.Runs(2)
	if err != nil || len(rs) != 2 || rs[0].Scene != "s2" || rs[1].OK || len(rs[1].Fails) != 1 || rs[1].Fails[0].Device != "a" {
		t.Fatalf("Runs: %+v %v", rs, err)
	}
	last, err := st.LastRuns()
	if err != nil || last["s1"].OK != 100 || last["s1"].Fail != 200 || last["s2"].OK != 300 || last["s2"].Fail != 0 {
		t.Fatalf("LastRuns: %+v %v", last, err)
	}
	must(st.Prune(250))
	if rs, _ := st.Runs(10); len(rs) != 1 || rs[0].Scene != "s2" {
		t.Fatalf("Prune: %+v", rs)
	}
}
