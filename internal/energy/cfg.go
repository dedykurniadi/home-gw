package energy

import (
	"errors"
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

// CfgStore menyimpan Cfg per perangkat (SQLite).
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
