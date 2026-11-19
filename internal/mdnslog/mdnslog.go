// Package mdnslog mencatat setiap paket mDNS Sonoff (dan tiap query yang dikirim dashboard) ke CSV
// harian. Tujuannya analisis: membedakan paket hilang, WiFi putus, dan reboot dari urutan seq.
//
//	mdns-YYYYMMDD.csv   t,ev,id,ip,seq,dup,viaq,sw,rssi,dec
//
// ev = p (paket dari perangkat) | q (query dikirim dashboard, kolom lain kosong).
// dup = 1 bila seq sama dengan paket sebelumnya (paket kembar dalam satu burst).
// viaq = 1 bila tiba <1,5 detik setelah query (balasan, bukan pengumuman spontan).
// dec = ok | fail (dekripsi gagal) | bad (isi bukan JSON).
package mdnslog

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const header = "t,ev,id,ip,seq,dup,viaq,sw,rssi,dec\n"

// Packet = satu paket mDNS dari perangkat.
type Packet struct {
	T    time.Time
	ID   string
	IP   string
	Seq  string
	Dup  bool
	ViaQ bool
	Sw   string
	RSSI *int
	Dec  string
}

// Log menulis CSV harian dengan buffer; aman dipakai dari banyak goroutine.
type Log struct {
	dir  string
	days int
	loc  *time.Location

	mu   sync.Mutex
	day  string
	f    *os.File
	w    *bufio.Writer
	stop chan struct{}
	done chan struct{}
}

// Open membuat folder dir dan mulai mencatat. days = lama file disimpan (<=0 → 14). loc menentukan batas hari.
func Open(dir string, days int, loc *time.Location) (*Log, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if days <= 0 {
		days = 14
	}
	if loc == nil {
		loc = time.Local
	}
	l := &Log{dir: dir, days: days, loc: loc, stop: make(chan struct{}), done: make(chan struct{})}
	go l.flusher()
	return l, nil
}

func (l *Log) flusher() {
	defer close(l.done)
	tk := time.NewTicker(5 * time.Second)
	defer tk.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-tk.C:
			l.mu.Lock()
			if l.w != nil {
				l.w.Flush()
			}
			l.mu.Unlock()
		}
	}
}

// rotate memastikan file untuk hari t terbuka. Dipanggil dengan l.mu terkunci.
func (l *Log) rotate(t time.Time) error {
	d := t.In(l.loc).Format("20060102")
	if l.f != nil && d == l.day {
		return nil
	}
	if l.f != nil {
		l.w.Flush()
		l.f.Close()
		l.f, l.w = nil, nil
	}
	path := filepath.Join(l.dir, "mdns-"+d+".csv")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 16<<10)
	if st, err := f.Stat(); err == nil && st.Size() == 0 {
		w.WriteString(header)
	}
	l.f, l.w, l.day = f, w, d
	l.prune(t)
	return nil
}

func (l *Log) prune(now time.Time) {
	files, _ := filepath.Glob(filepath.Join(l.dir, "mdns-*.csv"))
	sort.Strings(files)
	cut := now.In(l.loc).AddDate(0, 0, -l.days).Format("20060102")
	for _, p := range files {
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(p), "mdns-"), ".csv")
		if len(name) == 8 && name < cut {
			os.Remove(p)
		}
	}
}

func ts(t time.Time) string { return strconv.FormatFloat(float64(t.UnixNano())/1e9, 'f', 3, 64) }

func b2s(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// clean membuang karakter yang merusak CSV (nilai berasal dari jaringan).
func clean(s string) string {
	if strings.ContainsAny(s, ",\r\n\"") {
		return strings.Map(func(r rune) rune {
			if r == ',' || r == '\r' || r == '\n' || r == '"' {
				return '_'
			}
			return r
		}, s)
	}
	return s
}

func (l *Log) write(t time.Time, line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.rotate(t) != nil {
		return
	}
	l.w.WriteString(line)
}

// Packet mencatat satu paket dari perangkat.
func (l *Log) Packet(p Packet) {
	rssi := ""
	if p.RSSI != nil {
		rssi = strconv.Itoa(*p.RSSI)
	}
	l.write(p.T, fmt.Sprintf("%s,p,%s,%s,%s,%s,%s,%s,%s,%s\n",
		ts(p.T), clean(p.ID), clean(p.IP), clean(p.Seq), b2s(p.Dup), b2s(p.ViaQ), clean(p.Sw), rssi, clean(p.Dec)))
}

// Query mencatat saat dashboard mengirim query mDNS.
func (l *Log) Query(t time.Time) {
	l.write(t, ts(t)+",q,,,,,,,,\n")
}

// Close menulis sisa buffer dan menutup file.
func (l *Log) Close() {
	close(l.stop)
	<-l.done
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.w.Flush()
		l.f.Close()
		l.f, l.w = nil, nil
	}
}
