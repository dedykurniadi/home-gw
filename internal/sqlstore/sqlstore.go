// Paket sqlstore menyimpan riwayat ON/OFF dan log daya ke satu berkas SQLite.
//
// Paket ini hanya memakai database/sql. Driver SQLite (modernc.org/sqlite, tanpa CGO) baru
// ikut terpasang bila dibangun dengan -tags sqlite (lihat driver_sqlite.go dan README);
// tanpa tag itu Open mengembalikan ErrNoDriver dan dashboard memakai file CSV seperti biasa.
package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"owrt-dashboard/internal/onoff"
	"owrt-dashboard/internal/powerlog"
)

// ErrNoDriver: binary dibangun tanpa driver SQLite.
var ErrNoDriver = errors.New("binary ini dibangun tanpa SQLite (bangun dengan -tags sqlite, lihat README)")

const driverName = "sqlite"

type DB struct{ db *sql.DB }

func hasDriver() bool {
	for _, d := range sql.Drivers() {
		if d == driverName {
			return true
		}
	}
	return false
}

// Open membuka (dan menyiapkan) berkas SQLite di path.
// Mode jurnal DELETE + synchronous FULL dipilih sengaja: penulisan jarang dan dikelompokkan, tidak
// ada berkas -wal/-shm (mmap bersama bermasalah di sebagian filesystem overlay OpenWrt), dan data
// tetap utuh bila listrik padam.
func Open(path string) (*DB, error) {
	if !hasDriver() {
		return nil, ErrNoDriver
	}
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

func (d *DB) metaGet(k string) (string, bool) {
	var v string
	if err := d.db.QueryRow(qMetaGet, k).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

func (d *DB) metaSet(k, v string) error {
	_, err := d.db.Exec(qMetaSet, k, v)
	return err
}

// ImportOnce menjalankan fn hanya sekali per nama (impor data lama); gagal = diulang saat start berikutnya.
func (d *DB) ImportOnce(name string, fn func() error) error {
	k := "import:" + name
	if _, done := d.metaGet(k); done {
		return nil
	}
	if err := fn(); err != nil {
		return err
	}
	return d.metaSet(k, strconv.FormatInt(time.Now().Unix(), 10))
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

// MigrateOnOff memindahkan riwayat dari file lama (sekali saja; file lama dibiarkan sebagai cadangan).
func (d *DB) MigrateOnOff(fs *onoff.FileStore) error {
	return d.ImportOnce("onoff", func() error {
		all, err := fs.Load(0)
		if err != nil {
			return err
		}
		st := d.OnOff()
		for id, evs := range all {
			if err := st.Replace(id, evs); err != nil {
				return err
			}
		}
		if at, ok := fs.Alive(); ok {
			return st.SetAlive(at)
		}
		return nil
	})
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

// MigratePower memindahkan file CSV lama perangkat dev (sekali saja; file lama dibiarkan sebagai cadangan).
func (d *DB) MigratePower(dev string, fs *powerlog.FileStore) error {
	return d.ImportOnce("power:"+dev, func() error {
		st := d.Power(dev).(*powerStore)
		evs, _ := fs.LoadEvents(0)
		mins, _ := fs.LoadMinutes(0)
		raw := fs.LoadRaw(0)
		if _, err := d.db.Exec(qRawDelete, dev); err != nil { // sisa impor yang gagal sebelumnya
			return err
		}
		if err := st.AppendEvents(evs); err != nil {
			return err
		}
		if err := st.AppendMinutes(mins); err != nil {
			return err
		}
		for i := 0; i < len(raw); i += 5000 {
			j := i + 5000
			if j > len(raw) {
				j = len(raw)
			}
			if err := st.AppendRaw(raw[i:j]); err != nil {
				return err
			}
		}
		return nil
	})
}
