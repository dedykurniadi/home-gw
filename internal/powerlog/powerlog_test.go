package powerlog

import (
	"math"
	"sort"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 2, 10, 0, 0, 0, time.Local)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

var stores = map[string]*memStore{}

// newLogger memakai satu memStore per "dir" agar test restart membaca data yang sama.
func newLogger(t *testing.T, dir string, c *clock) *Logger {
	t.Helper()
	st := stores[dir]
	if st == nil {
		st = newMemStore()
		stores[dir] = st
	}
	l, err := Open("ac", Config{Store: st, Now: c.now})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func at(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

func states(l *Logger) []string {
	var out []string
	for _, e := range l.events {
		out = append(out, e.State)
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("event %v, diharapkan %v", got, want)
	}
}

func TestCompressorCycle(t *testing.T) {
	c := &clock{at(0)}
	l := newLogger(t, t.TempDir(), c)
	l.Add(at(0), "on", 5, 220, 0.05, false)
	l.Add(at(30), "on", 5, 220, 0.05, false)
	for s := 100; s <= 400; s += 30 { // kompresor menyala sejak detik 100
		l.Add(at(s), "on", 390, 218, 1.8, false)
	}
	l.Add(at(430), "on", 5, 220, 0.05, false)
	eq(t, states(l), []string{"fan", "comp", "fan"})

	comp, fan := l.events[1], l.events[2]
	if comp.T != at(100).Unix() || comp.Prev != "fan" {
		t.Fatalf("awal kompresor harus mundur ke update pertama >300W: %+v", comp)
	}
	if fan.T != at(430).Unix() || fan.Prev != "comp" || fan.Dur != 330 {
		t.Fatalf("akhir kompresor: %+v", fan)
	}
	if want := 390.0 * 330 / 3.6e6; math.Abs(fan.KWh-want) > 0.0005 {
		t.Fatalf("kWh siklus %.4f, diharapkan %.4f", fan.KWh, want)
	}
}

func TestSpikeIgnoredAndHold(t *testing.T) {
	c := &clock{at(0)}
	l := newLogger(t, t.TempDir(), c)
	l.Add(at(0), "on", 5, 220, 0.05, false)
	l.Add(at(5), "on", 420, 220, 2, false) // lonjakan sesaat
	l.Add(at(10), "on", 8, 220, 0.05, false)
	eq(t, states(l), []string{"fan"})

	// kompresor menyala, lalu turun ke 150 W (zona tengah): status dipertahankan
	l.Add(at(60), "on", 400, 220, 2, false)
	l.Add(at(75), "on", 400, 220, 2, false)
	l.Add(at(90), "on", 150, 220, 1, false)
	eq(t, states(l), []string{"fan", "comp"})
	l.Add(at(120), "on", 60, 220, 0.3, false) // di bawah comp_off_w
	eq(t, states(l), []string{"fan", "comp", "fan"})

	// 20-300 W saat fan: tidak berubah
	l.Add(at(150), "on", 120, 220, 0.6, false)
	eq(t, states(l), []string{"fan", "comp", "fan"})
}

func TestRelayAndGap(t *testing.T) {
	c := &clock{at(0)}
	l := newLogger(t, t.TempDir(), c)
	l.Add(at(0), "on", 5, 220, 0.05, false)
	l.Add(at(30), "off", 0, 220, 0, false)
	eq(t, states(l), []string{"fan", "off"})
	l.Add(at(60), "on", 6, 220, 0.05, false)
	eq(t, states(l), []string{"fan", "off", "fan"})

	// data hilang 10 menit saat kompresor menyala
	l.Add(at(100), "on", 400, 220, 2, false)
	l.Add(at(120), "on", 400, 220, 2, false)
	l.Add(at(150), "on", 400, 220, 2, false)
	l.Add(at(750), "on", 400, 220, 2, false)
	eq(t, states(l), []string{"fan", "off", "fan", "comp", "gap", "comp"})
	g := l.events[4]
	if g.T != at(150).Unix() || g.Prev != "comp" {
		t.Fatalf("gap harus dimulai di update terakhir: %+v", g)
	}
	// gap tidak ikut dihitung sebagai daya
	h := l.History(1)
	if h.Stats.GapSec != 600 {
		t.Fatalf("gap_sec = %d", h.Stats.GapSec)
	}
	for _, m := range h.Minutes {
		if int64(m[0]) >= at(180).Unix() && int64(m[0]) < at(720).Unix() {
			t.Fatalf("menit di dalam gap tidak boleh ada: %v", m)
		}
	}
}

func TestStaleLive(t *testing.T) {
	c := &clock{at(0)}
	l := newLogger(t, t.TempDir(), c)
	l.Add(at(0), "on", 5, 220, 0.05, false)
	c.t = at(30)
	if s := l.Live().State; s != "fan" {
		t.Fatalf("live %s", s)
	}
	c.t = at(400)
	if s := l.Live().State; s != "gap" {
		t.Fatalf("data basi harus gap, dapat %s", s)
	}
	h := l.History(1)
	if h.Stats.GapSec != 400 {
		t.Fatalf("segmen gap hidup tidak muncul: %+v", h.Segs)
	}
}

func TestMinuteRollupTimeWeighted(t *testing.T) {
	c := &clock{at(0)}
	l := newLogger(t, t.TempDir(), c)
	l.Add(at(0), "on", 100, 220, 1, false)  // 100 W selama 15 detik
	l.Add(at(15), "on", 500, 220, 2, false) // lalu 500 W selama 45 detik
	l.Add(at(60), "on", 500, 220, 2, false)
	b := l.buckets[0]
	if want := (100*15 + 500*45) / 60.0; math.Abs(b.avg()-want) > 0.01 {
		t.Fatalf("rata-rata berbobot waktu %.2f, diharapkan %.2f", b.avg(), want)
	}
	if b.min != 100 || b.max != 500 {
		t.Fatalf("min/max %v/%v", b.min, b.max)
	}
}

func TestPersistAndReload(t *testing.T) {
	dir := t.TempDir()
	c := &clock{at(0)}
	l := newLogger(t, dir, c)
	l.Add(at(0), "on", 5, 220, 0.05, false)
	for s := 100; s <= 400; s += 30 {
		l.Add(at(s), "on", 390, 218, 1.8, s%60 == 40)
	}
	l.Add(at(430), "on", 5, 220, 0.05, false)
	l.Add(at(520), "on", 5, 220, 0.05, false)
	c.t = at(520)
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	st := stores[dir]
	if len(st.raw) == 0 || len(st.events) == 0 || len(st.mins) == 0 {
		t.Fatalf("data tertunda belum disimpan: raw=%d events=%d mins=%d", len(st.raw), len(st.events), len(st.mins))
	}
	var gotP, gotQ bool
	for _, r := range st.raw {
		gotP = gotP || r.Src == "p"
		gotQ = gotQ || r.Src == "q"
	}
	if !gotP || !gotQ {
		t.Fatalf("kolom sumber p/q hilang: %+v", st.raw)
	}

	l2 := newLogger(t, dir, c)
	eq(t, states(l2), []string{"fan", "comp", "fan"})
	if l2.state != "fan" || !l2.hasPrev || l2.prev.t.Unix() != at(520).Unix() {
		t.Fatalf("pemulihan: state=%s prev=%v", l2.state, l2.prev)
	}
	if len(l2.buckets) == 0 {
		t.Fatal("rekap menit tidak dimuat")
	}
	// jam router sinkron ulang setelah restart lama => gap
	l2.Add(at(4000), "on", 5, 220, 0.05, false)
	eq(t, states(l2), []string{"fan", "comp", "fan", "gap", "fan"})

	// menulis ulang tidak menggandakan baris
	n := len(l2.events)
	c.t = at(4000)
	l2.Flush()
	l3 := newLogger(t, dir, c)
	if len(l3.events) != n {
		t.Fatalf("event %d, diharapkan %d", len(l3.events), n)
	}
}

func TestStats(t *testing.T) {
	c := &clock{at(0)}
	l := newLogger(t, t.TempDir(), c)
	l.Add(at(0), "on", 5, 220, 0.05, false)
	// dua siklus: 300 dtk menyala, 200 dtk istirahat
	s := 100
	for cyc := 0; cyc < 2; cyc++ {
		for e := s + 300; s <= e; s += 20 {
			l.Add(at(s), "on", 400, 220, 1.8, false)
		}
		for e := s + 200; s <= e; s += 20 {
			l.Add(at(s), "on", 5, 220, 0.05, false)
		}
	}
	c.t = at(s)
	h := l.History(1)
	st := h.Stats
	if st.Cycles != 2 {
		t.Fatalf("cycles %d", st.Cycles)
	}
	if st.AvgCompSec < 290 || st.AvgCompSec > 330 {
		t.Fatalf("avg comp %v", st.AvgCompSec)
	}
	if st.Duty < 0.5 || st.Duty > 0.7 {
		t.Fatalf("duty %v", st.Duty)
	}
	if st.KWh <= 0 || st.KWhPerCycle <= 0 || st.MaxW != 400 {
		t.Fatalf("stats %+v", st)
	}
}

func TestIgnoresUnsetClock(t *testing.T) {
	c := &clock{at(0)}
	l := newLogger(t, t.TempDir(), c)
	l.Add(time.Unix(100, 0), "on", 5, 220, 0.05, false)
	if l.hasPrev || len(l.events) != 0 {
		t.Fatal("sampel dengan jam belum sinkron harus diabaikan")
	}
}

func TestPruneCutsByRetention(t *testing.T) {
	dir := t.TempDir()
	c := &clock{at(0)} // 2026-11-02
	l := newLogger(t, dir, c)
	st := stores[dir]
	if st.pruned == 0 {
		t.Fatal("Prune harus dipanggil saat Open")
	}
	if l.cfg.RawDays != 3 || l.cfg.MinuteMonths != 24 {
		t.Fatalf("bawaan masa simpan: %+v", l.cfg)
	}
	if !st.lastRawBefore.Equal(at(0).AddDate(0, 0, -3)) {
		t.Fatalf("batas sampel mentah: %v", st.lastRawBefore)
	}
	if !st.lastMinBefore.Equal(at(0).AddDate(0, -24, 0)) {
		t.Fatalf("batas rekap menit: %v", st.lastMinBefore)
	}
}

func TestIdleVersusFan(t *testing.T) {
	c := &clock{at(0)}
	l := newLogger(t, t.TempDir(), c)
	l.Add(at(0), "on", 0, 216, 0, false) // AC mati: relay hidup, 0 W
	l.Add(at(30), "on", 0, 216, 0, false)
	eq(t, states(l), []string{"idle"})
	l.Add(at(60), "on", 35, 216, 0.2, false) // fan jalan
	eq(t, states(l), []string{"idle", "fan"})
	for s := 100; s <= 200; s += 30 {
		l.Add(at(s), "on", 390, 216, 1.8, false)
	}
	l.Add(at(230), "on", 35, 216, 0.2, false) // kompresor berhenti, fan lanjut
	eq(t, states(l), []string{"idle", "fan", "comp", "fan"})
	l.Add(at(260), "on", 0, 216, 0, false) // AC dimatikan dari remote
	eq(t, states(l), []string{"idle", "fan", "comp", "fan", "idle"})
}

// memStore: Store tiruan di memori. Membuktikan Logger hanya bergantung pada antarmuka Store
// (dan aturan idempotent per kunci waktu yang juga dipakai penyimpanan SQLite).
type memStore struct {
	raw    []RawRow
	mins   map[int64]MinRow
	events map[int64]Event
	pruned int

	lastRawBefore, lastMinBefore time.Time
}

func newMemStore() *memStore {
	return &memStore{mins: map[int64]MinRow{}, events: map[int64]Event{}}
}
func (m *memStore) AppendRaw(r []RawRow) error { m.raw = append(m.raw, r...); return nil }
func (m *memStore) AppendMinutes(r []MinRow) error {
	for _, x := range r {
		m.mins[x.T] = x
	}
	return nil
}
func (m *memStore) AppendEvents(r []Event) error {
	for _, x := range r {
		m.events[x.T] = x
	}
	return nil
}
func (m *memStore) LoadEvents(since int64) ([]Event, error) {
	var out []Event
	for _, e := range m.events {
		if e.T >= since {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out, nil
}
func (m *memStore) LoadMinutes(since int64) ([]MinRow, error) {
	var out []MinRow
	for _, x := range m.mins {
		if x.T >= since {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out, nil
}
func (m *memStore) LastRaw() (RawRow, bool, error) {
	if len(m.raw) == 0 {
		return RawRow{}, false, nil
	}
	return m.raw[len(m.raw)-1], true, nil
}
func (m *memStore) Prune(a, b time.Time) error {
	m.pruned++
	m.lastRawBefore, m.lastMinBefore = a, b
	return nil
}

func TestStoreInterfaceRoundTrip(t *testing.T) {
	st := newMemStore()
	c := &clock{at(0)}
	open := func() *Logger {
		l, err := Open("ac", Config{Store: st, Now: c.now})
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	l := open()
	l.Add(at(0), "on", 5, 220, 0.05, false)
	for s := 100; s <= 400; s += 30 {
		l.Add(at(s), "on", 390, 218, 1.8, false)
	}
	l.Add(at(430), "on", 5, 220, 0.05, false)
	l.Add(at(520), "on", 5, 220, 0.05, true)
	c.t = at(520)
	if err := l.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(st.raw) != 14 || len(st.events) != 3 || len(st.mins) == 0 {
		t.Fatalf("raw=%d events=%d mins=%d", len(st.raw), len(st.events), len(st.mins))
	}
	if st.pruned == 0 {
		t.Fatal("Prune harus dipanggil saat Open")
	}
	l2 := open()
	eq(t, states(l2), []string{"fan", "comp", "fan"})
	if !l2.hasPrev || l2.prev.t.Unix() != at(520).Unix() || l2.state != "fan" {
		t.Fatalf("pemulihan: %+v", l2.prev)
	}
	// flush ulang menimpa menit/kejadian yang sama, tidak menggandakan
	l2.Add(at(540), "on", 5, 220, 0.05, false)
	c.t = at(540)
	l2.Flush()
	if len(st.events) != 3 {
		t.Fatalf("event digandakan: %d", len(st.events))
	}
}
