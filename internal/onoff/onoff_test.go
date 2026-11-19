package onoff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

func at(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

type clk struct{ t time.Time }

func (c *clk) now() time.Time { return c.t }

func TestAddDedupeAndHistory(t *testing.T) {
	c := &clk{at(1000)}
	l, err := Open(t.TempDir(), c.now)
	if err != nil {
		t.Fatal(err)
	}
	if !l.Add("dev1", at(0), On) || l.Add("dev1", at(10), On) {
		t.Fatal("status sama harus dibuang")
	}
	l.Add("dev1", at(100), Off)
	l.Add("dev1", at(400), On)
	l.Add("dev1", at(700), Gap)
	h, ok := l.History("dev1", 1)
	if !ok {
		t.Fatal("riwayat tidak ada")
	}
	if h.Stats.OnSec != 100+300 || h.Stats.OffSec != 300 || h.Stats.GapSec != 300 || h.Stats.Toggles != 2 {
		t.Fatalf("stats: %+v", h.Stats)
	}
	if last, _ := l.Last("dev1"); last.State != Gap || last.T != at(700).Unix() {
		t.Fatalf("last: %+v", last)
	}
	if _, ok := l.History("tidak-ada", 1); ok {
		t.Fatal("id tak dikenal harus ok=false")
	}
}

func TestWindowClipsAndKeepsStartState(t *testing.T) {
	c := &clk{t0.Add(3 * time.Hour)}
	l, _ := Open(t.TempDir(), c.now)
	l.Add("d", t0, On) // 3 jam lalu, tetap ON sampai sekarang
	h, _ := l.History("d", 1)
	if len(h.Segs) != 1 || h.Segs[0].T0 != h.From || h.Segs[0].State != On || h.Stats.OnSec != 3600 {
		t.Fatalf("%+v", h)
	}
	if len(h.Events) != 0 {
		t.Fatal("kejadian di luar rentang tidak boleh ikut")
	}
}

func TestPersistAndGapAfterRestart(t *testing.T) {
	dir := t.TempDir()
	c := &clk{at(0)}
	l, _ := Open(dir, c.now)
	l.Add("d", at(0), On)
	c.t = at(600)
	l.Touch() // dashboard terakhir hidup di detik 600, lalu mati
	c.t = at(5000)
	l2, _ := Open(dir, c.now)
	last, _ := l2.Last("d")
	if last.State != Gap || last.T != at(600).Unix() {
		t.Fatalf("setelah restart harus gap sejak 600: %+v", last)
	}
	h, _ := l2.History("d", 2)
	if h.Stats.OnSec != 600 || h.Stats.GapSec != 4400 {
		t.Fatalf("%+v", h.Stats)
	}
}

func TestPruneOldAndRejectBadInput(t *testing.T) {
	dir := t.TempDir()
	c := &clk{t0}
	l, _ := Open(dir, c.now)
	if l.Add("../x", t0, On) || l.Add("d", t0, "bogus") || l.Add("d", time.Unix(5, 0), On) {
		t.Fatal("input buruk harus ditolak")
	}
	old := t0.Add(-keep - 3*24*time.Hour)
	l.Add("d", old, On)
	l.Add("d", old.Add(time.Hour), Off)
	l.Add("d", t0.Add(-time.Hour), On)
	c.t = t0
	l2, _ := Open(dir, c.now)
	b, _ := os.ReadFile(filepath.Join(dir, "d.log"))
	if n := len(l2.ev["d"]); n != 2 { // satu sebelum batas (Off) + satu baru
		t.Fatalf("setelah prune %d kejadian:\n%s", n, b)
	}
}

func TestSeconds(t *testing.T) {
	c := &clk{at(1000)}
	l, _ := Open(t.TempDir(), c.now)
	l.Add("d", at(100), On)
	l.Add("d", at(400), Off)
	l.Add("d", at(700), Gap)
	on, off, gap, ok := l.Seconds("d", at(0).Unix(), at(1000).Unix())
	if !ok || on != 300 || off != 300 || gap != 300 { // sebelum at(100) tidak dihitung
		t.Fatalf("on=%d off=%d gap=%d", on, off, gap)
	}
	on, off, gap, _ = l.Seconds("d", at(200).Unix(), at(500).Unix())
	if on != 200 || off != 100 || gap != 0 {
		t.Fatalf("sebagian: on=%d off=%d gap=%d", on, off, gap)
	}
	if _, _, _, ok := l.Seconds("tidak-ada", 0, 10); ok {
		t.Fatal("perangkat tanpa riwayat harus ok=false")
	}
}

type memStore struct {
	ev    map[string][]Event
	alive int64
}

func (m *memStore) Load(cut int64) (map[string][]Event, error) {
	out := map[string][]Event{}
	for id, evs := range m.ev {
		out[id] = append([]Event(nil), evs...)
	}
	return out, nil
}
func (m *memStore) Append(id string, e Event) error { m.ev[id] = append(m.ev[id], e); return nil }
func (m *memStore) Replace(id string, evs []Event) error {
	m.ev[id] = append([]Event(nil), evs...)
	return nil
}
func (m *memStore) Alive() (int64, bool)   { return m.alive, m.alive > 0 }
func (m *memStore) SetAlive(t int64) error { m.alive = t; return nil }

func TestStoreInterface(t *testing.T) {
	st := &memStore{ev: map[string][]Event{}}
	c := &clk{at(1000)}
	l, err := OpenStore(st, c.now)
	if err != nil {
		t.Fatal(err)
	}
	l.Add("d", at(100), On)
	l.Add("d", at(400), Off)
	l.Touch()
	if len(st.ev["d"]) != 2 || st.alive != at(1000).Unix() {
		t.Fatalf("store: %+v alive=%d", st.ev, st.alive)
	}
	// restart: status terakhir jadi gap sejak waktu hidup terakhir
	c.t = at(5000)
	l2, _ := OpenStore(st, c.now)
	last, _ := l2.Last("d")
	if last.State != Gap || last.T != at(1000).Unix() {
		t.Fatalf("harus gap sejak 1000: %+v", last)
	}
	if len(st.ev["d"]) != 3 {
		t.Fatalf("gap harus ikut tersimpan: %+v", st.ev["d"])
	}
}

func TestSinceBridgesGapOnlyWhenStateSame(t *testing.T) {
	dir := t.TempDir()
	c := &clk{at(0)}
	l, _ := Open(dir, c.now)
	if _, ok := l.Since("d"); ok {
		t.Fatal("tanpa riwayat harus ok=false")
	}
	l.Add("d", at(0), On)
	if s, ok := l.Since("d"); !ok || s != at(0).Unix() {
		t.Fatalf("since awal: %d %v", s, ok)
	}
	c.t = at(600)
	l.Touch() // gateway mati di 600, hidup lagi di 5000
	c.t = at(5000)
	l2, _ := Open(dir, c.now)
	if _, ok := l2.Since("d"); ok {
		t.Fatal("saat gap belum ada status: ok=false")
	}
	l2.Add("d", at(5001), On) // status sama setelah restart: hitungan tidak putus
	if s, _ := l2.Since("d"); s != at(0).Unix() {
		t.Fatalf("harus tetap sejak 0, dapat %d", s)
	}
	// restart lagi, kali ini statusnya berbeda: hitungan mulai dari waktu gateway melihatnya
	c.t = at(6000)
	l2.Touch()
	c.t = at(9000)
	l3, _ := Open(dir, c.now)
	l3.Add("d", at(9002), Off)
	if s, _ := l3.Since("d"); s != at(9002).Unix() {
		t.Fatalf("status berubah saat gateway mati: harus %d, dapat %d", at(9002).Unix(), s)
	}
	// ON -> OFF normal tanpa gap
	l3.Add("d", at(9100), On)
	if s, _ := l3.Since("d"); s != at(9100).Unix() {
		t.Fatalf("%d", s)
	}
}

func TestShortGapsIgnoredOnRead(t *testing.T) {
	c := &clk{at(10000)}
	l, _ := Open(t.TempDir(), c.now)
	// ON@0, gap pendek 100 dtk lalu ON lagi; kemudian gap 130 dtk lalu OFF
	l.Add("d", at(0), On)
	l.add("d", Event{at(1000).Unix(), Gap}) // data lama: gap 100 dtk (< 2 menit) disimpan di penyimpanan
	l.add("d", Event{at(1100).Unix(), On})
	l.add("d", Event{at(2000).Unix(), Gap}) // gap 130 dtk (>= 2 menit): tetap dihitung
	l.add("d", Event{at(2130).Unix(), Off})
	h, _ := l.History("d", 24)
	// tersisa: ON 0-2000, GAP 2000-2130, OFF 2130-10000
	if len(h.Events) != 3 || h.Events[0].State != On || h.Events[1].State != Gap || h.Events[2].State != Off {
		t.Fatalf("kejadian: %+v", h.Events)
	}
	if h.Stats.GapSec != 130 || h.Stats.OnSec != 2000 {
		t.Fatalf("statistik: %+v", h.Stats)
	}
	on, off, gap, _ := l.Seconds("d", at(0).Unix(), at(10000).Unix())
	if on != 2000 || gap != 130 || off != 7870 {
		t.Fatalf("seconds: %d %d %d", on, off, gap)
	}
	if s, _ := l.Since("d"); s != at(2130).Unix() {
		t.Fatalf("since: %d", s)
	}
	// data di penyimpanan tidak diubah
	if last, _ := l.Last("d"); last.State != Off {
		t.Fatalf("%+v", last)
	}
}

func TestOngoingShortGapAndLeadingGap(t *testing.T) {
	c := &clk{at(1050)}
	l, _ := Open(t.TempDir(), c.now)
	l.Add("d", at(0), On)
	l.add("d", Event{at(1000).Unix(), Gap}) // baru 50 dtk: masih dianggap ON
	if s, ok := l.Since("d"); !ok || s != at(0).Unix() {
		t.Fatalf("gap pendek yang berlangsung harus diabaikan: %d %v", s, ok)
	}
	h, _ := l.History("d", 24)
	if h.Stats.GapSec != 0 || h.Stats.OnSec != 1050 {
		t.Fatalf("%+v", h.Stats)
	}
	c.t = at(1200) // sudah 200 dtk: sekarang gap
	if _, ok := l.Since("d"); ok {
		t.Fatal("gap > 2 menit harus terlihat sebagai gap")
	}
	// gap di awal riwayat dipertahankan
	l2, _ := Open(t.TempDir(), c.now)
	l2.add("x", Event{at(1000).Unix(), Gap})
	l2.add("x", Event{at(1010).Unix(), On})
	h2, _ := l2.History("x", 24)
	if h2.Stats.GapSec != 10 {
		t.Fatalf("gap awal: %+v", h2.Stats)
	}
}

func TestSourceOfEvents(t *testing.T) {
	st := &memStore{ev: map[string][]Event{}}
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	cur := now.Add(-3 * time.Hour)
	l, _ := OpenStore(st, func() time.Time { return cur })
	l.SetSrcFile(t.TempDir() + "/src.txt")
	l.Add("a", cur, On) // status awal: tanpa sumber
	cur = cur.Add(time.Hour)
	l.NoteCmd("a", "Scene: Pagi")
	cur = cur.Add(5 * time.Second)
	l.Add("a", cur, Off) // oleh scene
	cur = cur.Add(time.Hour)
	l.Add("a", cur, On) // tanpa perintah: perangkat
	cur = now
	h, _ := l.History("a", 24)
	got := []string{}
	for _, e := range h.Events {
		got = append(got, e.State+":"+e.Src)
	}
	if want := "on: off:Scene: Pagi on:Perangkat"; strings.Join(got, " ") != want {
		t.Fatalf("got %q want %q", strings.Join(got, " "), want)
	}
}
