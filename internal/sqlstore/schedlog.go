package sqlstore

import (
	"database/sql"
	"encoding/json"

	"owrt-dashboard/internal/sched"
)

// schedLog menyimpan log eksekusi scene (berhasil/gagal, beserta perangkat yang gagal).
type schedLog struct{ db *sql.DB }

// SchedLog mengembalikan penyimpanan log scene untuk paket sched.
func (d *DB) SchedLog() sched.RunStore { return &schedLog{d.db} }

func (s *schedLog) AddRun(r sched.Run) error {
	fails := "[]"
	if len(r.Fails) > 0 {
		if b, err := json.Marshal(r.Fails); err == nil {
			fails = string(b)
		}
	}
	ok := 0
	if r.OK {
		ok = 1
	}
	_, err := s.db.Exec(qSchedAdd, r.T, r.Fire, r.Scene, r.Label, ok, r.Err, fails)
	return err
}

func (s *schedLog) Runs(limit int) ([]sched.Run, error) {
	rows, err := s.db.Query(qSchedRuns, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sched.Run
	for rows.Next() {
		var r sched.Run
		var ok int
		var fails string
		if err := rows.Scan(&r.T, &r.Fire, &r.Scene, &r.Label, &ok, &r.Err, &fails); err != nil {
			return nil, err
		}
		r.OK = ok == 1
		if fails != "" && fails != "[]" {
			json.Unmarshal([]byte(fails), &r.Fails)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *schedLog) LastRuns() (map[string]sched.LastRun, error) {
	rows, err := s.db.Query(qSchedLast)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]sched.LastRun{}
	for rows.Next() {
		var scene string
		var ok int
		var t int64
		if err := rows.Scan(&scene, &ok, &t); err != nil {
			return nil, err
		}
		l := out[scene]
		if ok == 1 {
			l.OK = t
		} else {
			l.Fail = t
		}
		out[scene] = l
	}
	return out, rows.Err()
}

func (s *schedLog) Prune(before int64) error {
	_, err := s.db.Exec(qSchedPrune, before)
	return err
}
