package energy

import (
	"path/filepath"
	"testing"
)

func TestConfigSeedSetAndPersist(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d", "cfg.json")
	c, err := OpenConfig(NewFileCfg(p), map[string]Cfg{"a": {Watt: 9, StandbyW: 1}, "b": {}})
	if err != nil {
		t.Fatal(err)
	}
	if g := c.Get("a"); g.Watt != 9 || g.StandbyW != 1 {
		t.Fatalf("seed: %+v", g)
	}
	if err := c.Set("a", Cfg{Watt: 12.5, StandbyW: 0.8, Note: " modul Mini "}); err != nil {
		t.Fatal(err)
	}
	// seed baru tidak menimpa data yang sudah ada
	c2, _ := OpenConfig(NewFileCfg(p), map[string]Cfg{"a": {Watt: 99}})
	if g := c2.Get("a"); g.Watt != 12.5 || g.Note != "modul Mini" {
		t.Fatalf("persist: %+v", g)
	}
	for _, bad := range []Cfg{{Watt: -1}, {Watt: 6000}, {StandbyW: 501}} {
		if c.Set("a", bad) == nil {
			t.Fatalf("harus ditolak: %+v", bad)
		}
	}
}
