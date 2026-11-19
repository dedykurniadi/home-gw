package sched

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var wib = time.FixedZone("WIB", 7*3600)

func f64(v float64) *float64 { return &v }

var names = map[string]string{"a": "Teras", "b": "Samping", "c": "Pompa"}

func nm(id string) (string, bool) { n, ok := names[id]; return n, ok }

func TestSunJakarta(t *testing.T) {
	d := time.Date(2026, 11, 11, 12, 0, 0, 0, wib)
	rise, set, ok := sunTimes(d, -6.2, 106.8)
	if !ok {
		t.Fatal("tidak ok")
	}
	if rise.Day() != 11 || set.Day() != 11 {
		t.Fatalf("tanggal salah %v %v", rise, set)
	}
	if h := rise.Hour()*60 + rise.Minute(); h < 5*60+25 || h > 5*60+45 {
		t.Fatalf("terbit %v", rise)
	}
	if h := set.Hour()*60 + set.Minute(); h < 17*60+40 || h > 18*60+5 {
		t.Fatalf("terbenam %v", set)
	}
}

func newE(t *testing.T, set func(id, cmd string) error) (*Engine, *time.Time) {
	e, err := New(filepath.Join(t.TempDir(), "s.json"), wib, f64(-6.2), f64(106.8), set, nm)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, wib) // Rabu
	e.now = func() time.Time { return now }
	return e, &now
}

func tm(at string) Trigger { return Trigger{Type: "time", At: at} }

func TestMultiDeviceFiresOnceAndRetriesOnlyFailed(t *testing.T) {
	calls := map[string]int{}
	bFail := true
	e, now := newE(t, func(id, cmd string) error {
		calls[id+cmd]++
		if id == "b" && bFail {
			return errors.New("offline")
		}
		return nil
	})
	_, err := e.Put(Scene{Name: "Evening", Enabled: true, Triggers: []Trigger{tm("18:00")},
		Actions: []Action{{"a", "on"}, {"b", "on"}}})
	if err != nil {
		t.Fatal(err)
	}
	e.Tick(*now)
	if len(calls) != 0 {
		t.Fatal("jalan terlalu awal")
	}
	*now = time.Date(2026, 11, 11, 18, 0, 5, 0, wib)
	e.Tick(*now)
	e.Tick(now.Add(30 * time.Second))
	if calls["aon"] != 1 || calls["bon"] != 2 || len(e.Runs()) != 0 {
		t.Fatalf("retry salah: %v %v", calls, e.Runs())
	}
	bFail = false
	e.Tick(now.Add(time.Minute))
	e.Tick(now.Add(2 * time.Minute))
	r := e.Runs()
	if calls["aon"] != 1 || calls["bon"] != 3 || len(r) != 1 || !r[0].OK || r[0].Label != "Evening" {
		t.Fatalf("sukses salah: %v %+v", calls, r)
	}
}

func TestGiveUpReportsFailure(t *testing.T) {
	e, now := newE(t, func(id, cmd string) error { return errors.New("offline") })
	e.Put(Scene{Enabled: true, Triggers: []Trigger{tm("18:00")}, Actions: []Action{{"a", "on"}}})
	*now = time.Date(2026, 11, 11, 18, 1, 0, 0, wib)
	e.Tick(*now)
	e.Tick(now.Add(11 * time.Minute)) // jendela habis
	r := e.Runs()
	if len(r) != 1 || r[0].OK || !strings.Contains(r[0].Err, "Teras: offline") || r[0].Label != "Teras" ||
		len(r[0].Fails) != 1 || r[0].Fails[0].Device != "a" || r[0].Fails[0].Name != "Teras" || r[0].Fire == 0 {
		t.Fatalf("%+v", r)
	}
}

func TestFiresOnceAndStale(t *testing.T) {
	n := 0
	e, now := newE(t, func(id, cmd string) error { n++; return nil })
	e.Put(Scene{Enabled: true, Triggers: []Trigger{tm("17:35")}, Actions: []Action{{"a", "on"}}})
	e.Put(Scene{Enabled: true, Triggers: []Trigger{tm("18:00")}, Actions: []Action{{"a", "on"}}})
	*now = time.Date(2026, 11, 11, 17, 36, 0, 0, wib)
	e.Tick(*now)
	if n != 1 {
		t.Fatalf("pemicu 17:35: %d", n)
	}
	*now = time.Date(2026, 11, 11, 18, 0, 0, 0, wib)
	e.Tick(*now)
	if n != 2 {
		t.Fatalf("pemicu 18:00: %d", n)
	}
	e.Tick(now.Add(time.Hour)) // tidak diulang
	if n != 2 {
		t.Fatalf("diulang: %d", n)
	}
	// basi: pemicu 20:00 terlewat 20 menit
	e.Put(Scene{Enabled: true, Triggers: []Trigger{tm("20:00")}, Actions: []Action{{"b", "off"}}})
	*now = time.Date(2026, 11, 11, 19, 0, 0, 0, wib)
	e2, _ := newE(t, func(id, cmd string) error { n += 100; return nil })
	e2.Put(Scene{Enabled: true, Triggers: []Trigger{tm("19:30")}, Actions: []Action{{"a", "on"}}})
	e2.now = func() time.Time { return time.Date(2026, 11, 11, 19, 31, 0, 0, wib) }
	e2.scenes[0].Last = []int64{0}
	e2.Tick(time.Date(2026, 11, 11, 19, 50, 0, 0, wib))
	if n != 2 || len(e2.Runs()) != 1 || e2.Runs()[0].OK {
		t.Fatalf("basi tetap jalan: %d %+v", n, e2.Runs())
	}
}

func TestDaysAndNew(t *testing.T) {
	n := 0
	e, now := newE(t, func(id, cmd string) error { n++; return nil })
	e.Put(Scene{Enabled: true, Triggers: []Trigger{tm("11:00")}, Days: []int{1, 2}, Actions: []Action{{"a", "off"}}}) // Sen, Sel; hari ini Rabu
	e.Put(Scene{Enabled: true, Triggers: []Trigger{tm("09:00")}, Actions: []Action{{"a", "on"}}})                     // sudah lewat saat dibuat
	*now = time.Date(2026, 11, 11, 11, 5, 0, 0, wib)
	e.Tick(*now)
	if n != 0 {
		t.Fatalf("hari salah / langsung jalan: %d", n)
	}
}

func TestNextSunset(t *testing.T) {
	e, _ := newE(t, func(id, cmd string) error { return nil })
	e.Put(Scene{Enabled: true, Triggers: []Trigger{{Type: "sunset", Offset: -10}}, Actions: []Action{{"a", "on"}}})
	v := e.Views()
	if len(v) != 1 {
		t.Fatal("views")
	}
	nx := time.Unix(v[0].Next, 0).In(wib)
	if nx.Day() != 11 || nx.Hour() != 17 {
		t.Fatalf("next %v", nx)
	}
	if v[0].Label != "Teras" {
		t.Fatalf("label %q", v[0].Label)
	}
}

func TestValidate(t *testing.T) {
	e, _ := newE(t, func(id, cmd string) error { return nil })
	ok := []Action{{"a", "on"}}
	bad := []Scene{
		{Triggers: []Trigger{tm("18:00")}, Actions: []Action{{"x", "on"}}},
		{Triggers: []Trigger{tm("18:00")}, Actions: []Action{{"a", "toggle"}}},
		{Triggers: []Trigger{tm("18:00"), tm("19:00")}, Actions: ok}, // hanya satu pemicu
		{Triggers: []Trigger{tm("18:00")}, Actions: []Action{{"a", "on"}, {"a", "off"}}},
		{Triggers: []Trigger{tm("25:00")}, Actions: ok},
		{Triggers: []Trigger{tm("18:00")}, Actions: ok, Days: []int{7}},
		{Triggers: []Trigger{{Type: "sunset", Offset: 999}}, Actions: ok},
		{Actions: ok},
		{Triggers: []Trigger{tm("18:00")}},
		{Name: strings.Repeat("x", 41), Triggers: []Trigger{tm("18:00")}, Actions: ok},
	}
	for i, s := range bad {
		if _, err := e.Put(s); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%d lolos: %v", i, err)
		}
	}
	e2, _ := New(filepath.Join(t.TempDir(), "x.json"), wib, nil, nil, nil, nm)
	if _, err := e2.Put(Scene{Triggers: []Trigger{{Type: "sunrise"}}, Actions: ok}); !errors.Is(err, ErrInvalid) {
		t.Fatal("matahari tanpa lokasi lolos")
	}
}

func TestMigrateLegacyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(p, []byte(`[{"id":"old1","device":"a","action":"on","trigger":"time","at":"17:35","days":[],"enabled":true,"last":5},
 {"id":"old2","device":"b","action":"off","trigger":"sunrise","offset":10,"days":[1],"enabled":false}]`), 0o644)
	e, err := New(p, wib, f64(3.55), f64(98.58), nil, nm)
	if err != nil {
		t.Fatal(err)
	}
	v := e.Views()
	if len(v) != 2 || v[0].ID != "old1" || v[0].Triggers[0].At != "17:35" || v[0].Actions[0].Device != "a" ||
		v[1].Triggers[0].Type != "sunrise" || v[1].Triggers[0].Offset != 10 || v[0].Label != "Teras" {
		t.Fatalf("%+v", v)
	}
}

func TestPersistAndHTTP(t *testing.T) {
	e, _ := newE(t, func(id, cmd string) error { return nil })
	h := e.Handler()
	body := `{"name":"Morning","triggers":[{"type":"time","at":"05:30"}],"actions":[{"device":"a","action":"off"},{"device":"b","action":"off"}],"days":[1,2],"enabled":true}`
	req := httptest.NewRequest("POST", "/api/sched", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("tanpa header: %d", w.Code)
	}
	req = httptest.NewRequest("POST", "/api/sched", strings.NewReader(body))
	req.Header.Set("X-Dashboard", "1")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("buat: %d %s", w.Code, w.Body)
	}
	e2, err := New(e.path, wib, nil, nil, nil, nm)
	if err != nil || len(e2.Views()) != 1 || len(e2.Views()[0].Actions) != 2 {
		t.Fatalf("tidak tersimpan: %v", err)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/sched", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"has_location":true`) || !strings.Contains(w.Body.String(), `"label":"Morning"`) {
		t.Fatalf("daftar: %s", w.Body)
	}
	id := e.Views()[0].ID
	rq := httptest.NewRequest("DELETE", "/api/sched/"+id, nil)
	rq.Header.Set("X-Dashboard", "1")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, rq)
	if w.Code != 200 || len(e.Views()) != 0 {
		t.Fatalf("hapus: %d", w.Code)
	}
}

func TestSplitMultiTriggerScene(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(p, []byte(`[{"id":"m1","name":"Pagi","triggers":[{"type":"time","at":"05:30"},{"type":"time","at":"06:00"}],
 "actions":[{"device":"a","action":"off"}],"days":[1],"enabled":true,"last":[11,22]},
 {"id":"m2","triggers":[{"type":"time","at":"07:00"}],"actions":[{"device":"b","action":"on"}],"enabled":true}]`), 0o644)
	e, err := New(p, wib, nil, nil, nil, nm)
	if err != nil {
		t.Fatal(err)
	}
	v := e.Views()
	if len(v) != 3 || v[0].ID != "m1" || v[0].Name != "Pagi" || v[0].Triggers[0].At != "05:30" || v[0].Last[0] != 11 ||
		v[1].Name != "Pagi 2" || v[1].ID == "m1" || v[1].Triggers[0].At != "06:00" || v[1].Last[0] != 22 || len(v[1].Days) != 1 ||
		v[2].ID != "m2" {
		t.Fatalf("%+v", v)
	}
	e2, _ := New(p, wib, nil, nil, nil, nm) // sudah tersimpan: muat ulang memberi ID yang sama
	v2 := e2.Views()
	if len(v2) != 3 || v2[1].ID != v[1].ID {
		t.Fatalf("pemecahan tidak tersimpan: %+v", v2)
	}
}

type memRuns struct{ rs []Run }

func (m *memRuns) AddRun(r Run) error { m.rs = append(m.rs, r); return nil }
func (m *memRuns) Runs(n int) ([]Run, error) {
	out := []Run{}
	for i := len(m.rs) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, m.rs[i])
	}
	return out, nil
}
func (m *memRuns) LastRuns() (map[string]LastRun, error) {
	o := map[string]LastRun{}
	for _, r := range m.rs {
		l := o[r.Scene]
		if r.OK {
			l.OK = r.T
		} else {
			l.Fail = r.T
		}
		o[r.Scene] = l
	}
	return o, nil
}
func (m *memRuns) Prune(before int64) error { return nil }

func TestRunLogStoreAndLastResults(t *testing.T) {
	bad := true
	e, now := newE(t, func(id, cmd string) error {
		if id == "b" && bad {
			return errors.New("tidak terjangkau")
		}
		return nil
	})
	ms := &memRuns{}
	e.SetStore(ms)
	sc, _ := e.Put(Scene{Name: "Morning", Enabled: true, Triggers: []Trigger{tm("18:00")}, Actions: []Action{{"a", "on"}, {"b", "on"}}})
	*now = time.Date(2026, 11, 11, 18, 0, 5, 0, wib)
	e.Tick(*now)
	e.Tick(now.Add(11 * time.Minute)) // jendela habis: gagal, hanya perangkat b
	rs := e.Runs()
	if len(rs) != 1 || rs[0].OK || len(rs[0].Fails) != 1 || rs[0].Fails[0].Device != "b" || len(ms.rs) != 1 {
		t.Fatalf("%+v", rs)
	}
	// hari berikutnya berhasil
	bad = false
	*now = time.Date(2026, 11, 12, 18, 0, 5, 0, wib)
	e.Tick(*now)
	v := e.Views()[0]
	if v.ID != sc.ID || v.LastOK != now.Unix() || v.LastFail == 0 || v.LastFail >= v.LastOK {
		t.Fatalf("terakhir berhasil/gagal salah: %+v", v)
	}
	if rs := e.Runs(); len(rs) != 2 || !rs[0].OK || rs[1].OK {
		t.Fatalf("urutan log: %+v", rs)
	}
}

func TestRunsCarryCurrentSceneName(t *testing.T) {
	e := &Engine{scenes: []Scene{{ID: "abc", Name: "Pagi"}}, runs: []Run{{Scene: "abc"}, {Scene: "gone"}}}
	rs := e.Runs()
	if len(rs) != 2 || rs[1].Name != "Pagi" || rs[0].Name != "" {
		t.Fatalf("runs: %+v", rs)
	}
}
