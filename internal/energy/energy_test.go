package energy

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestEstimateAndMeasured(t *testing.T) {
	loc := time.FixedZone("WIB", 7*3600)
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, loc) // 12 jam sejak tengah malam
	mid := time.Date(2026, 11, 3, 0, 0, 0, 0, loc).Unix()
	secs := func(id string, from, to int64) (int64, int64, int64, bool) {
		if id != "led" {
			return 0, 0, 0, false
		}
		// riwayat ada sejak 2 jam sebelum tengah malam: ON 4 jam, OFF sisanya
		start := mid - 7200
		if from < start {
			from = start
		}
		total := to - from
		return 4 * 3600, total - 4*3600, 0, true
	}
	day, month := 1.5, 20.25
	meter := func(id string) (*float64, *float64) {
		if id == "ac" {
			return &day, &month
		}
		return nil, nil
	}
	devs := []Dev{
		{ID: "led", Watt: 10, StandbyW: 0.5},
		{ID: "ac", Metered: true},
		{ID: "x"}, // watt belum diisi
	}
	r := Compute(devs, secs, meter, 1500, now, loc)

	led := r.Devices["led"]
	if led.Basis != Estimate {
		t.Fatalf("basis %s", led.Basis)
	}
	// hari ini: ON 4 j x 10 W = 0,040 kWh; standby 0,5 W x 12 j = 0,006 kWh
	if want := 0.046; math.Abs(led.Today.KWh-want) > 1e-6 || led.Today.Cost != 69 {
		t.Fatalf("hari ini %+v", led.Today)
	}
	if led.Today.UnknownSec != 0 {
		t.Fatalf("unknown %d", led.Today.UnknownSec)
	}
	// minggu: riwayat baru ada 14 jam terakhir; sisanya tidak diketahui
	if led.Week.UnknownSec != 7*86400-14*3600 {
		t.Fatalf("unknown minggu %d", led.Week.UnknownSec)
	}
	ac := r.Devices["ac"]
	if ac.Basis != Measured || ac.Today.KWh != 1.5 || ac.Month.KWh != 20.25 || ac.Today.Cost != 2250 || ac.Week != nil {
		t.Fatalf("ac %+v", ac)
	}
	if r.Devices["x"].Basis != None || r.Devices["x"].Today != nil {
		t.Fatalf("x %+v", r.Devices["x"])
	}
	if r.Total.N != 2 || math.Abs(r.Total.Today.KWh-1.546) > 1e-6 {
		t.Fatalf("total %+v", r.Total)
	}
}

func TestMonthBoundaryUsesZone(t *testing.T) {
	loc := time.FixedZone("WIB", 7*3600)
	now := time.Date(2026, 12, 1, 0, 30, 0, 0, loc) // 00:30 WIB tanggal 1 = 17:30 UTC tgl 30 Nov
	var gotFrom int64
	secs := func(id string, from, to int64) (int64, int64, int64, bool) {
		if to-from == 1800 {
			gotFrom = from
		}
		return 0, 0, 0, true
	}
	Compute([]Dev{{ID: "a", Watt: 5}}, secs, nil, 0, now, loc)
	if gotFrom != time.Date(2026, 12, 1, 0, 0, 0, 0, loc).Unix() {
		t.Fatalf("awal bulan salah: %d", gotFrom)
	}
}

func TestMonthly(t *testing.T) {
	loc := time.FixedZone("WIB", 7*3600)
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, loc)
	oct := time.Date(2026, 10, 1, 0, 0, 0, 0, loc).Unix()
	nov := time.Date(2026, 11, 1, 0, 0, 0, 0, loc).Unix()
	secs := func(id string, from, to int64) (int64, int64, int64, bool) {
		if id != "led" || from < oct { // riwayat baru ada sejak Oktober
			return 0, 0, 0, false
		}
		total := to - from
		return total / 2, total - total/2, 0, true // ON setengah waktu
	}
	month := 12.5
	meter := func(id string) (*float64, *float64) { return nil, &month }
	daily := func(id string, y int, m time.Month) (float64, int) {
		if y == 2026 && m == time.October {
			return 30.5, 31
		}
		return 0, 0
	}
	devs := []Dev{{ID: "led", Watt: 10}, {ID: "ac", Metered: true}}
	r := Monthly(devs, secs, daily, meter, 1000, now, loc, 3)
	if len(r.Months) != 3 || r.Months[0].Key != "2026-11" || !r.Months[0].Partial || r.Months[2].Key != "2026-09" {
		t.Fatalf("bulan: %+v", r.Months)
	}
	octM := r.Months[1]
	want := float64(31*24*3600/2) * 10 / 3.6e6 // 31 hari x 12 jam x 10 W
	if d := octM.Devices["led"]; !d.Known || math.Abs(d.KWh-want) > 0.01 {
		t.Fatalf("estimasi Okt: %+v want %.3f", d, want)
	}
	if d := octM.Devices["ac"]; !d.Known || d.KWh != 30.5 || d.Days != 31 {
		t.Fatalf("terukur Okt: %+v", d)
	}
	if d := r.Months[0].Devices["ac"]; d.KWh != 12.5 || !d.Known {
		t.Fatalf("terukur Nov: %+v", d)
	}
	if d := r.Months[2].Devices["led"]; d.Known {
		t.Fatalf("Sep seharusnya tidak diketahui: %+v", d)
	}
	if d := r.Months[2].Devices["ac"]; d.Known || octM.Total.Cost != math.Round((want+30.5)*1000) && math.Abs(octM.Total.Cost-(want+30.5)*1000) > 2 {
		t.Fatalf("Sep AC / total Okt: %+v %+v", d, octM.Total)
	}
	_ = nov
}

func TestDailyPersist(t *testing.T) {
	loc := time.FixedZone("WIB", 7*3600)
	p := filepath.Join(t.TempDir(), "d.csv")
	d, err := OpenDaily(p)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, loc)
	d.Record("ac", now, loc, 1.2)
	d.Record("ac", now.Add(time.Hour), loc, 1.9)
	d.Record("ac", now.Add(2*time.Hour), loc, 0.1) // counter turun (reset) tidak menimpa
	d.Record("ac", now.AddDate(0, 0, 1), loc, 2.0)
	if err := d.Flush(now, loc); err != nil {
		t.Fatal(err)
	}
	d2, err := OpenDaily(p)
	if err != nil {
		t.Fatal(err)
	}
	if k, n := d2.Month("ac", 2026, time.November); n != 2 || math.Abs(k-3.9) > 1e-9 {
		t.Fatalf("%v %d", k, n)
	}
}

func TestCompareWeekStartsMonday(t *testing.T) {
	loc := time.FixedZone("WIB", 7*3600)
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, loc) // Jumat
	devs := []Dev{{ID: "m", Metered: true}, {ID: "e", Watt: 10}}
	days := map[string]float64{"2026-11-09": 1, "2026-11-10": 2, "2026-11-12": 3, "2026-11-13": 0.5, "2026-11-02": 4, "2026-11-08": 6, "2026-10-20": 7}
	daily := func(id, from, to string) (float64, int) {
		var s float64
		n := 0
		for d, v := range days {
			if d >= from && d <= to {
				s += v
				n++
			}
		}
		return s, n
	}
	secs := func(id string, from, to int64) (int64, int64, int64, bool) { return 3600, 0, 0, true } // 1 jam ON
	r := Compare(devs, secs, daily, nil, 1000, now, loc)
	if r.WeekOf != "2026-11-09" {
		t.Fatalf("awal minggu %s", r.WeekOf)
	}
	m := r.Devices["m"].Periods
	if m["week"].KWh != 6.5 || m["last_week"].KWh != 10 || m["today"].KWh != 0.5 || m["yesterday"].KWh != 3 || m["month"].KWh != 16.5 {
		t.Fatalf("terukur: %+v", m)
	}
	if m["last_month"].Days != 1 { // hanya 1 hari tercatat bulan lalu: app menampilkan "1 hari tercatat"
		t.Fatalf("last_month %+v", m["last_month"])
	}
	if m["last_month"].KWh != 7 {
		t.Fatalf("last_month %+v", m["last_month"])
	}
	e := r.Devices["e"].Periods["week"]
	if e.KWh != 0.01 || !e.Known {
		t.Fatalf("estimasi %+v", e)
	}
	if got := r.Total["week"].KWh; got != 6.51 {
		t.Fatalf("total week %v", got)
	}
}

func TestDailyMergeFrom(t *testing.T) {
	dir := t.TempDir()
	a, _ := OpenDaily(dir + "/a.csv")
	b, _ := OpenDaily(dir + "/b.csv")
	a.put("p", "2026-11-01", 1)
	a.put("p", "2026-11-02", 5)
	b.put("p", "2026-11-02", 3) // lebih kecil: tidak menimpa
	b.put("p", "2026-11-03", 2) // baru
	b.put("p", "2026-11-01", 4) // lebih besar: menang
	if n := a.MergeFrom(b); n != 2 {
		t.Fatalf("berubah %d", n)
	}
	if k, d := a.Range("p", "2026-11-01", "2026-11-03"); d != 3 || k != 4+5+2 {
		t.Fatalf("hasil %v %v", k, d)
	}
	if n := a.MergeFrom(b); n != 0 {
		t.Fatalf("tidak idempotent %d", n)
	}
}
