package sqlstore

import (
	"owrt-dashboard/internal/onoff"
	"owrt-dashboard/internal/powerlog"
)

// Penggabungan data lama (CSV) ke SQLite yang sudah berisi data: hanya mengisi RIWAYAT YANG LEBIH TUA
// dari baris paling awal yang sudah ada di SQLite untuk perangkat itu. Tidak ada yang dihapus atau
// ditimpa, jadi aman diulang (hasil sama) dan data baru di SQLite tidak tersentuh.

const (
	qOnoffMin = `SELECT COALESCE(MIN(t), 0) FROM onoff_events WHERE id = ?`
	qMinMin   = `SELECT COALESCE(MIN(t), 0) FROM pw_min WHERE dev = ?`
	qEvMin    = `SELECT COALESCE(MIN(t), 0) FROM pw_events WHERE dev = ?`
	qRawMin   = `SELECT COALESCE(MIN(t), 0) FROM pw_raw WHERE dev = ?`
)

func (d *DB) minOf(q, id string) (float64, error) {
	var v float64
	err := d.db.QueryRow(q, id).Scan(&v)
	return v, err
}

// MergeOnOff menggabungkan riwayat ON/OFF dari fs; mengembalikan jumlah kejadian yang masuk per perangkat.
// dry=true hanya menghitung.
func (d *DB) MergeOnOff(fs *onoff.FileStore, dry bool) (map[string]int, error) {
	all, err := fs.Load(0)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	st := d.OnOff()
	for id, evs := range all {
		first, err := d.minOf(qOnoffMin, id)
		if err != nil {
			return out, err
		}
		var add []onoff.Event
		for _, e := range evs {
			if first == 0 || float64(e.T) < first {
				add = append(add, e)
			}
		}
		out[id] = len(add)
		if dry {
			continue
		}
		for _, e := range add {
			if err := st.Append(id, e); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

// PowerMerge = jumlah baris yang masuk (atau akan masuk).
type PowerMerge struct{ Events, Minutes, Raw int }

// MergePower menggabungkan log daya satu perangkat dari fs.
func (d *DB) MergePower(dev string, fs *powerlog.FileStore, dry bool) (PowerMerge, error) {
	var r PowerMerge
	st := d.Power(dev).(*powerStore)

	evFirst, err := d.minOf(qEvMin, dev)
	if err != nil {
		return r, err
	}
	evs, _ := fs.LoadEvents(0)
	var evAdd []powerlog.Event
	for _, e := range evs {
		if evFirst == 0 || float64(e.T) < evFirst {
			evAdd = append(evAdd, e)
		}
	}
	r.Events = len(evAdd)

	minFirst, err := d.minOf(qMinMin, dev)
	if err != nil {
		return r, err
	}
	mins, _ := fs.LoadMinutes(0)
	var minAdd []powerlog.MinRow
	for _, m := range mins {
		if minFirst == 0 || float64(m.T) < minFirst {
			minAdd = append(minAdd, m)
		}
	}
	r.Minutes = len(minAdd)

	rawFirst, err := d.minOf(qRawMin, dev)
	if err != nil {
		return r, err
	}
	var rawAdd []powerlog.RawRow
	for _, w := range fs.LoadRaw(0) {
		if rawFirst == 0 || w.T < rawFirst {
			rawAdd = append(rawAdd, w)
		}
	}
	r.Raw = len(rawAdd)
	if dry {
		return r, nil
	}

	if err := st.AppendEvents(evAdd); err != nil {
		return r, err
	}
	if err := st.AppendMinutes(minAdd); err != nil {
		return r, err
	}
	for i := 0; i < len(rawAdd); i += 5000 {
		j := i + 5000
		if j > len(rawAdd) {
			j = len(rawAdd)
		}
		if err := st.AppendRaw(rawAdd[i:j]); err != nil {
			return r, err
		}
	}
	return r, nil
}
