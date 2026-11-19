// Paket onoff mencatat riwayat ON/OFF (dan "tidak terdengar") tiap perangkat Sonoff.
// Hanya perubahan yang disimpan (satu baris "unix,state" per kejadian), jadi kecil dan hemat flash.
package onoff

import (
	"log"
	"sync"
	"time"
)

const (
	On  = "on"
	Off = "off"
	Gap = "gap" // perangkat tidak terdengar (status tidak diketahui)

	minGap    = 120                  // detik: "tidak terdengar" yang lebih singkat dari ini tidak dihitung dan tidak ditampilkan
	keep      = 400 * 24 * time.Hour // cukup untuk rekap bulanan/tahunan; hanya perubahan yang disimpan
	maxEvents = 40000
)

type Event struct {
	T     int64  `json:"t"`
	State string `json:"state"`
}

type Seg struct {
	T0    int64  `json:"t0"`
	T1    int64  `json:"t1"`
	State string `json:"state"`
}

type Stats struct {
	OnSec   int64 `json:"on_sec"`
	OffSec  int64 `json:"off_sec"`
	GapSec  int64 `json:"gap_sec"`
	Toggles int   `json:"toggles"` // jumlah perpindahan ON<->OFF dalam rentang
}

type Hist struct {
	Now    int64    `json:"now"`
	From   int64    `json:"from"`
	Segs   []Seg    `json:"segs"`
	Events []HEvent `json:"events"`
	Stats  Stats    `json:"stats"`
}

type Log struct {
	mu    sync.Mutex
	store Store
	now   func() time.Time
	ev    map[string][]Event
	src   *srcLog
}

func validID(id string) bool {
	if id == "" || len(id) > 40 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// Open memakai penyimpanan file di dir (satu file per perangkat). Lihat OpenStore.
func Open(dir string, now func() time.Time) (*Log, error) {
	fs, err := NewFileStore(dir)
	if err != nil {
		return nil, err
	}
	return OpenStore(fs, now)
}

// OpenStore memuat riwayat dari st. now boleh nil (waktu nyata). Perangkat yang terakhir tercatat
// ON/OFF diberi kejadian "gap" pada waktu terakhir dashboard hidup (penanda alive), karena selama
// dashboard mati statusnya tidak diketahui.
func OpenStore(st Store, now func() time.Time) (*Log, error) {
	if now == nil {
		now = time.Now
	}
	l := &Log{store: st, now: now, ev: map[string][]Event{}}
	all, err := st.Load(now().Add(-keep).Unix())
	if err != nil {
		return nil, err
	}
	for id, evs := range all {
		if validID(id) {
			l.ev[id] = evs
		}
	}
	if at, ok := st.Alive(); ok && at > 0 {
		for id, evs := range l.ev {
			if n := len(evs); n > 0 && evs[n-1].State != Gap && at >= evs[n-1].T {
				l.add(id, Event{at, Gap})
			}
		}
	}
	return l, nil
}

func (l *Log) add(id string, e Event) {
	l.ev[id] = append(l.ev[id], e)
	if err := l.store.Append(id, e); err != nil {
		log.Printf("onoff %s: gagal menyimpan: %v", id, err)
	}
	if len(l.ev[id]) > maxEvents {
		l.ev[id] = append([]Event(nil), l.ev[id][len(l.ev[id])-maxEvents*3/4:]...)
		if err := l.store.Replace(id, l.ev[id]); err != nil {
			log.Printf("onoff %s: gagal memangkas: %v", id, err)
		}
	}
}

// Add mencatat status baru; false bila sama dengan status terakhir atau id tidak valid.
func (l *Log) Add(id string, t time.Time, state string) bool {
	if !validID(id) || (state != On && state != Off && state != Gap) || t.Year() < 2024 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ts := t.Unix()
	if evs := l.ev[id]; len(evs) > 0 {
		last := evs[len(evs)-1]
		if last.State == state {
			return false
		}
		if ts < last.T {
			ts = last.T
		}
	}
	l.add(id, Event{ts, state})
	return true
}

// clean menyaring gap yang lebih singkat dari minGap saat membaca (data di penyimpanan tidak diubah,
// jadi gap pendek lama pun ikut diabaikan). Gap pendek dianggap tidak ada: status sebelumnya berlanjut,
// dan bila statusnya sama sesudahnya, kejadian duplikat dibuang. Gap yang masih berlangsung dihitung
// sampai now. Gap di awal riwayat (belum ada status sebelumnya) dipertahankan.
func clean(evs []Event, now int64) []Event {
	out := make([]Event, 0, len(evs))
	for i, e := range evs {
		if e.State == Gap && len(out) > 0 && out[len(out)-1].State != Gap {
			end := now
			if i+1 < len(evs) {
				end = evs[i+1].T
			}
			if end-e.T < minGap {
				continue
			}
		}
		if n := len(out); n > 0 && out[n-1].State == e.State {
			continue
		}
		out = append(out, e)
	}
	return out
}

// Last mengembalikan kejadian terakhir (waktu dan status sejak itu).
func (l *Log) Last(id string) (Event, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	evs := l.ev[id]
	if len(evs) == 0 {
		return Event{}, false
	}
	return evs[len(evs)-1], true
}

// Since mengembalikan waktu mulai status ON/OFF yang sedang berlangsung. Selang "tidak terdengar"
// (gateway mati/restart, perangkat sempat hilang) yang dibatasi status sama di kedua sisinya tidak
// memutus hitungan: ON, gap, ON tetap dihitung sejak ON pertama. Bila statusnya berbeda sesudah
// selang, hitungan mulai dari waktu status baru itu pertama kali terlihat (waktu gateway hidup lagi).
// ok=false bila belum ada riwayat atau statusnya sedang gap.
func (l *Log) Since(id string) (int64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	evs := clean(l.ev[id], l.now().Unix())
	i := len(evs) - 1
	if i < 0 || evs[i].State == Gap {
		return 0, false
	}
	cur := evs[i].State
	for i >= 2 && evs[i-1].State == Gap && evs[i-2].State == cur {
		i -= 2
	}
	return evs[i].T, true
}

// Touch menandai dashboard masih hidup (dipakai Open untuk menandai selang saat dashboard mati).
func (l *Log) Touch() {
	if err := l.store.SetAlive(l.now().Unix()); err != nil {
		log.Printf("onoff: gagal menulis penanda hidup: %v", err)
	}
}

// History menyusun segmen untuk `hours` jam terakhir (1..800).
func (l *Log) History(id string, hours int) (Hist, bool) {
	if hours < 1 {
		hours = 1
	}
	if hours > 800 {
		hours = 800
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	raw, ok := l.ev[id]
	now := l.now()
	evs := clean(raw, now.Unix())
	h := Hist{Now: now.Unix(), From: now.Add(-time.Duration(hours) * time.Hour).Unix(), Segs: []Seg{}, Events: []HEvent{}}
	if !ok {
		return h, false
	}
	for i, e := range evs {
		end := h.Now
		if i+1 < len(evs) {
			end = evs[i+1].T
		}
		if end <= h.From {
			continue
		}
		start := e.T
		if start < h.From {
			start = h.From
		}
		if end > start {
			h.Segs = append(h.Segs, Seg{start, end, e.State})
			d := end - start
			switch e.State {
			case On:
				h.Stats.OnSec += d
			case Off:
				h.Stats.OffSec += d
			default:
				h.Stats.GapSec += d
			}
		}
		if e.T >= h.From {
			h.Events = append(h.Events, HEvent{e.T, e.State, l.sourceOf(id, evs, i)})
			if i > 0 && (e.State == On || e.State == Off) && (evs[i-1].State == On || evs[i-1].State == Off) {
				h.Stats.Toggles++
			}
		}
	}
	return h, true
}

// Seconds menghitung lama ON, OFF, dan tidak terdengar (gap) pada rentang [from, to] (unix detik).
// Waktu sebelum kejadian pertama (riwayat belum ada) tidak dihitung ke mana pun.
func (l *Log) Seconds(id string, from, to int64) (on, off, gap int64, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	raw, ok := l.ev[id]
	if !ok || to <= from {
		return 0, 0, 0, ok
	}
	evs := clean(raw, l.now().Unix())
	for i, e := range evs {
		end := to
		if i+1 < len(evs) && evs[i+1].T < to {
			end = evs[i+1].T
		}
		start := e.T
		if start < from {
			start = from
		}
		if end <= start {
			continue
		}
		switch d := end - start; e.State {
		case On:
			on += d
		case Off:
			off += d
		default:
			gap += d
		}
	}
	return on, off, gap, true
}
