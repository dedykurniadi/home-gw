// Paket sqlstore menyimpan seluruh data dashboard (riwayat ON/OFF, log daya, kWh harian,
// pengaturan energi, dan log jadwal) ke satu berkas SQLite.
package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "modernc.org/sqlite" // driver murni-Go, cross-compile ke linux/arm tanpa CGO

	"owrt-dashboard/internal/energy"
	"owrt-dashboard/internal/onoff"
	"owrt-dashboard/internal/powerlog"
)

const driverName = "sqlite"

type DB struct{ db *sql.DB }

// Open membuka (dan menyiapkan) berkas SQLite di path.
// Mode jurnal DELETE + synchronous FULL dipilih sengaja: penulisan jarang dan dikelompokkan, tidak
// ada berkas -wal/-shm (mmap bersama bermasalah di sebagian filesystem overlay OpenWrt), dan data
// tetap utuh bila listrik padam.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(DELETE)&_pragma=synchronous(FULL)"
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: satu penulis; sekaligus mencegah kunci antar-koneksi
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	for _, s := range schemaStmts {
		if _, err := db.Exec(s); err != nil {
			db.Close()
			return nil, fmt.Errorf("skema: %w", err)
		}
	}
	return &DB{db}, nil
}

func (d *DB) Close() error { return d.db.Close() }

func inTx(db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

/* ---------------- riwayat ON/OFF ---------------- */

type onoffStore struct{ db *sql.DB }

// OnOff mengembalikan penyimpanan SQLite untuk paket onoff.
func (d *DB) OnOff() onoff.Store { return &onoffStore{d.db} }

func (s *onoffStore) Load(cut int64) (map[string][]onoff.Event, error) {
	rows, err := s.db.Query(qOnoffIDs)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := map[string][]onoff.Event{}
	for _, id := range ids {
		var base int64
		if err := s.db.QueryRow(qOnoffBase, id, cut).Scan(&base); err != nil {
			return nil, err
		}
		r, err := s.db.Query(qOnoffLoad, id, base)
		if err != nil {
			return nil, err
		}
		var evs []onoff.Event
		for r.Next() {
			var e onoff.Event
			if err := r.Scan(&e.T, &e.State); err != nil {
				r.Close()
				return nil, err
			}
			if n := len(evs); n > 0 && (e.T < evs[n-1].T || evs[n-1].State == e.State) {
				continue
			}
			evs = append(evs, e)
		}
		if err := r.Close(); err != nil {
			return nil, err
		}
		out[id] = evs
		if base > 0 { // buang yang kedaluwarsa; satu kejadian sebelum batas disisakan sebagai status awal
			s.db.Exec(qOnoffPrune, id, base)
		}
	}
	return out, nil
}

func (s *onoffStore) Append(id string, e onoff.Event) error {
	_, err := s.db.Exec(qOnoffAppend, id, e.T, e.State)
	return err
}

func (s *onoffStore) Replace(id string, evs []onoff.Event) error {
	return inTx(s.db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(qOnoffDelete, id); err != nil {
			return err
		}
		for _, e := range evs {
			if _, err := tx.Exec(qOnoffAppend, id, e.T, e.State); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *onoffStore) Alive() (int64, bool) {
	var v string
	if err := s.db.QueryRow(qMetaGet, "onoff_alive").Scan(&v); err != nil {
		return 0, false
	}
	at, err := strconv.ParseInt(v, 10, 64)
	return at, err == nil && at > 0
}

func (s *onoffStore) SetAlive(t int64) error {
	_, err := s.db.Exec(qMetaSet, "onoff_alive", strconv.FormatInt(t, 10))
	return err
}

func (s *onoffStore) LoadSrc(cut int64) ([]onoff.SrcRec, error) {
	rows, err := s.db.Query(qSrcLoad, cut)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []onoff.SrcRec
	for rows.Next() {
		var r onoff.SrcRec
		if err := rows.Scan(&r.T, &r.ID, &r.Src); err != nil {
			return out, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	s.db.Exec(qSrcPrune, cut)
	return out, nil
}

func (s *onoffStore) SrcStart() (int64, error) {
	var v string
	if err := s.db.QueryRow(qMetaGet, "onoff_src_start").Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return strconv.ParseInt(v, 10, 64)
}

func (s *onoffStore) AppendSrc(r onoff.SrcRec) error {
	_, err := s.db.Exec(qSrcAppend, r.ID, r.T, r.Src)
	return err
}

func (s *onoffStore) SetSrcStart(t int64) error {
	_, err := s.db.Exec(qMetaSet, "onoff_src_start", strconv.FormatInt(t, 10))
	return err
}

/* ---------------- log daya (satu Store per perangkat) ---------------- */

type powerStore struct {
	db  *sql.DB
	dev string
}

// Power mengembalikan penyimpanan SQLite untuk log daya perangkat dev.
func (d *DB) Power(dev string) powerlog.Store { return &powerStore{d.db, dev} }

func (s *powerStore) AppendRaw(rows []powerlog.RawRow) error {
	return inTx(s.db, func(tx *sql.Tx) error {
		st, err := tx.Prepare(qRawInsert)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, r := range rows {
			if _, err := st.Exec(s.dev, r.T, r.W, r.V, r.A, r.Sw, r.Src); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *powerStore) AppendMinutes(rows []powerlog.MinRow) error {
	return inTx(s.db, func(tx *sql.Tx) error {
		st, err := tx.Prepare(qMinUpsert)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, r := range rows {
			if _, err := st.Exec(s.dev, r.T, r.Avg, r.Min, r.Max, r.V, r.A, r.Cov); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *powerStore) AppendEvents(rows []powerlog.Event) error {
	return inTx(s.db, func(tx *sql.Tx) error {
		st, err := tx.Prepare(qEvUpsert)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, e := range rows {
			if _, err := st.Exec(s.dev, e.T, e.State, e.W, e.Prev, e.Dur, e.KWh); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *powerStore) LoadEvents(since int64) ([]powerlog.Event, error) {
	r, err := s.db.Query(qEvLoad, s.dev, since)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var out []powerlog.Event
	for r.Next() {
		var e powerlog.Event
		if err := r.Scan(&e.T, &e.State, &e.W, &e.Prev, &e.Dur, &e.KWh); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, r.Err()
}

func (s *powerStore) LoadMinutes(since int64) ([]powerlog.MinRow, error) {
	r, err := s.db.Query(qMinLoad, s.dev, since)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var out []powerlog.MinRow
	for r.Next() {
		var m powerlog.MinRow
		if err := r.Scan(&m.T, &m.Avg, &m.Min, &m.Max, &m.V, &m.A, &m.Cov); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, r.Err()
}

func (s *powerStore) LastRaw() (powerlog.RawRow, bool, error) {
	var r powerlog.RawRow
	err := s.db.QueryRow(qRawLast, s.dev).Scan(&r.T, &r.W, &r.V, &r.A, &r.Sw, &r.Src)
	if errors.Is(err, sql.ErrNoRows) {
		return powerlog.RawRow{}, false, nil
	}
	if err != nil {
		return powerlog.RawRow{}, false, err
	}
	return r, true, nil
}

func (s *powerStore) Prune(rawBefore, minuteBefore time.Time) error {
	if _, err := s.db.Exec(qRawPrune, s.dev, float64(rawBefore.Unix())); err != nil {
		return err
	}
	_, err := s.db.Exec(qMinPrune, s.dev, minuteBefore.Unix())
	return err
}

/* ---------------- kWh harian terukur ---------------- */

type dailyStore struct{ db *sql.DB }

// EnergyDaily mengembalikan penyimpanan kWh harian terukur untuk paket energy.
func (d *DB) EnergyDaily() energy.DailyStore { return &dailyStore{d.db} }

func (s *dailyStore) LoadDaily() (map[string]map[string]float64, error) {
	rows, err := s.db.Query(qDailyAll)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]float64{}
	for rows.Next() {
		var id, day string
		var kwh float64
		if err := rows.Scan(&id, &day, &kwh); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]float64{}
		}
		out[id][day] = kwh
	}
	return out, rows.Err()
}

func (s *dailyStore) SaveDaily(id, day string, kwh float64) error {
	_, err := s.db.Exec(qDailySave, id, day, kwh)
	return err
}

func (s *dailyStore) PruneDaily(cut string) error {
	_, err := s.db.Exec(qDailyPrune, cut)
	return err
}
