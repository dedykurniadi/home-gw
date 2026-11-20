package sqlstore

import (
	"database/sql"
	"strings"
	"time"

	"homegw/internal/netdata"
)

type wifiHistStore struct{ db *sql.DB }

func (d *DB) WifiHist() netdata.WifiHistStore { return &wifiHistStore{d.db} }

func (s *wifiHistStore) All() ([]netdata.WifiHistoryItem, error) {
	rows, err := s.db.Query(qWifiHistAll)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []netdata.WifiHistoryItem
	for rows.Next() {
		var it netdata.WifiHistoryItem
		if err := rows.Scan(&it.MAC, &it.IP, &it.Name, &it.Band, &it.LastSeen, &it.Connected); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *wifiHistStore) Upsert(item netdata.WifiHistoryItem) error {
	mac := strings.ToLower(strings.TrimSpace(item.MAC))
	if mac == "" {
		return nil
	}
	_, err := s.db.Exec(qWifiHistUpsert, mac, item.IP, item.Name, item.Band, item.LastSeen, item.Connected)
	return err
}

func (s *wifiHistStore) Delete(mac string) error {
	_, err := s.db.Exec(qWifiHistDelete, strings.ToLower(strings.TrimSpace(mac)))
	return err
}

func (s *wifiHistStore) Prune(olderThan time.Duration) error {
	cutoff := time.Now().Add(-olderThan).Unix()
	_, err := s.db.Exec(qWifiHistPrune, cutoff)
	return err
}
