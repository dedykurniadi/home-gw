package sqlstore

import (
	"database/sql"
	"homegw/internal/sonoff"
)

// DeviceStore mengelola penyimpanan konfigurasi perangkat Sonoff di SQLite.
type DeviceStore interface {
	All() ([]sonoff.Device, error)
	Get(id string) (sonoff.Device, error)
	Save(d sonoff.Device) error
	Delete(id string) error
}

type devStore struct{ db *sql.DB }

func (d *DB) Devices() DeviceStore { return &devStore{d.db} }

func (s *devStore) All() ([]sonoff.Device, error) {
	rows, err := s.db.Query(qDevAll)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sonoff.Device
	for rows.Next() {
		var d sonoff.Device
		if err := rows.Scan(&d.ID, &d.Name, &d.Key, &d.MAC, &d.IP, &d.Kind, &d.Watt, &d.StandbyW); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *devStore) Get(id string) (sonoff.Device, error) {
	var d sonoff.Device
	err := s.db.QueryRow(qDevGet, id).Scan(&d.ID, &d.Name, &d.Key, &d.MAC, &d.IP, &d.Kind, &d.Watt, &d.StandbyW)
	return d, err
}

func (s *devStore) Save(d sonoff.Device) error {
	if d.Kind == "" {
		d.Kind = "switch"
	}
	_, err := s.db.Exec(qDevSave, d.ID, d.Name, d.Key, d.MAC, d.IP, d.Kind, d.Watt, d.StandbyW)
	return err
}

func (s *devStore) Delete(id string) error {
	_, err := s.db.Exec(qDevDelete, id)
	return err
}
