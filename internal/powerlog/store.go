package powerlog

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RawRow = satu update mDNS (sampel mentah).
type RawRow struct {
	T       float64 // unix detik (dengan pecahan milidetik)
	W, V, A float64
	Sw      string // status relay: on/off
	Src     string // p = spontan dari perangkat, q = balasan query
}

// MinRow = rekap satu menit (rata-rata dibobot waktu).
type MinRow struct {
	T                        int64 // awal menit (unix detik)
	Avg, Min, Max, V, A, Cov float64
}

// Store adalah tempat data disimpan (satu Store per perangkat): file CSV bawaan atau SQLite.
// Seluruh metode Append bersifat idempotent terhadap kunci waktu (menulis ulang menit/kejadian yang
// sama menimpa, bukan menggandakan), kecuali AppendRaw.
type Store interface {
	AppendRaw(rows []RawRow) error
	AppendMinutes(rows []MinRow) error
	AppendEvents(rows []Event) error
	LoadEvents(since int64) ([]Event, error)
	LoadMinutes(since int64) ([]MinRow, error)
	LastRaw() (RawRow, bool, error)
	// Prune menghapus sampel mentah sebelum rawBefore dan rekap menit sebelum minuteBefore.
	Prune(rawBefore, minuteBefore time.Time) error
}

/* ---------------- file CSV (format asli, kompatibel dengan data lama) ---------------- */

type FileStore struct{ dir string }

func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileStore{dir: dir}, nil
}

func (f *FileStore) path(name string) string { return filepath.Join(f.dir, name) }

func appendFile(path, text string) error {
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := fh.WriteString(text); err != nil {
		fh.Close()
		return err
	}
	return fh.Close()
}

func readLines(path string, fn func(f []string)) {
	fh, err := os.Open(path)
	if err != nil {
		return
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if s := strings.TrimSpace(sc.Text()); s != "" {
			fn(strings.Split(s, ","))
		}
	}
}

func pf(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }

func (f *FileStore) AppendRaw(rows []RawRow) error {
	byDay := map[string]*strings.Builder{}
	var days []string
	for _, r := range rows {
		d := time.UnixMilli(int64(r.T * 1000)).Format("20060102")
		sb := byDay[d]
		if sb == nil {
			sb = &strings.Builder{}
			byDay[d] = sb
			days = append(days, d)
		}
		fmt.Fprintf(sb, "%.3f,%.2f,%.1f,%.2f,%s,%s\n", r.T, r.W, r.V, r.A, r.Sw, r.Src)
	}
	for _, d := range days {
		if err := appendFile(f.path("raw-"+d+".csv"), byDay[d].String()); err != nil {
			return err
		}
	}
	return nil
}

func (f *FileStore) AppendMinutes(rows []MinRow) error {
	byMonth := map[string]*strings.Builder{}
	var months []string
	for _, b := range rows {
		m := time.Unix(b.T, 0).Format("200601")
		sb := byMonth[m]
		if sb == nil {
			sb = &strings.Builder{}
			byMonth[m] = sb
			months = append(months, m)
		}
		fmt.Fprintf(sb, "%d,%.1f,%.1f,%.1f,%.1f,%.2f,%.0f\n", b.T, b.Avg, b.Min, b.Max, b.V, b.A, b.Cov)
	}
	for _, m := range months {
		if err := appendFile(f.path("min-"+m+".csv"), byMonth[m].String()); err != nil {
			return err
		}
	}
	return nil
}

func (f *FileStore) AppendEvents(rows []Event) error {
	var sb strings.Builder
	for _, e := range rows {
		fmt.Fprintf(&sb, "%d,%s,%.1f,%s,%d,%.4f\n", e.T, e.State, e.W, e.Prev, e.Dur, e.KWh)
	}
	return appendFile(f.path("events.csv"), sb.String())
}

func (f *FileStore) LoadEvents(since int64) ([]Event, error) {
	var out []Event
	readLines(f.path("events.csv"), func(c []string) {
		if len(c) < 6 {
			return
		}
		t, err := strconv.ParseInt(c[0], 10, 64)
		if err != nil || t < since {
			return
		}
		dur, _ := strconv.ParseInt(c[4], 10, 64)
		out = append(out, Event{T: t, State: c[1], W: pf(c[2]), Prev: c[3], Dur: dur, KWh: pf(c[5])})
	})
	return out, nil
}

func (f *FileStore) LoadMinutes(since int64) ([]MinRow, error) {
	files, _ := filepath.Glob(f.path("min-??????.csv"))
	sort.Strings(files)
	from := ""
	if since > 0 {
		from = time.Unix(since, 0).Format("200601")
	}
	var out []MinRow
	for _, fn := range files {
		if mo := filepath.Base(fn)[4:10]; mo < from {
			continue
		}
		readLines(fn, func(c []string) {
			if len(c) < 7 {
				return
			}
			t, err := strconv.ParseInt(c[0], 10, 64)
			if err != nil || t < since {
				return
			}
			out = append(out, MinRow{T: t, Avg: pf(c[1]), Min: pf(c[2]), Max: pf(c[3]), V: pf(c[4]), A: pf(c[5]), Cov: pf(c[6])})
		})
	}
	return out, nil
}

func parseRaw(c []string) (RawRow, bool) {
	if len(c) < 5 {
		return RawRow{}, false
	}
	r := RawRow{T: pf(c[0]), W: pf(c[1]), V: pf(c[2]), A: pf(c[3]), Sw: c[4]}
	if len(c) >= 6 {
		r.Src = c[5]
	}
	return r, true
}

func (f *FileStore) rawFiles() []string {
	ents, _ := os.ReadDir(f.dir)
	var raws []string
	for _, e := range ents {
		if n := e.Name(); strings.HasPrefix(n, "raw-") && strings.HasSuffix(n, ".csv") {
			raws = append(raws, n)
		}
	}
	sort.Strings(raws)
	return raws
}

func (f *FileStore) LastRaw() (RawRow, bool, error) {
	raws := f.rawFiles()
	for i := len(raws) - 1; i >= 0 && i >= len(raws)-2; i-- {
		var last RawRow
		found := false
		readLines(f.path(raws[i]), func(c []string) {
			if r, ok := parseRaw(c); ok {
				last, found = r, true
			}
		})
		if found {
			return last, true, nil
		}
	}
	return RawRow{}, false, nil
}

// LoadRaw membaca semua sampel mentah sejak `since` (unix detik); dipakai untuk migrasi ke SQLite.
func (f *FileStore) LoadRaw(since float64) []RawRow {
	var out []RawRow
	for _, n := range f.rawFiles() {
		readLines(f.path(n), func(c []string) {
			if r, ok := parseRaw(c); ok && r.T >= since {
				out = append(out, r)
			}
		})
	}
	return out
}

func (f *FileStore) Prune(rawBefore, minuteBefore time.Time) error {
	rawCut, minCut := rawBefore.Format("20060102"), minuteBefore.Format("200601")
	ents, err := os.ReadDir(f.dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		n := e.Name()
		switch {
		case strings.HasPrefix(n, "raw-") && strings.HasSuffix(n, ".csv") && len(n) == len("raw-20060102.csv"):
			if n[4:12] < rawCut {
				os.Remove(f.path(n))
			}
		case strings.HasPrefix(n, "min-") && strings.HasSuffix(n, ".csv") && len(n) == len("min-200601.csv"):
			if n[4:10] < minCut {
				os.Remove(f.path(n))
			}
		}
	}
	return nil
}
