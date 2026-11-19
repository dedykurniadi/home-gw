package onoff

import (
	"log"
	"strings"
)

// Sumber perubahan ON/OFF. Perintah dari HomeGW/app/scene dicatat terpisah di penyimpanan dan
// digabung saat riwayat dibaca, jadi skema ON/OFF tidak berubah.
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
	st    SrcStore
	start int64 // awal pencatatan sumber; kejadian sebelumnya tidak diberi sumber
	cmds  map[string][]cmdRec
}

// enableSrc mengaktifkan pencatatan sumber bila penyimpanan mendukungnya.
func (l *Log) enableSrc() {
	ss, ok := l.store.(SrcStore)
	if !ok {
		return
	}
	s := &srcLog{st: ss, cmds: map[string][]cmdRec{}}
	start, err := ss.SrcStart()
	if err != nil {
		log.Printf("onoff: gagal membaca awal pencatatan sumber: %v", err)
	}
	recs, err := ss.LoadSrc(l.now().Unix() - srcKeep)
	if err != nil {
		log.Printf("onoff: gagal memuat sumber: %v", err)
	}
	for _, r := range recs {
		if validID(r.ID) {
			s.cmds[r.ID] = append(s.cmds[r.ID], cmdRec{r.T, r.Src})
		}
	}
	s.start = start
	if s.start == 0 {
		s.start = l.now().Unix()
		if err := ss.SetSrcStart(s.start); err != nil {
			log.Printf("onoff: gagal menyimpan awal pencatatan sumber: %v", err)
		}
	}
	l.src = s
}

// NoteCmd mencatat bahwa src (mis. "HomeGW", "Scene: Pagi") baru saja memerintah perangkat id.
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
	if err := s.st.AppendSrc(SrcRec{T: t, ID: id, Src: src}); err != nil {
		log.Printf("onoff: gagal mencatat sumber: %v", err)
	}
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
