package sqlstore

// Semua pernyataan SQL dikumpulkan di sini (dan diuji terhadap SQLite asli di luar Go).

var schemaStmts = []string{
	`CREATE TABLE IF NOT EXISTS meta(k TEXT PRIMARY KEY, v TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS onoff_events(id TEXT NOT NULL, t INTEGER NOT NULL, state TEXT NOT NULL, PRIMARY KEY(id, t))`,
	`CREATE TABLE IF NOT EXISTS pw_raw(dev TEXT NOT NULL, t REAL NOT NULL, w REAL NOT NULL, v REAL NOT NULL, a REAL NOT NULL, sw TEXT NOT NULL, src TEXT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS pw_raw_dev_t ON pw_raw(dev, t)`,
	`CREATE TABLE IF NOT EXISTS pw_min(dev TEXT NOT NULL, t INTEGER NOT NULL, w_avg REAL NOT NULL, w_min REAL NOT NULL, w_max REAL NOT NULL, v REAL NOT NULL, a REAL NOT NULL, cov REAL NOT NULL, PRIMARY KEY(dev, t))`,
	`CREATE TABLE IF NOT EXISTS pw_events(seq INTEGER PRIMARY KEY AUTOINCREMENT, dev TEXT NOT NULL, t INTEGER NOT NULL, state TEXT NOT NULL, w REAL NOT NULL, prev TEXT NOT NULL, dur INTEGER NOT NULL, kwh REAL NOT NULL, UNIQUE(dev, t, state))`,
	`CREATE TABLE IF NOT EXISTS sched_log(seq INTEGER PRIMARY KEY AUTOINCREMENT, t INTEGER NOT NULL, fire INTEGER NOT NULL, scene TEXT NOT NULL, label TEXT NOT NULL, ok INTEGER NOT NULL, err TEXT NOT NULL, fails TEXT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS sched_log_t ON sched_log(t)`,
	`CREATE TABLE IF NOT EXISTS energy_cfg(id TEXT PRIMARY KEY, watt REAL NOT NULL, standby REAL NOT NULL, note TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS onoff_src(id TEXT NOT NULL, t INTEGER NOT NULL, src TEXT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS onoff_src_id_t ON onoff_src(id, t)`,
	`CREATE TABLE IF NOT EXISTS energy_daily(dev TEXT NOT NULL, day TEXT NOT NULL, kwh REAL NOT NULL, PRIMARY KEY(dev, day))`,
	`CREATE TABLE IF NOT EXISTS devices(id TEXT PRIMARY KEY, name TEXT NOT NULL, key TEXT NOT NULL, mac TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL DEFAULT 'switch', watt REAL NOT NULL DEFAULT 0, standby_watt REAL NOT NULL DEFAULT 0, created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')))`,
	`CREATE TABLE IF NOT EXISTS wifi_history(mac TEXT PRIMARY KEY, ip TEXT NOT NULL DEFAULT '', name TEXT NOT NULL DEFAULT '', band TEXT NOT NULL DEFAULT '', last_seen INTEGER NOT NULL DEFAULT 0, connected INTEGER NOT NULL DEFAULT 0)`,
	`CREATE TABLE IF NOT EXISTS wifi_blocks(mac TEXT PRIMARY KEY, ip TEXT NOT NULL DEFAULT '', name TEXT NOT NULL DEFAULT '', banned_at INTEGER NOT NULL DEFAULT 0, ban_until INTEGER NOT NULL DEFAULT 0)`,
	`CREATE TABLE IF NOT EXISTS scenes(id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', triggers TEXT NOT NULL DEFAULT '[]', actions TEXT NOT NULL DEFAULT '[]', days TEXT NOT NULL DEFAULT '[]', enabled INTEGER NOT NULL DEFAULT 1, last TEXT NOT NULL DEFAULT '[]')`,
}

const (
	qCfgAll  = `SELECT id, watt, standby, note FROM energy_cfg`
	qCfgSave = `INSERT INTO energy_cfg(id, watt, standby, note) VALUES(?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET watt = excluded.watt, standby = excluded.standby, note = excluded.note`

	qMetaGet = `SELECT v FROM meta WHERE k = ?`
	qMetaSet = `INSERT INTO meta(k, v) VALUES(?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`

	qOnoffIDs    = `SELECT DISTINCT id FROM onoff_events`
	qOnoffBase   = `SELECT COALESCE(MAX(t), 0) FROM onoff_events WHERE id = ? AND t < ?`
	qOnoffLoad   = `SELECT t, state FROM onoff_events WHERE id = ? AND t >= ? ORDER BY t`
	qOnoffPrune  = `DELETE FROM onoff_events WHERE id = ? AND t < ?`
	qOnoffAppend = `INSERT INTO onoff_events(id, t, state) VALUES(?, ?, ?) ON CONFLICT(id, t) DO UPDATE SET state = excluded.state`
	qOnoffDelete = `DELETE FROM onoff_events WHERE id = ?`

	qSrcLoad    = `SELECT t, id, src FROM onoff_src WHERE t >= ? ORDER BY t`
	qSrcAppend  = `INSERT INTO onoff_src(id, t, src) VALUES(?, ?, ?)`
	qSrcPrune   = `DELETE FROM onoff_src WHERE t < ?`
	qDailyAll   = `SELECT dev, day, kwh FROM energy_daily`
	qDailySave  = `INSERT INTO energy_daily(dev, day, kwh) VALUES(?, ?, ?) ON CONFLICT(dev, day) DO UPDATE SET kwh = excluded.kwh`
	qDailyPrune = `DELETE FROM energy_daily WHERE day < ?`

	qRawInsert = `INSERT INTO pw_raw(dev, t, w, v, a, sw, src) VALUES(?, ?, ?, ?, ?, ?, ?)`
	qRawLast   = `SELECT t, w, v, a, sw, src FROM pw_raw WHERE dev = ? ORDER BY t DESC LIMIT 1`
	qRawPrune  = `DELETE FROM pw_raw WHERE dev = ? AND t < ?`

	qMinUpsert = `INSERT INTO pw_min(dev, t, w_avg, w_min, w_max, v, a, cov) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(dev, t) DO UPDATE SET w_avg = excluded.w_avg, w_min = excluded.w_min, w_max = excluded.w_max,
		v = excluded.v, a = excluded.a, cov = excluded.cov`
	qMinLoad  = `SELECT t, w_avg, w_min, w_max, v, a, cov FROM pw_min WHERE dev = ? AND t >= ? ORDER BY t`
	qMinPrune = `DELETE FROM pw_min WHERE dev = ? AND t < ?`

	qEvUpsert = `INSERT INTO pw_events(dev, t, state, w, prev, dur, kwh) VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(dev, t, state) DO UPDATE SET w = excluded.w, prev = excluded.prev, dur = excluded.dur, kwh = excluded.kwh`
	qEvLoad = `SELECT t, state, w, prev, dur, kwh FROM pw_events WHERE dev = ? AND t >= ? ORDER BY t, seq`
)

const (
	qSchedAdd   = `INSERT INTO sched_log(t, fire, scene, label, ok, err, fails) VALUES(?, ?, ?, ?, ?, ?, ?)`
	qSchedRuns  = `SELECT t, fire, scene, label, ok, err, fails FROM sched_log ORDER BY seq DESC LIMIT ?`
	qSchedLast  = `SELECT scene, ok, MAX(t) FROM sched_log GROUP BY scene, ok`
	qSchedPrune = `DELETE FROM sched_log WHERE t < ?`

	qDevAll    = `SELECT id, name, key, mac, ip, kind, watt, standby_watt FROM devices ORDER BY created_at`
	qDevGet    = `SELECT id, name, key, mac, ip, kind, watt, standby_watt FROM devices WHERE id = ?`
	qDevSave   = `INSERT INTO devices(id, name, key, mac, ip, kind, watt, standby_watt) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, key = excluded.key, mac = excluded.mac,
		ip = excluded.ip, kind = excluded.kind, watt = excluded.watt, standby_watt = excluded.standby_watt`
	qDevDelete = `DELETE FROM devices WHERE id = ?`

	qWifiHistAll    = `SELECT mac, ip, name, band, last_seen, connected FROM wifi_history ORDER BY last_seen DESC`
	qWifiHistUpsert = `INSERT INTO wifi_history(mac, ip, name, band, last_seen, connected) VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(mac) DO UPDATE SET
			ip = CASE WHEN excluded.ip != '' AND excluded.ip != '-' THEN excluded.ip ELSE wifi_history.ip END,
			name = CASE WHEN excluded.name != '' THEN excluded.name ELSE wifi_history.name END,
			band = CASE WHEN excluded.band != '' THEN excluded.band ELSE wifi_history.band END,
			last_seen = excluded.last_seen,
			connected = CASE WHEN excluded.connected > 0 THEN excluded.connected ELSE wifi_history.connected END`
	qWifiHistDelete = `DELETE FROM wifi_history WHERE mac = ?`
	qWifiHistPrune  = `DELETE FROM wifi_history WHERE last_seen < ?`

	qWifiBlockAll    = `SELECT mac, ip, name, banned_at, ban_until FROM wifi_blocks ORDER BY banned_at DESC`
	qWifiBlockUpsert = `INSERT INTO wifi_blocks(mac, ip, name, banned_at, ban_until) VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(mac) DO UPDATE SET ip = excluded.ip, name = excluded.name, banned_at = excluded.banned_at, ban_until = excluded.ban_until`
	qWifiBlockDelete = `DELETE FROM wifi_blocks WHERE mac = ?`
	qWifiBlockPrune  = `DELETE FROM wifi_blocks WHERE ban_until > 0 AND ban_until < ?`

	qSceneAll    = `SELECT id, name, triggers, actions, days, enabled, last FROM scenes ORDER BY id`
	qSceneSave   = `INSERT INTO scenes(id, name, triggers, actions, days, enabled, last) VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, triggers = excluded.triggers, actions = excluded.actions,
		days = excluded.days, enabled = excluded.enabled, last = excluded.last`
	qSceneDelete = `DELETE FROM scenes WHERE id = ?`
)
