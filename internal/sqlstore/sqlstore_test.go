package sqlstore

import (
	"path/filepath"
	"testing"
	"time"

	"homegw/internal/onoff"
	"homegw/internal/powerlog"
	"homegw/internal/sched"
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

func TestEnergyDailyRoundTrip(t *testing.T) {
	d := openDB(t, filepath.Join(t.TempDir(), "t.db"))
	st := d.EnergyDaily()
	m, err := st.LoadDaily()
	if err != nil || len(m) != 0 {
		t.Fatalf("kosong: %v %v", m, err)
	}
	if err := st.SaveDaily("ac", "2026-11-03", 1.25); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveDaily("ac", "2026-11-03", 1.75); err != nil { // idempotent per kunci
		t.Fatal(err)
	}
	if err := st.SaveDaily("led", "2026-11-03", 0.4); err != nil {
		t.Fatal(err)
	}
	if err := st.PruneDaily("2026-11-02"); err != nil {
		t.Fatal(err)
	}
	if err := st.PruneDaily("2026-11-04"); err != nil {
		t.Fatal(err)
	}
	m, err = st.LoadDaily()
	if err != nil || len(m) != 0 {
		t.Fatalf("setelah prune harus kosong: %v %v", m, err)
	}
}

func TestOnOffSrcRoundTrip(t *testing.T) {
	d := openDB(t, filepath.Join(t.TempDir(), "t.db"))
	st := d.OnOff().(interface {
		SrcStart() (int64, error)
		SetSrcStart(int64) error
		LoadSrc(int64) ([]onoff.SrcRec, error)
		AppendSrc(onoff.SrcRec) error
	})
	if at, err := st.SrcStart(); err != nil || at != 0 {
		t.Fatalf("awal kosong: %d %v", at, err)
	}
	if err := st.SetSrcStart(1000); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendSrc(onoff.SrcRec{T: 1010, ID: "lampu", Src: "HomeGW"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendSrc(onoff.SrcRec{T: 1020, ID: "ac", Src: "Scene: Pagi"}); err != nil {
		t.Fatal(err)
	}
	if at, _ := st.SrcStart(); at != 1000 {
		t.Fatalf("awal pencatatan: %d", at)
	}
	recs, err := st.LoadSrc(1000)
	if err != nil || len(recs) != 2 || recs[0].ID != "lampu" || recs[1].Src != "Scene: Pagi" {
		t.Fatalf("LoadSrc: %+v %v", recs, err)
	}
	if recs, _ := st.LoadSrc(1015); len(recs) != 1 || recs[0].ID != "ac" {
		t.Fatalf("filter cut: %+v", recs)
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
