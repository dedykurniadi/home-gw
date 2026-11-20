package sqlstore

import (
	"database/sql"
	"encoding/json"
	"homegw/internal/sched"
)

type sceneStore struct{ db *sql.DB }

// Scenes mengembalikan implementasi sched.SceneStore berbasis SQLite.
func (d *DB) Scenes() sched.SceneStore { return &sceneStore{d.db} }

func (s *sceneStore) All() ([]sched.Scene, error) {
	rows, err := s.db.Query(qSceneAll)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []sched.Scene
	for rows.Next() {
		var sc sched.Scene
		var trigJSON, actJSON, daysJSON, lastJSON string
		var enabled int
		if err := rows.Scan(&sc.ID, &sc.Name, &trigJSON, &actJSON, &daysJSON, &enabled, &lastJSON); err != nil {
			return nil, err
		}
		sc.Enabled = (enabled != 0)
		_ = json.Unmarshal([]byte(trigJSON), &sc.Triggers)
		_ = json.Unmarshal([]byte(actJSON), &sc.Actions)
		_ = json.Unmarshal([]byte(daysJSON), &sc.Days)
		_ = json.Unmarshal([]byte(lastJSON), &sc.Last)
		if sc.Days == nil {
			sc.Days = []int{}
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *sceneStore) Save(sc sched.Scene) error {
	trigJSON, _ := json.Marshal(sc.Triggers)
	actJSON, _ := json.Marshal(sc.Actions)
	daysJSON, _ := json.Marshal(sc.Days)
	lastJSON, _ := json.Marshal(sc.Last)
	enabled := 0
	if sc.Enabled {
		enabled = 1
	}

	_, err := s.db.Exec(qSceneSave, sc.ID, sc.Name, string(trigJSON), string(actJSON), string(daysJSON), enabled, string(lastJSON))
	return err
}

func (s *sceneStore) Delete(id string) error {
	_, err := s.db.Exec(qSceneDelete, id)
	return err
}
