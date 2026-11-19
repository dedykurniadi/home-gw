package onoff

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

// Sumber perubahan ON/OFF. Perintah dari dashboard/app/scene dicatat terpisah (file teks kecil) dan
// digabung saat riwayat dibaca, jadi skema penyimpanan ON/OFF (file/SQLite) tidak berubah.
// Perubahan ON/OFF tanpa perintah terdekat dianggap dari perangkat (tombol fisik/eWeLink/lainnya).
const (
	SrcDevice = "Perangkat"
	srcWindow = 90 // detik: perintah dianggap penyebab perubahan yang terlihat setelahnya
	srcKeep   = 400 * 24 * 3600
	srcMax    = 3000 // per perangkat
)

type cmdRec struct {
	T   int64
	Src string
}

// HEvent adalah kejadian pada riwayat yang dikirim ke klien, dengan sumbernya.
type HEvent struct {
	T     int64  `json:"t"`
	State string `json:"state"`
	Src   string `json:"src,omitempty"` // kosong = tidak diketahui (sebelum pencatatan sumber, atau setelah selang)
}

type srcLog struct {
	path  string
	start int64 // awal pencatatan sumber; kejadian sebelumnya tidak diberi sumber
	cmds  map[string][]cmdRec
}

// SetSrcFile mengaktifkan pencatatan sumber ke file path (dibuat bila belum ada).
func (l *Log) SetSrcFile(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := &srcLog{path: path, cmds: map[string][]cmdRec{}}
	cut := l.now().Unix() - srcKeep
	if fh, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			f := strings.SplitN(sc.Text(), "\t", 3)
			if len(f) < 3 {
				continue
			}
			t, err := strconv.ParseInt(f[0], 10, 64)
			if err != nil {
				continue
			}
			if f[1] == "#" {
				s.start = t
				continue
			}
			if t >= cut && validID(f[1]) {
				s.cmds[f[1]] = append(s.cmds[f[1]], cmdRec{t, f[2]})
			}
		}
		fh.Close()
	}
	if s.start == 0 {
		s.start = l.now().Unix()
		if fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintf(fh, "%d\t#\tstart\n", s.start)
			fh.Close()
		}
	}
	l.src = s
}

// NoteCmd mencatat bahwa src (mis. "Dashboard", "Scene: Pagi") baru saja memerintah perangkat id.
func (l *Log) NoteCmd(id, src string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.src
	if s == nil || !validID(id) || src == "" {
		return
	}
	src = strings.NewReplacer("\t", " ", "\n", " ").Replace(src)
	t := l.now().Unix()
	c := append(s.cmds[id], cmdRec{t, src})
	if len(c) > srcMax {
		c = append([]cmdRec(nil), c[len(c)-srcMax*3/4:]...)
	}
	s.cmds[id] = c
	fh, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("onoff: gagal mencatat sumber: %v", err)
		return
	}
	fmt.Fprintf(fh, "%d\t%s\t%s\n", t, id, src)
	fh.Close()
}

// sourceOf menentukan sumber kejadian ke-i pada evs (sudah dibersihkan).
func (l *Log) sourceOf(id string, evs []Event, i int) string {
	s := l.src
	e := evs[i]
	if s == nil || (e.State != On && e.State != Off) {
		return ""
	}
	if e.T < s.start {
		return ""
	}
	c := s.cmds[id]
	for j := len(c) - 1; j >= 0; j-- {
		if d := e.T - c[j].T; d >= -2 && d <= srcWindow {
			return c[j].Src
		} else if d > srcWindow {
			break
		}
	}
	// tanpa perintah: dari perangkat, kecuali status sebelumnya tidak diketahui (selang/awal riwayat)
	if i > 0 && (evs[i-1].State == On || evs[i-1].State == Off) {
		return SrcDevice
	}
	return ""
}
