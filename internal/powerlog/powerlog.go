// Paket powerlog mencatat daya perangkat Sonoff (POWR3) dari pengumuman mDNS:
// sampel mentah, rekap per menit, dan deteksi kompresor AC (off / fan / comp) berikut log kejadiannya.
//
// Data hanya berasal dari update mDNS (tidak ada polling tambahan). Penulisan ke disk dikumpulkan
// dan di-flush berkala supaya flash router tidak cepat aus.
package powerlog

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"
)

const (
	StateOff  = "off"  // relay mati
	StateFan  = "fan"  // relay hidup, kompresor mati, fan jalan (daya >= IdleW)
	StateIdle = "idle" // relay hidup tetapi AC mati / standby (daya < IdleW, mis. 0 W)
	StateComp = "comp" // kompresor menyala
	StateGap  = "gap"  // tidak ada data

	keepMinutes = 48 * time.Hour
	keepEvents  = 8 * 24 * time.Hour
	maxBuckets  = 3200
)

type Config struct {
	Store Store // penyimpanan (wajib)
	// NewStore (opsional): dipakai OpenAll untuk membuat Store per perangkat. Menimpa Store.
	NewStore     func(id string) (Store, error)
	CompOnW      float64       // daya di atas ini (bertahan Debounce) = kompresor ON. Default 300
	IdleW        float64       // daya di bawah ini (relay hidup) = AC mati/standby, bukan fan. Default 2
	CompOffW     float64       // daya di bawah ini = kompresor OFF; di antara keduanya status dipertahankan. Default 100
	Debounce     time.Duration // default 10s
	Gap          time.Duration // selang antar-update terlama yang masih dianggap berkesinambungan. Default 120s
	RawDays      int           // lama simpan sampel mentah. Default 3
	MinuteMonths int           // lama simpan rekap per menit. Default 24
	Now          func() time.Time
}

func (c *Config) setDefaults() {
	if c.CompOnW <= 0 {
		c.CompOnW = 300
	}
	if c.CompOffW <= 0 {
		c.CompOffW = 100
	}
	if c.IdleW <= 0 {
		c.IdleW = 2
	}
	if c.CompOffW > c.CompOnW {
		c.CompOffW = c.CompOnW
	}
	if c.Debounce <= 0 {
		c.Debounce = 10 * time.Second
	}
	if c.Gap <= 0 {
		c.Gap = 120 * time.Second
	}
	if c.RawDays <= 0 {
		c.RawDays = 3
	}
	if c.MinuteMonths <= 0 {
		c.MinuteMonths = 24
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

// Event adalah perpindahan status. Dur/KWh menggambarkan status SEBELUMNYA (Prev).
type Event struct {
	T     int64   `json:"t"`
	State string  `json:"state"`
	W     float64 `json:"w"`
	Prev  string  `json:"prev,omitempty"`
	Dur   int64   `json:"dur,omitempty"`
	KWh   float64 `json:"kwh,omitempty"`
}

type sample struct {
	t       time.Time
	sw      string
	w, v, a float64
}

// bucket = rekap satu menit. Rata-rata dibobot waktu (nilai terakhir dianggap bertahan sampai update berikutnya).
type bucket struct {
	t                     int64
	wsec, vsec, asec, cov float64
	min, max              float64
	n                     int
	seen                  bool
	lastW, lastV, lastA   float64
}

func (b *bucket) upd(w float64) {
	if !b.seen {
		b.min, b.max, b.seen = w, w, true
		return
	}
	if w < b.min {
		b.min = w
	}
	if w > b.max {
		b.max = w
	}
}

func (b *bucket) avg() float64 {
	if b.cov > 0 {
		return b.wsec / b.cov
	}
	return b.lastW
}

func (b *bucket) avgV() float64 {
	if b.cov > 0 {
		return b.vsec / b.cov
	}
	return b.lastV
}

func (b *bucket) avgA() float64 {
	if b.cov > 0 {
		return b.asec / b.cov
	}
	return b.lastA
}

type Logger struct {
	id    string
	cfg   Config
	store Store

	mu        sync.Mutex
	hasPrev   bool
	prev      sample
	state     string
	segStart  time.Time
	segJ      float64 // joule pada segmen berjalan
	pend      bool
	pendStart time.Time
	pendJ     float64
	buckets   []*bucket
	bflushed  int
	events    []Event
	eflushed  int
	raw       []RawRow
	pushed    int
	replied   int
	lastPrune string
}

// Open membuka (dan memuat) log dari cfg.Store.
func Open(id string, cfg Config) (*Logger, error) {
	cfg.setDefaults()
	if cfg.Store == nil {
		return nil, fmt.Errorf("powerlog %s: Store wajib diisi", id)
	}
	l := &Logger{id: id, cfg: cfg, store: cfg.Store}
	l.load()
	l.prune()
	return l, nil
}

func minuteOf(t time.Time) int64 { return t.Unix() / 60 * 60 }

/* ---------------- pencatatan ---------------- */

// Add mencatat satu update mDNS. viaQuery = update datang segera setelah query kita (bukan spontan dari perangkat).
func (l *Logger) Add(t time.Time, sw string, w, v, a float64, viaQuery bool) {
	if t.Year() < 2024 { // jam router belum sinkron (NTP)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hasPrev && t.Before(l.prev.t) {
		return
	}
	if viaQuery {
		l.replied++
	} else {
		l.pushed++
	}
	src := "p"
	if viaQuery {
		src = "q"
	}
	l.raw = append(l.raw, RawRow{T: float64(t.UnixMilli()) / 1000, W: w, V: v, A: a, Sw: sw, Src: src})

	if l.hasPrev {
		if t.Sub(l.prev.t) > l.cfg.Gap {
			l.closeGap()
		} else {
			l.integrate(l.prev, t)
		}
	}
	b := l.bucketFor(minuteOf(t))
	b.n++
	b.upd(w)
	b.lastW, b.lastV, b.lastA = w, v, a

	l.step(t, sw, w)
	l.prev = sample{t, sw, w, v, a}
	l.hasPrev = true
	l.trim()
}

// closeGap menutup segmen berjalan di waktu update terakhir karena data terputus.
func (l *Logger) closeGap() {
	if l.pend {
		l.segJ += l.pendJ
		l.pend = false
	}
	if l.state != "" && l.state != StateGap {
		at := l.prev.t
		if at.Before(l.segStart) {
			at = l.segStart
		}
		l.emit(at, StateGap, l.prev.w)
	}
}

// integrate membagi interval [p.t, t] (nilai p bertahan) ke bucket menit dan ke energi segmen.
func (l *Logger) integrate(p sample, t time.Time) {
	dt := t.Sub(p.t).Seconds()
	if dt <= 0 {
		return
	}
	if l.pend {
		l.pendJ += p.w * dt
	} else {
		l.segJ += p.w * dt
	}
	cur := p.t
	for cur.Before(t) {
		mStart := minuteOf(cur)
		end := time.Unix(mStart+60, 0)
		if end.After(t) {
			end = t
		}
		sec := end.Sub(cur).Seconds()
		b := l.bucketFor(mStart)
		b.wsec += p.w * sec
		b.vsec += p.v * sec
		b.asec += p.a * sec
		b.cov += sec
		b.upd(p.w)
		cur = end
	}
}

func (l *Logger) bucketFor(m int64) *bucket {
	if n := len(l.buckets); n > 0 {
		if last := l.buckets[n-1]; last.t == m {
			return last
		} else if last.t > m { // seharusnya jarang: cari mundur
			for i := n - 1; i >= 0 && i >= n-5; i-- {
				if l.buckets[i].t == m {
					return l.buckets[i]
				}
			}
			m = last.t
			return last
		}
	}
	b := &bucket{t: m}
	l.buckets = append(l.buckets, b)
	return b
}

func (l *Logger) emit(at time.Time, state string, w float64) {
	ev := Event{T: at.Unix(), State: state, W: round(w, 1)}
	if l.state != "" {
		ev.Prev = l.state
		ev.Dur = int64(at.Sub(l.segStart).Seconds() + 0.5)
		ev.KWh = round(l.segJ/3.6e6, 4)
	}
	l.events = append(l.events, ev)
	l.state, l.segStart, l.segJ = state, at, 0
}

// low = status saat kompresor mati: Standby (AC mati) bila daya hampir nol, selain itu Fan.
func (l *Logger) low(w float64) string {
	if w < l.cfg.IdleW {
		return StateIdle
	}
	return StateFan
}

func (l *Logger) step(t time.Time, sw string, w float64) {
	if sw == "off" {
		if l.state != StateOff {
			l.cancelPend()
			l.emit(t, StateOff, w)
		}
		return
	}
	switch l.state {
	case "", StateGap, StateOff:
		s := l.low(w)
		if w >= l.cfg.CompOnW {
			s = StateComp
		}
		l.pend = false
		l.emit(t, s, w)
	case StateFan, StateIdle:
		if w >= l.cfg.CompOnW {
			if !l.pend {
				l.pend, l.pendStart, l.pendJ = true, t, 0
			} else if t.Sub(l.pendStart) >= l.cfg.Debounce {
				// kompresor dianggap menyala sejak update pertama yang melewati ambang
				j := l.pendJ
				l.pend = false
				l.emit(l.pendStart, StateComp, w)
				l.segJ = j
			}
		} else {
			l.cancelPend()
			if s := l.low(w); s != l.state {
				l.emit(t, s, w)
			}
		}
	case StateComp:
		if w < l.cfg.CompOffW {
			l.emit(t, l.low(w), w)
		}
	}
}

func (l *Logger) cancelPend() {
	if l.pend {
		l.segJ += l.pendJ
		l.pend = false
	}
}

func (l *Logger) trim() {
	if n := len(l.buckets); n > maxBuckets {
		drop := n - maxBuckets
		if drop > l.bflushed {
			drop = l.bflushed
		}
		if drop > 0 {
			l.buckets = append([]*bucket(nil), l.buckets[drop:]...)
			l.bflushed -= drop
		}
	}
	if n := len(l.events); n > 4000 {
		drop := n - 3000
		if drop > l.eflushed {
			drop = l.eflushed
		}
		if drop > 0 {
			l.events = append([]Event(nil), l.events[drop:]...)
			l.eflushed -= drop
		}
	}
}

func round(v float64, d int) float64 {
	p := 1.0
	for i := 0; i < d; i++ {
		p *= 10
	}
	if v < 0 {
		return float64(int64(v*p-0.5)) / p
	}
	return float64(int64(v*p+0.5)) / p
}

/* ---------------- tampilan ---------------- */

type Live struct {
	State    string  `json:"state"` // off | idle | fan | comp | gap
	Since    int64   `json:"since"` // unix detik awal status
	Power    float64 `json:"power"`
	Voltage  float64 `json:"voltage"`
	Current  float64 `json:"current"`
	LastSeen int64   `json:"last_seen"`
	Pushed   int     `json:"pushed"`  // jumlah update spontan dari perangkat (sejak dashboard jalan)
	Replied  int     `json:"replied"` // jumlah update yang datang tepat setelah query kita
	CompOnW  float64 `json:"comp_on_w"`
	CompOffW float64 `json:"comp_off_w"`
}

func (l *Logger) Live() Live {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.live()
}

func (l *Logger) live() Live {
	lv := Live{State: l.state, Since: l.segStart.Unix(), Pushed: l.pushed, Replied: l.replied,
		CompOnW: l.cfg.CompOnW, CompOffW: l.cfg.CompOffW}
	if !l.hasPrev {
		lv.State = StateGap
		lv.Since = 0
		return lv
	}
	lv.Power, lv.Voltage, lv.Current, lv.LastSeen = l.prev.w, l.prev.v, l.prev.a, l.prev.t.Unix()
	if l.cfg.Now().Sub(l.prev.t) > l.cfg.Gap {
		lv.State = StateGap
		lv.Since = l.prev.t.Unix()
	}
	return lv
}

type Seg struct {
	T0    int64  `json:"t0"`
	T1    int64  `json:"t1"`
	State string `json:"state"`
}

type Stats struct {
	CompSec       int64   `json:"comp_sec"`
	FanSec        int64   `json:"fan_sec"`
	IdleSec       int64   `json:"idle_sec"`
	OffSec        int64   `json:"off_sec"`
	GapSec        int64   `json:"gap_sec"`
	Duty          float64 `json:"duty"` // porsi waktu kompresor dari waktu relay hidup (0..1)
	Cycles        int     `json:"cycles"`
	AvgCompSec    float64 `json:"avg_comp_sec"`
	AvgRestSec    float64 `json:"avg_rest_sec"`
	CyclesPerHour float64 `json:"cycles_per_hour"`
	KWh           float64 `json:"kwh"`
	KWhPerCycle   float64 `json:"kwh_per_cycle"`
	MaxW          float64 `json:"max_w"`
}

type History struct {
	Now     int64       `json:"now"`
	From    int64       `json:"from"`
	Live    Live        `json:"live"`
	Minutes [][]float64 `json:"minutes"` // [t, rata2, min, maks]
	Segs    []Seg       `json:"segs"`
	Events  []Event     `json:"events"`
	Stats   Stats       `json:"stats"`
}

// History menyusun data grafik untuk `hours` jam terakhir (maks 48).
func (l *Logger) History(hours int) History {
	if hours < 1 {
		hours = 1
	}
	if hours > 48 {
		hours = 48
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.cfg.Now()
	from := now.Add(-time.Duration(hours) * time.Hour).Unix()
	h := History{Now: now.Unix(), From: from, Live: l.live(), Minutes: [][]float64{}, Segs: []Seg{}, Events: []Event{}}

	var kwh, maxW float64
	for _, b := range l.buckets {
		if b.t+60 < from {
			continue
		}
		h.Minutes = append(h.Minutes, []float64{float64(b.t), round(b.avg(), 1), round(b.min, 1), round(b.max, 1)})
		kwh += b.wsec / 3.6e6
		if b.max > maxW {
			maxW = b.max
		}
	}

	// segmen dari log kejadian, dipotong ke jendela
	evs := l.events
	for i, e := range evs {
		end := now.Unix()
		if i+1 < len(evs) {
			end = evs[i+1].T
		}
		if end <= from {
			continue
		}
		start := e.T
		if start < from {
			start = from
		}
		h.Segs = append(h.Segs, Seg{start, end, e.State})
	}
	if lv := h.Live; lv.State == StateGap && len(h.Segs) > 0 && l.state != StateGap && l.hasPrev {
		// data berhenti tetapi kejadian "gap" belum tercatat
		last := &h.Segs[len(h.Segs)-1]
		pt := l.prev.t.Unix()
		if pt >= last.T0 && pt < last.T1 {
			last.T1 = pt
			if last.T1 <= last.T0 {
				h.Segs = h.Segs[:len(h.Segs)-1]
			}
			h.Segs = append(h.Segs, Seg{pt, now.Unix(), StateGap})
		}
	}

	var st Stats
	for _, s := range h.Segs {
		d := s.T1 - s.T0
		switch s.State {
		case StateComp:
			st.CompSec += d
		case StateFan:
			st.FanSec += d
		case StateIdle:
			st.IdleSec += d
		case StateOff:
			st.OffSec += d
		default:
			st.GapSec += d
		}
	}
	if on := st.CompSec + st.FanSec; on > 0 {
		st.Duty = float64(st.CompSec) / float64(on)
		st.CyclesPerHour = 0
	}
	var compDur, restDur, compKWh float64
	var nComp, nRest int
	for i, e := range evs {
		if e.T < from {
			continue
		}
		h.Events = append(h.Events, e)
		if e.State == StateComp {
			st.Cycles++
		}
		if e.Prev == StateComp && e.State != StateGap {
			compDur += float64(e.Dur)
			compKWh += e.KWh
			nComp++
		}
		if e.Prev == StateFan && e.State == StateComp && i > 0 && evs[i-1].Prev == StateComp && evs[i-1].State == StateFan {
			restDur += float64(e.Dur)
			nRest++
		}
	}
	if nComp > 0 {
		st.AvgCompSec = compDur / float64(nComp)
		st.KWhPerCycle = round(compKWh/float64(nComp), 3)
	}
	if nRest > 0 {
		st.AvgRestSec = restDur / float64(nRest)
	}
	if on := st.CompSec + st.FanSec; on > 0 {
		st.CyclesPerHour = round(float64(st.Cycles)/(float64(on)/3600), 2)
	}
	st.KWh = round(kwh, 3)
	st.MaxW = round(maxW, 1)
	h.Stats = st
	if len(h.Events) > 200 {
		h.Events = h.Events[len(h.Events)-200:]
	}
	return h
}

/* ---------------- penyimpanan (lewat Store) ---------------- */

// Flush menulis data tertunda ke penyimpanan. Bucket menit yang masih bisa bertambah tidak ditulis.
func (l *Logger) Flush() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// sampel mentah
	if len(l.raw) > 0 {
		if err := l.store.AppendRaw(l.raw); err != nil {
			note(err)
			if len(l.raw) > 50000 { // penyimpanan bermasalah: jangan biarkan RAM membengkak
				l.raw = l.raw[len(l.raw)-20000:]
			}
		} else {
			l.raw = nil
		}
	}

	// rekap menit yang sudah final
	closedEnd := l.bflushed
	if l.hasPrev {
		cut := minuteOf(l.prev.t)
		for closedEnd < len(l.buckets) && l.buckets[closedEnd].t < cut {
			closedEnd++
		}
	}
	if closedEnd > l.bflushed {
		rows := make([]MinRow, 0, closedEnd-l.bflushed)
		for _, b := range l.buckets[l.bflushed:closedEnd] {
			rows = append(rows, MinRow{T: b.t, Avg: b.avg(), Min: b.min, Max: b.max, V: b.avgV(), A: b.avgA(), Cov: b.cov})
		}
		if err := l.store.AppendMinutes(rows); err != nil {
			note(err)
		} else {
			l.bflushed = closedEnd
		}
	}

	// log kejadian
	if l.eflushed < len(l.events) {
		if err := l.store.AppendEvents(l.events[l.eflushed:]); err != nil {
			note(err)
		} else {
			l.eflushed = len(l.events)
		}
	}
	if day := l.cfg.Now().Format("20060102"); day != l.lastPrune {
		l.pruneLocked()
	}
	return firstErr
}

func (l *Logger) prune() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked()
}

// pruneLocked menghapus sampel mentah dan rekap menit yang melewati masa simpan.
func (l *Logger) pruneLocked() {
	now := l.cfg.Now()
	l.lastPrune = now.Format("20060102")
	if err := l.store.Prune(now.AddDate(0, 0, -l.cfg.RawDays), now.AddDate(0, -l.cfg.MinuteMonths, 0)); err != nil {
		log.Printf("powerlog %s: gagal membersihkan data lama: %v", l.id, err)
	}
}

// load memulihkan event, rekap menit 48 jam terakhir, dan sampel terakhir dari penyimpanan.
func (l *Logger) load() {
	now := l.cfg.Now()

	evs, err := l.store.LoadEvents(now.Add(-keepEvents).Unix())
	if err != nil {
		log.Printf("powerlog %s: gagal memuat kejadian: %v", l.id, err)
	}
	l.events = evs
	l.eflushed = len(l.events)
	if n := len(l.events); n > 0 {
		last := l.events[n-1]
		l.state, l.segStart = last.State, time.Unix(last.T, 0)
	}

	rows, err := l.store.LoadMinutes(now.Add(-keepMinutes).Unix())
	if err != nil {
		log.Printf("powerlog %s: gagal memuat rekap menit: %v", l.id, err)
	}
	seen := map[int64]int{}
	for _, r := range rows {
		b := &bucket{t: r.T, min: r.Min, max: r.Max, cov: r.Cov, seen: true, n: 1,
			wsec: r.Avg * r.Cov, vsec: r.V * r.Cov, asec: r.A * r.Cov, lastW: r.Avg, lastV: r.V, lastA: r.A}
		if i, ok := seen[r.T]; ok {
			l.buckets[i] = b
			continue
		}
		seen[r.T] = len(l.buckets)
		l.buckets = append(l.buckets, b)
	}
	sort.Slice(l.buckets, func(i, j int) bool { return l.buckets[i].t < l.buckets[j].t })
	l.bflushed = len(l.buckets)

	// sampel mentah terakhir (untuk mendeteksi gap setelah restart)
	if r, ok, err := l.store.LastRaw(); err != nil {
		log.Printf("powerlog %s: gagal memuat sampel terakhir: %v", l.id, err)
	} else if ok {
		l.prev = sample{t: time.UnixMilli(int64(r.T * 1000)), w: r.W, v: r.V, a: r.A, sw: r.Sw}
		l.hasPrev = true
	}
}

/* ---------------- banyak perangkat ---------------- */

type Registry struct {
	// OnUpdate dipanggil (tanpa lock) setelah ada sampel baru atau berkala.
	OnUpdate func(map[string]Live)
	mu       sync.RWMutex
	cfg      Config
	loggers  map[string]*Logger
	order    []string
}

// OpenAll membuka satu Logger per id dengan Store dari cfg.NewStore.
func OpenAll(ids []string, cfg Config) (*Registry, error) {
	r := &Registry{cfg: cfg, loggers: map[string]*Logger{}}
	for _, id := range ids {
		if err := r.Add(id); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *Registry) Add(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.loggers[id]; ok {
		return nil
	}
	if r.cfg.NewStore == nil {
		return fmt.Errorf("powerlog %s: NewStore wajib diisi", id)
	}
	st, err := r.cfg.NewStore(id)
	if err != nil {
		return fmt.Errorf("powerlog %s: %w", id, err)
	}
	c := r.cfg
	c.Store = st
	l, err := Open(id, c)
	if err != nil {
		return fmt.Errorf("powerlog %s: %w", id, err)
	}
	r.loggers[id] = l
	r.order = append(r.order, id)
	return nil
}

func (r *Registry) Has(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.loggers[id]
	return ok
}

func (r *Registry) Record(id string, t time.Time, sw string, w, v, a float64, viaQuery bool) {
	r.mu.RLock()
	l := r.loggers[id]
	r.mu.RUnlock()
	if l == nil {
		return
	}
	l.Add(t, sw, w, v, a, viaQuery)
	if r.OnUpdate != nil {
		r.OnUpdate(r.Lives())
	}
}

func (r *Registry) Lives() map[string]Live {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Live, len(r.loggers))
	for id, l := range r.loggers {
		out[id] = l.Live()
	}
	return out
}

// DailyKWh menjumlahkan energi (daya rata-rata x lama tercakup) per tanggal lokal "YYYY-MM-DD" sejak `since`,
// dari rekap menit di penyimpanan ditambah rekap yang masih di memori. Nilai bisa lebih kecil dari
// sebenarnya bila ada menit tanpa data. ok=false bila id tidak dikenal.
func (r *Registry) DailyKWh(id string, since time.Time, loc *time.Location) (map[string]float64, bool) {
	r.mu.RLock()
	l := r.loggers[id]
	r.mu.RUnlock()
	if l == nil {
		return nil, false
	}
	rows, err := l.store.LoadMinutes(since.Unix())
	if err != nil {
		log.Printf("powerlog %s: gagal memuat rekap menit: %v", id, err)
	}
	ws := map[int64]float64{}
	for _, m := range rows {
		ws[m.T] = m.Avg * m.Cov
	}
	l.mu.Lock()
	for _, b := range l.buckets {
		if b.t >= since.Unix() {
			ws[b.t] = b.wsec
		}
	}
	l.mu.Unlock()
	out := map[string]float64{}
	for t, w := range ws {
		out[time.Unix(t, 0).In(loc).Format("2006-01-02")] += w / 3.6e6
	}
	for k, v := range out {
		out[k] = round(v, 3)
	}
	return out, true
}

func (r *Registry) History(id string, hours int) (History, bool) {
	r.mu.RLock()
	l := r.loggers[id]
	r.mu.RUnlock()
	if l == nil {
		return History{}, false
	}
	return l.History(hours), true
}

// Run menulis ke disk tiap flushEvery dan menyiarkan status tiap 10 detik (supaya "gap" terdeteksi
// walau tak ada update). Saat ctx selesai, data tertunda ditulis dulu.
func (r *Registry) Run(ctx context.Context, flushEvery time.Duration) {
	if flushEvery <= 0 {
		flushEvery = 5 * time.Minute
	}
	fl := time.NewTicker(flushEvery)
	pub := time.NewTicker(10 * time.Second)
	defer fl.Stop()
	defer pub.Stop()
	for {
		select {
		case <-ctx.Done():
			r.FlushAll()
			return
		case <-fl.C:
			r.FlushAll()
		case <-pub.C:
			if r.OnUpdate != nil {
				r.OnUpdate(r.Lives())
			}
		}
	}
}

func (r *Registry) FlushAll() {
	r.mu.RLock()
	ids := append([]string(nil), r.order...)
	loggers := make([]*Logger, 0, len(ids))
	for _, id := range ids {
		if l := r.loggers[id]; l != nil {
			loggers = append(loggers, l)
		}
	}
	r.mu.RUnlock()
	for i, l := range loggers {
		if err := l.Flush(); err != nil {
			log.Printf("powerlog %s: gagal menulis: %v", ids[i], err)
		}
	}
}

// SyncEvent adalah kejadian status daya satu perangkat untuk sinkronisasi ke klien.
type SyncEvent struct {
	ID string `json:"id"`
	Event
}

// EventsSince mengembalikan kejadian semua perangkat dengan T >= since (urut waktu).
func (r *Registry) EventsSince(since int64) []SyncEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []SyncEvent
	for id, l := range r.loggers {
		l.mu.Lock()
		for _, e := range l.events {
			if e.T >= since {
				out = append(out, SyncEvent{id, e})
			}
		}
		l.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].T != out[j].T {
			return out[i].T < out[j].T
		}
		return out[i].ID < out[j].ID
	})
	return out
}
