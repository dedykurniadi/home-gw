package mdnslog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteAndRotate(t *testing.T) {
	dir := t.TempDir()
	loc := time.FixedZone("x", 7*3600)
	l, err := Open(dir, 2, loc)
	if err != nil {
		t.Fatal(err)
	}
	r := -61
	t0 := time.Date(2026, 11, 3, 17, 46, 54, 500e6, loc)
	l.Query(t0)
	l.Packet(Packet{T: t0.Add(time.Second), ID: "1000c47454", IP: "10.142.27.206", Seq: "366", ViaQ: true, Sw: "on", RSSI: &r, Dec: "ok"})
	l.Packet(Packet{T: t0.Add(time.Second), ID: "a,b\n", Seq: "367", Dup: true, Dec: "fail"})
	l.Packet(Packet{T: t0.Add(25 * time.Hour), ID: "x", Seq: "1", Dec: "ok"}) // hari berikutnya
	l.Close()

	b, err := os.ReadFile(filepath.Join(dir, "mdns-20261103.csv"))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(b)), "\n")
	want := []string{
		"t,ev,id,ip,seq,dup,viaq,sw,rssi,dec",
		"1793702814.500,q,,,,,,,,",
		"1793702815.500,p,1000c47454,10.142.27.206,366,0,1,on,-61,ok",
		"1793702815.500,p,a_b_,,367,1,0,,,fail",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("isi:\n%s\nharapan:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if _, err := os.Stat(filepath.Join(dir, "mdns-20261104.csv")); err != nil {
		t.Fatalf("rotasi hari: %v", err)
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	loc := time.UTC
	for _, d := range []string{"20261101", "20261102", "20261104"} {
		os.WriteFile(filepath.Join(dir, "mdns-"+d+".csv"), []byte("x"), 0o644)
	}
	l, _ := Open(dir, 2, loc)
	l.Query(time.Date(2026, 11, 5, 1, 0, 0, 0, loc)) // pangkas < 20261103
	l.Close()
	for d, keep := range map[string]bool{"20261101": false, "20261102": false, "20261104": true, "20261105": true} {
		_, err := os.Stat(filepath.Join(dir, "mdns-"+d+".csv"))
		if (err == nil) != keep {
			t.Errorf("%s: ada=%v harusnya %v", d, err == nil, keep)
		}
	}
}
