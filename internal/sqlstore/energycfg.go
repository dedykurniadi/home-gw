package sqlstore

import (
	"database/sql"

	"owrt-dashboard/internal/energy"
)

type energyCfg struct{ db *sql.DB }

// EnergyCfg mengembalikan penyimpanan SQLite untuk pengaturan estimasi energi per perangkat.
func (d *DB) EnergyCfg() energy.CfgStore { return &energyCfg{d.db} }

func (s *energyCfg) LoadAll() (map[string]energy.Cfg, error) {
	rows, err := s.db.Query(qCfgAll)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]energy.Cfg{}
	for rows.Next() {
		var id string
		var c energy.Cfg
		if err := rows.Scan(&id, &c.Watt, &c.StandbyW, &c.Note); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

func (s *energyCfg) Save(id string, c energy.Cfg) error {
	_, err := s.db.Exec(qCfgSave, id, c.Watt, c.StandbyW, c.Note)
	return err
}
