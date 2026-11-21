package sqlstore

import (
	"database/sql"
	"strings"

	"homegw/internal/netdata"
)

type wifiBlockStore struct{ db *sql.DB }

func (d *DB) WifiBlocks() netdata.WifiBlockStore { return &wifiBlockStore{d.db} }

func (s *wifiBlockStore) All() ([]netdata.WifiBlockItem, error) {
	rows, err := s.db.Query(qWifiBlockAll)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []netdata.WifiBlockItem
	for rows.Next() {
		var it netdata.WifiBlockItem
		if err := rows.Scan(&it.MAC, &it.IP, &it.Name, &it.BannedAt, &it.BanUntil); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *wifiBlockStore) Upsert(item netdata.WifiBlockItem) error {
	mac := strings.ToLower(strings.TrimSpace(item.MAC))
	if mac == "" {
		return nil
	}
	_, err := s.db.Exec(qWifiBlockUpsert, mac, item.IP, item.Name, item.BannedAt, item.BanUntil)
	return err
}

func (s *wifiBlockStore) Delete(mac string) error {
	_, err := s.db.Exec(qWifiBlockDelete, strings.ToLower(strings.TrimSpace(mac)))
	return err
}

func (s *wifiBlockStore) Prune(now int64) error {
	_, err := s.db.Exec(qWifiBlockPrune, now)
	return err
}
