package powerlog

import "time"

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

// Store adalah tempat data disimpan (satu Store per perangkat): SQLite.
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
