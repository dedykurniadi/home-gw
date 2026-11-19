package energy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Cfg adalah pengaturan estimasi energi satu perangkat (bisa diubah dari dashboard).
// Watt = daya beban saat ON. StandbyW = konsumsi modul Sonoff sendiri (dihitung selama perangkat terdengar,
// ON maupun OFF). Note = catatan bebas.
type Cfg struct {
	Watt     float64 `json:"watt"`
	StandbyW float64 `json:"standby_watt"`
	Note     string  `json:"note"`
}

var ErrCfg = errors.New("pengaturan energi tidak valid")

// CfgStore menyimpan Cfg per perangkat (SQLite atau file JSON).
type CfgStore interface {
	LoadAll() (map[string]Cfg, error)
	Save(id string, c Cfg) error
}

// Config menyimpan pengaturan di memori dan meneruskan perubahan ke store.
type Config struct {
	mu sync.RWMutex
	st CfgStore
	m  map[string]Cfg
}

// OpenConfig memuat dari st. seed (mis. nilai lama devices.json) hanya dipakai untuk perangkat yang belum
// punya data di store, lalu disimpan sehingga store yang berlaku sesudahnya.
func OpenConfig(st CfgStore, seed map[string]Cfg) (*Config, error) {
	m, err := st.LoadAll()
	if err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]Cfg{}
	}
	c := &Config{st: st, m: m}
	for id, s := range seed {
		if _, ok := m[id]; ok || (s.Watt == 0 && s.StandbyW == 0 && s.Note == "") {
			continue
		}
		if err := st.Save(id, s); err != nil {
			return c, err
		}
		m[id] = s
	}
	return c, nil
}

func (c *Config) Get(id string) Cfg {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.m[id]
}

// Set memvalidasi dan menyimpan. Watt 0..5000, standby 0..500, catatan maks 200 karakter.
func (c *Config) Set(id string, v Cfg) error {
	v.Note = strings.TrimSpace(v.Note)
	if v.Watt < 0 || v.Watt > 5000 || v.StandbyW < 0 || v.StandbyW > 500 || len([]rune(v.Note)) > 200 || v.Watt != v.Watt || v.StandbyW != v.StandbyW {
		return ErrCfg
	}
	v.Watt, v.StandbyW = round(v.Watt, 2), round(v.StandbyW, 2)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.st.Save(id, v); err != nil {
		return err
	}
	c.m[id] = v
	return nil
}

/* ---- penyimpanan file JSON (tanpa SQLite) ---- */

type fileCfg struct {
	mu   sync.Mutex
	path string
}

func NewFileCfg(path string) CfgStore { return &fileCfg{path: path} }

func (f *fileCfg) LoadAll() (map[string]Cfg, error) {
	b, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Cfg{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]Cfg{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (f *fileCfg) Save(id string, c Cfg) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.LoadAll()
	if err != nil {
		return err
	}
	m[id] = c
	b, _ := json.MarshalIndent(m, "", " ")
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}
