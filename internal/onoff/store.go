package onoff

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Store adalah tempat riwayat disimpan: file per perangkat (bawaan) atau SQLite.
type Store interface {
	// Load mengembalikan kejadian per perangkat sejak `cut` (unix detik), ditambah satu kejadian
	// sebelum batas sebagai status awal. Data yang lebih lama dibuang dari penyimpanan.
	Load(cut int64) (map[string][]Event, error)
	Append(id string, e Event) error
	// Replace mengganti seluruh riwayat satu perangkat (setelah dipangkas).
	Replace(id string, evs []Event) error
	Alive() (int64, bool)
	SetAlive(t int64) error
}

/* ---------------- file (satu file "<id>.log" per perangkat, baris "unix,state") ---------------- */

type FileStore struct{ dir string }

func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileStore{dir: dir}, nil
}

func (f *FileStore) path(id string) string { return filepath.Join(f.dir, id+".log") }

func (f *FileStore) Load(cut int64) (map[string][]Event, error) {
	out := map[string][]Event{}
	files, _ := filepath.Glob(filepath.Join(f.dir, "*.log"))
	for _, fn := range files {
		id := strings.TrimSuffix(filepath.Base(fn), ".log")
		if !validID(id) {
			continue
		}
		evs, pruned := loadFile(fn, cut)
		out[id] = evs
		if pruned {
			f.Replace(id, evs)
		}
	}
	return out, nil
}

func loadFile(path string, cut int64) ([]Event, bool) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer fh.Close()
	var evs []Event
	pruned := false
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		a, b, ok := strings.Cut(strings.TrimSpace(sc.Text()), ",")
		if !ok || (b != On && b != Off && b != Gap) {
			continue
		}
		t, err := strconv.ParseInt(a, 10, 64)
		if err != nil {
			continue
		}
		if n := len(evs); n > 0 && (t < evs[n-1].T || evs[n-1].State == b) {
			continue
		}
		evs = append(evs, Event{t, b})
	}
	// buang yang kedaluwarsa, tetapi sisakan satu kejadian sebelum batas sebagai status awal
	i := sort.Search(len(evs), func(i int) bool { return evs[i].T >= cut })
	if i > 1 {
		evs = evs[i-1:]
		pruned = true
	}
	return evs, pruned
}

func (f *FileStore) Append(id string, e Event) error {
	fh, err := os.OpenFile(f.path(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(fh, "%d,%s\n", e.T, e.State); err != nil {
		fh.Close()
		return err
	}
	return fh.Close()
}

func (f *FileStore) Replace(id string, evs []Event) error {
	var sb strings.Builder
	for _, e := range evs {
		fmt.Fprintf(&sb, "%d,%s\n", e.T, e.State)
	}
	tmp := f.path(id) + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, f.path(id))
}

func (f *FileStore) Alive() (int64, bool) {
	b, err := os.ReadFile(filepath.Join(f.dir, "alive"))
	if err != nil {
		return 0, false
	}
	at, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return at, err == nil && at > 0
}

func (f *FileStore) SetAlive(t int64) error {
	return os.WriteFile(filepath.Join(f.dir, "alive"), []byte(strconv.FormatInt(t, 10)), 0o644)
}
