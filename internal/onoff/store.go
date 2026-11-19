package onoff

// SrcRec = satu perintah yang dicatat sebagai penyebab perubahan ON/OFF.
type SrcRec struct {
	T   int64
	ID  string
	Src string
}

// Store adalah tempat riwayat disimpan (SQLite).
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

// SrcStore (opsional, memenuhi Store) menyimpan catatan sumber perubahan ON/OFF.
// SrcStart mengembalikan awal pencatatan (0 = belum ada); SetSrcStart menyimpannya.
type SrcStore interface {
	SrcStart() (int64, error)
	SetSrcStart(t int64) error
	LoadSrc(cut int64) ([]SrcRec, error)
	AppendSrc(r SrcRec) error
}
