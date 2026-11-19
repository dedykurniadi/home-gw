// Package sched menjalankan scene otomatis (jam tetap, atau terbit/terbenam matahari)
// untuk perangkat Sonoff. Jalan di dashboard, jadi tetap aktif walau aplikasi ditutup.
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxScenes   = 50
	maxTriggers = 1 // satu pemicu per scene (scene lama dengan banyak pemicu dipecah saat dimuat)
	maxActions  = 16
	maxRuns     = 50 // cadangan di memori bila tidak ada penyimpanan DB
	keepRuns    = 90 * 24 * time.Hour
	listRuns    = 100
	retryUntil  = 10 * time.Minute // jadwal yang terlewat lebih lama dari ini dilewati (mis. router baru menyala)
	tickEvery   = 15 * time.Second
)

var ErrInvalid = errors.New("scene tidak valid")

// Trigger adalah satu pemicu: jam tetap, atau terbit/terbenam matahari dengan selisih menit.
type Trigger struct {
	Type   string `json:"type"`             // time | sunrise | sunset
	At     string `json:"at,omitempty"`     // "HH:MM" bila type=time
	Offset int    `json:"offset,omitempty"` // menit untuk sunrise/sunset (-180..180)
}

// Action menyalakan atau mematikan satu perangkat.
type Action struct {
	Device string `json:"device"`
	Action string `json:"action"` // on | off
}

// Scene: bila salah satu pemicu tiba (pada hari yang dipilih), semua aksi dijalankan.
type Scene struct {
	ID       string    `json:"id"`
	Name     string    `json:"name,omitempty"`
	Triggers []Trigger `json:"triggers"`
	Actions  []Action  `json:"actions"`
	Days     []int     `json:"days"` // 0=Min .. 6=Sab; kosong = setiap hari
	Enabled  bool      `json:"enabled"`
	Last     []int64   `json:"last,omitempty"` // per pemicu: waktu terjadwal (unix) yang terakhir sudah diproses
}

// Fail = satu perangkat yang gagal dijalankan pada suatu eksekusi.
type Fail struct {
	Device string `json:"device"`
	Name   string `json:"name"`
	Err    string `json:"err"`
}

// Run adalah catatan eksekusi satu scene: berhasil semua, atau gagal (beserta perangkat yang gagal).
type Run struct {
	T     int64  `json:"t"`              // waktu selesai diproses
	Fire  int64  `json:"fire"`           // waktu terjadwal
	Scene string `json:"scene"`          // id scene
	Name  string `json:"name,omitempty"` // nama scene saat ini (diisi saat dibaca)
	Label string `json:"label"`
	OK    bool   `json:"ok"`
	Err   string `json:"err,omitempty"`
	Fails []Fail `json:"fails,omitempty"`
}

// LastRun = waktu terakhir scene berhasil dan terakhir gagal (unix, 0 = belum pernah).
type LastRun struct {
	OK   int64 `json:"last_ok"`
	Fail int64 `json:"last_fail"`
}

// RunStore menyimpan log eksekusi (SQLite). Tanpa itu log hanya di memori (hilang saat restart).
type RunStore interface {
	AddRun(r Run) error
	Runs(limit int) ([]Run, error) // terbaru lebih dulu
	LastRuns() (map[string]LastRun, error)
	Prune(before int64) error
}

// SceneView = Scene + label tampilan + waktu jalan berikutnya.
type SceneView struct {
	Scene
	Label    string `json:"label"`
	Next     int64  `json:"next,omitempty"` // 0 = tidak ada (nonaktif / lokasi belum diatur)
	LastOK   int64  `json:"last_ok,omitempty"`
	LastFail int64  `json:"last_fail,omitempty"`
}

type Engine struct {
	path string
	loc  *time.Location
	lat  float64
	lon  float64
	hasL bool
	set  func(id, cmd string) error
	note func(id, scene string)         // opsional: dipanggil sebelum scene memerintah perangkat (untuk catatan sumber)
	name func(id string) (string, bool) // nama perangkat; ok=false bila tidak ada
	now  func() time.Time

	mu      sync.Mutex
	scenes  []Scene
	runs    []Run
	store   RunStore
	pruned  time.Time
	pending map[string]map[int]bool // "scene|pemicu|waktu" -> indeks aksi yang sudah sukses (selama jendela retry)
	errs    map[string][]Fail
}

// legacy = format lama: satu aturan untuk satu perangkat.
type legacy struct {
	ID      string `json:"id"`
	Device  string `json:"device"`
	Action  string `json:"action"`
	Trigger string `json:"trigger"`
	At      string `json:"at"`
	Offset  int    `json:"offset"`
	Days    []int  `json:"days"`
	Enabled bool   `json:"enabled"`
	Last    int64  `json:"last"`
}

// splitTriggers memecah scene berpemicu banyak menjadi beberapa scene berpemicu tunggal
// (scene pertama mempertahankan ID); true bila ada yang dipecah.
func splitTriggers(in []Scene) ([]Scene, bool) {
	out := make([]Scene, 0, len(in))
	split := false
	for _, sc := range in {
		if len(sc.Triggers) <= 1 {
			out = append(out, sc)
			continue
		}
		split = true
		for i, t := range sc.Triggers {
			n := Scene{ID: sc.ID, Name: sc.Name, Triggers: []Trigger{t}, Days: sc.Days, Enabled: sc.Enabled,
				Actions: append([]Action(nil), sc.Actions...), Last: []int64{sc.last(i)}}
			if i > 0 {
				n.ID = newID() + strconv.Itoa(i)
				if sc.Name != "" {
					n.Name = sc.Name + " " + strconv.Itoa(i+1)
				}
			}
			out = append(out, n)
		}
	}
	return out, split
}

func decodeScenes(b []byte) ([]Scene, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := make([]Scene, 0, len(raw))
	for _, r := range raw {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(r, &probe); err != nil {
			return nil, err
		}
		if _, ok := probe["triggers"]; !ok {
			var l legacy
			if err := json.Unmarshal(r, &l); err != nil {
				return nil, err
			}
			sc := Scene{ID: l.ID, Days: l.Days, Enabled: l.Enabled,
				Triggers: []Trigger{{Type: l.Trigger, At: l.At, Offset: l.Offset}},
				Actions:  []Action{{Device: l.Device, Action: l.Action}}}
			if l.Last != 0 {
				sc.Last = []int64{l.Last}
			}
			out = append(out, sc)
			continue
		}
		var sc Scene
		if err := json.Unmarshal(r, &sc); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, nil
}

// New memuat scene dari path (file belum ada = kosong; format jadwal lama dimigrasi otomatis).
// lat/lon nil = pemicu matahari tidak aktif.
func New(path string, loc *time.Location, lat, lon *float64, set func(id, cmd string) error, name func(id string) (string, bool)) (*Engine, error) {
	e := &Engine{path: path, loc: loc, set: set, name: name, now: time.Now,
		pending: map[string]map[int]bool{}, errs: map[string][]Fail{}}
	if lat != nil && lon != nil {
		e.lat, e.lon, e.hasL = *lat, *lon, true
	}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return e, err
	default:
		sc, err := decodeScenes(b)
		if err != nil {
			return e, fmt.Errorf("%s: %w", path, err)
		}
		var split bool
		e.scenes, split = splitTriggers(sc)
		if split { // simpan hasil pemecahan sekali, supaya ID scene baru tetap sama sesudahnya
			if err := e.saveLocked(); err != nil {
				return e, err
			}
		}
	}
	return e, nil
}

func (e *Engine) saveLocked() error {
	b, _ := json.MarshalIndent(e.scenes, "", " ")
	if err := os.MkdirAll(filepath.Dir(e.path), 0o755); err != nil {
		return err
	}
	tmp := e.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, e.path)
}

func (e *Engine) validate(s *Scene) error {
	s.Name = strings.TrimSpace(s.Name)
	if len([]rune(s.Name)) > 40 {
		return fmt.Errorf("%w: nama maksimal 40 huruf", ErrInvalid)
	}
	if len(s.Triggers) != maxTriggers {
		return fmt.Errorf("%w: butuh tepat satu pemicu", ErrInvalid)
	}
	for i := range s.Triggers {
		t := &s.Triggers[i]
		switch t.Type {
		case "time":
			if _, _, ok := parseHM(t.At); !ok {
				return fmt.Errorf("%w: jam harus HH:MM", ErrInvalid)
			}
			t.Offset = 0
		case "sunrise", "sunset":
			if !e.hasL {
				return fmt.Errorf("%w: lokasi (lat/lon) belum diatur di config", ErrInvalid)
			}
			if t.Offset < -180 || t.Offset > 180 {
				return fmt.Errorf("%w: selisih maksimal 180 menit", ErrInvalid)
			}
			t.At = ""
		default:
			return fmt.Errorf("%w: pemicu harus time, sunrise, atau sunset", ErrInvalid)
		}
	}
	if len(s.Actions) == 0 || len(s.Actions) > maxActions {
		return fmt.Errorf("%w: butuh 1-%d aksi", ErrInvalid, maxActions)
	}
	seenDev := map[string]bool{}
	for _, a := range s.Actions {
		if _, ok := e.name(a.Device); !ok {
			return fmt.Errorf("%w: perangkat %q tidak ada", ErrInvalid, a.Device)
		}
		if a.Action != "on" && a.Action != "off" {
			return fmt.Errorf("%w: aksi harus on atau off", ErrInvalid)
		}
		if seenDev[a.Device] {
			return fmt.Errorf("%w: perangkat dipakai dua kali", ErrInvalid)
		}
		seenDev[a.Device] = true
	}
	seen := map[int]bool{}
	days := []int{}
	for _, d := range s.Days {
		if d < 0 || d > 6 {
			return fmt.Errorf("%w: hari 0-6", ErrInvalid)
		}
		if !seen[d] {
			seen[d] = true
			days = append(days, d)
		}
	}
	sort.Ints(days)
	if len(days) == 7 {
		days = []int{}
	}
	s.Days = days
	return nil
}

func parseHM(s string) (h, m int, ok bool) {
	p := strings.Split(s, ":")
	if len(p) != 2 || len(p[0]) != 2 || len(p[1]) != 2 {
		return
	}
	h, e1 := strconv.Atoi(p[0])
	m, e2 := strconv.Atoi(p[1])
	if e1 != nil || e2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

// fireTime = waktu pemicu t pada tanggal kalender day.
func (e *Engine) fireTime(t Trigger, day time.Time) (time.Time, bool) {
	y, mo, d := day.Date()
	switch t.Type {
	case "time":
		h, m, ok := parseHM(t.At)
		if !ok {
			return time.Time{}, false
		}
		return time.Date(y, mo, d, h, m, 0, 0, e.loc), true
	case "sunrise", "sunset":
		if !e.hasL {
			return time.Time{}, false
		}
		rise, set, ok := sunTimes(time.Date(y, mo, d, 12, 0, 0, 0, e.loc), e.lat, e.lon)
		if !ok {
			return time.Time{}, false
		}
		at := rise
		if t.Type == "sunset" {
			at = set
		}
		return at.Add(time.Duration(t.Offset) * time.Minute), true
	}
	return time.Time{}, false
}

func dayAllowed(s Scene, day time.Time) bool {
	if len(s.Days) == 0 {
		return true
	}
	wd := int(day.Weekday())
	for _, d := range s.Days {
		if d == wd {
			return true
		}
	}
	return false
}

// next = waktu pemicu terdekat setelah `after` di antara semua pemicu (sampai 8 hari ke depan).
func (e *Engine) next(s Scene, after time.Time) int64 {
	if !s.Enabled {
		return 0
	}
	base := after.In(e.loc)
	var best int64
	for i := 0; i <= 8; i++ {
		day := base.AddDate(0, 0, i)
		if !dayAllowed(s, day) {
			continue
		}
		for _, t := range s.Triggers {
			if at, ok := e.fireTime(t, day); ok && at.After(after) {
				if u := at.Unix(); best == 0 || u < best {
					best = u
				}
			}
		}
		if best != 0 {
			return best // hari paling awal yang punya pemicu sudah cukup
		}
	}
	return best
}

func (e *Engine) label(s Scene) string {
	if s.Name != "" {
		return s.Name
	}
	var ns []string
	for _, a := range s.Actions {
		n, _ := e.name(a.Device)
		if n == "" {
			n = a.Device
		}
		ns = append(ns, n)
	}
	return strings.Join(ns, ", ")
}

// addRun mencatat eksekusi (dipanggil dengan e.mu terpegang): ke DB bila ada, dan ke memori.
func (e *Engine) addRun(r Run) {
	e.runs = append(e.runs, r)
	if len(e.runs) > maxRuns {
		e.runs = e.runs[len(e.runs)-maxRuns:]
	}
	if e.store != nil {
		if err := e.store.AddRun(r); err != nil {
			log.Printf("scene: gagal menyimpan log: %v", err)
		}
	}
}

// SetStore memakai st untuk log eksekusi dan memangkas log lebih tua dari 90 hari.
// SetNote mendaftarkan pencatat sumber perintah (nama scene yang memerintah perangkat).
func (e *Engine) SetNote(fn func(id, scene string)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.note = fn
}

func (e *Engine) SetStore(st RunStore) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.store = st
	e.prune()
}

func (e *Engine) prune() {
	if e.store == nil {
		return
	}
	e.pruned = e.now()
	if err := e.store.Prune(e.pruned.Add(-keepRuns).Unix()); err != nil {
		log.Printf("scene: gagal memangkas log: %v", err)
	}
}

func failText(f []Fail) string {
	parts := make([]string, len(f))
	for i, x := range f {
		parts[i] = x.Name + ": " + x.Err
	}
	return strings.Join(parts, "; ")
}

func (s *Scene) last(i int) int64 {
	if i < len(s.Last) {
		return s.Last[i]
	}
	return 0
}

func (s *Scene) setLast(i int, v int64) {
	for len(s.Last) < len(s.Triggers) {
		s.Last = append(s.Last, 0)
	}
	s.Last[i] = v
}

// Tick memproses pemicu yang jatuh tempo pada waktu now.
func (e *Engine) Tick(now time.Time) {
	e.mu.Lock()
	if e.store != nil && now.Sub(e.pruned) > 24*time.Hour {
		e.prune()
	}
	type job struct {
		scene Scene
		trig  int
		fire  time.Time
	}
	var jobs []job
	skipped := false
	today := now.In(e.loc)
	for si := range e.scenes {
		sc := e.scenes[si]
		if !sc.Enabled || !dayAllowed(sc, today) {
			continue
		}
		for ti, t := range sc.Triggers {
			fire, ok := e.fireTime(t, today)
			if !ok || now.Before(fire) || sc.last(ti) >= fire.Unix() {
				continue
			}
			if now.Sub(fire) > retryUntil { // terlalu lama terlewat (mis. router mati): lewati, jangan menyalakan mendadak
				key := sc.ID + "|" + strconv.Itoa(ti) + "|" + strconv.FormatInt(fire.Unix(), 10)
				r := Run{T: now.Unix(), Fire: fire.Unix(), Scene: sc.ID, Label: e.label(sc), Err: "dilewati (terlambat)"}
				if f, had := e.errs[key]; had { // sempat dicoba tapi gagal terus sampai jendela habis
					r.Err, r.Fails = failText(f), f
				}
				delete(e.pending, key)
				delete(e.errs, key)
				e.scenes[si].setLast(ti, fire.Unix())
				e.addRun(r)
				skipped = true
				continue
			}
			jobs = append(jobs, job{sc, ti, fire})
		}
	}
	if skipped {
		e.saveLocked()
	}
	e.mu.Unlock()

	for _, j := range jobs {
		key := j.scene.ID + "|" + strconv.Itoa(j.trig) + "|" + strconv.FormatInt(j.fire.Unix(), 10)
		e.mu.Lock()
		done := e.pending[key]
		if done == nil {
			done = map[int]bool{}
			e.pending[key] = done
		}
		e.mu.Unlock()

		var failed []Fail
		for ai, a := range j.scene.Actions {
			if done[ai] {
				continue
			}
			if e.note != nil {
				e.note(a.Device, j.scene.Name)
			}
			if err := e.set(a.Device, a.Action); err != nil { // di luar kunci: bisa lama
				n, _ := e.name(a.Device)
				if n == "" {
					n = a.Device
				}
				failed = append(failed, Fail{Device: a.Device, Name: n, Err: err.Error()})
			} else {
				done[ai] = true
			}
		}

		e.mu.Lock()
		idx := -1
		for i := range e.scenes {
			if e.scenes[i].ID == j.scene.ID {
				idx = i
			}
		}
		finish := func(ok bool, fails []Fail) {
			delete(e.pending, key)
			delete(e.errs, key)
			if idx >= 0 {
				e.scenes[idx].setLast(j.trig, j.fire.Unix())
				e.saveLocked()
			}
			e.addRun(Run{T: now.Unix(), Fire: j.fire.Unix(), Scene: j.scene.ID, Label: e.label(j.scene), OK: ok,
				Err: failText(fails), Fails: fails})
		}
		switch {
		case idx < 0:
			delete(e.pending, key) // scene dihapus selama berjalan
		case len(failed) == 0:
			finish(true, nil)
		default:
			e.errs[key] = failed // dicoba lagi pada tick berikutnya; dicatat gagal bila jendela habis
		}
		e.mu.Unlock()
	}
}

// Run menjalankan penjadwal sampai ctx selesai. Pemicu yang terlewat <10 menit (mis. setelah restart) tetap dijalankan.
func (e *Engine) Run(ctx context.Context) {
	tk := time.NewTicker(tickEvery)
	defer tk.Stop()
	e.Tick(e.now())
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			e.Tick(e.now())
		}
	}
}

func newID() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }

// Views mengembalikan semua scene beserta waktu berikutnya.
func (e *Engine) Views() []SceneView {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	last := e.lastRunsLocked()
	out := make([]SceneView, 0, len(e.scenes))
	for _, s := range e.scenes {
		if s.Days == nil {
			s.Days = []int{}
		}
		l := last[s.ID]
		out = append(out, SceneView{Scene: s, Label: e.label(s), Next: e.next(s, now), LastOK: l.OK, LastFail: l.Fail})
	}
	return out
}

// lastRunsLocked: terakhir berhasil/gagal per scene (dari DB bila ada, selain itu dari memori).
func (e *Engine) lastRunsLocked() map[string]LastRun {
	if e.store != nil {
		if m, err := e.store.LastRuns(); err == nil {
			return m
		} else {
			log.Printf("scene: gagal membaca log: %v", err)
		}
	}
	m := map[string]LastRun{}
	for _, r := range e.runs {
		l := m[r.Scene]
		if r.OK {
			l.OK = r.T
		} else {
			l.Fail = r.T
		}
		m[r.Scene] = l
	}
	return m
}

// Runs mengembalikan catatan eksekusi terbaru lebih dulu (maksimal 100).
func (e *Engine) Runs() []Run {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Run
	if e.store != nil {
		if rs, err := e.store.Runs(listRuns); err == nil {
			out = rs
		} else {
			log.Printf("scene: gagal membaca log: %v", err)
		}
	}
	if out == nil {
		out = make([]Run, 0, len(e.runs))
		for i := len(e.runs) - 1; i >= 0; i-- {
			out = append(out, e.runs[i])
		}
	}
	names := map[string]string{}
	for _, s := range e.scenes {
		names[s.ID] = s.Name
	}
	for i := range out {
		out[i].Name = names[out[i].Scene] // scene yang sudah dihapus: Name kosong, klien memakai id
	}
	return out
}

// Sun = terbit/terbenam hari ini (unix); nil bila lokasi belum diatur.
func (e *Engine) Sun() map[string]int64 {
	if !e.hasL {
		return nil
	}
	rise, set, ok := sunTimes(e.now().In(e.loc), e.lat, e.lon)
	if !ok {
		return nil
	}
	return map[string]int64{"sunrise": rise.Unix(), "sunset": set.Unix()}
}

// Put membuat (ID kosong) atau mengganti scene. Pemicu yang waktunya hari ini sudah lewat saat
// disimpan dianggap sudah diproses, agar tidak langsung menyala.
func (e *Engine) Put(s Scene) (Scene, error) {
	if err := e.validate(&s); err != nil {
		return s, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	s.Last = make([]int64, len(s.Triggers))
	for i, t := range s.Triggers {
		if fire, ok := e.fireTime(t, now.In(e.loc)); ok && !now.Before(fire) {
			s.Last[i] = fire.Unix()
		}
	}
	if s.ID == "" {
		if len(e.scenes) >= maxScenes {
			return s, fmt.Errorf("%w: maksimal %d scene", ErrInvalid, maxScenes)
		}
		s.ID = newID()
		e.scenes = append(e.scenes, s)
	} else {
		found := false
		for i := range e.scenes {
			if e.scenes[i].ID == s.ID {
				e.scenes[i], found = s, true
			}
		}
		if !found {
			return s, os.ErrNotExist
		}
	}
	return s, e.saveLocked()
}

func (e *Engine) Delete(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.scenes {
		if e.scenes[i].ID == id {
			e.scenes = append(e.scenes[:i], e.scenes[i+1:]...)
			return e.saveLocked()
		}
	}
	return os.ErrNotExist
}
