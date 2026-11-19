package energy

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DailyKWh memberi jumlah kWh terukur satu perangkat selama bulan (y, m) dari catatan harian,
// dan berapa hari yang tercatat (days=0 berarti belum ada catatan).
type DailyKWh func(id string, y int, m time.Month) (kwh float64, days int)

// MonthDev = hasil satu perangkat pada satu bulan.
type MonthDev struct {
	Basis      string  `json:"basis"`
	KWh        float64 `json:"kwh"`
	Cost       float64 `json:"cost"`
	UnknownSec int64   `json:"unknown_sec,omitempty"` // estimasi: rentang tidak diketahui (tidak terdengar / belum ada riwayat)
	Days       int     `json:"days,omitempty"`        // terukur: jumlah hari yang tercatat pada bulan itu
	Known      bool    `json:"known"`                 // false = tidak ada data untuk bulan itu
}

type Month struct {
	Key     string              `json:"key"`     // "2026-11"
	Partial bool                `json:"partial"` // bulan berjalan
	Total   Period              `json:"total"`
	Devices map[string]MonthDev `json:"devices"`
}

type MonthlyReport struct {
	Tariff float64 `json:"tariff"`
	Months []Month `json:"months"` // terbaru lebih dulu
}

// Monthly menyusun rekap n bulan terakhir (termasuk bulan berjalan).
func Monthly(devs []Dev, secs Seconds, daily DailyKWh, meter Meter, tariff float64, now time.Time, loc *time.Location, n int) MonthlyReport {
	if loc == nil {
		loc = time.Local
	}
	if n < 1 {
		n = 1
	}
	ln := now.In(loc)
	rep := MonthlyReport{Tariff: tariff, Months: []Month{}}
	for i := 0; i < n; i++ {
		start := time.Date(ln.Year(), ln.Month()-time.Month(i), 1, 0, 0, 0, 0, loc)
		end := start.AddDate(0, 1, 0)
		cur := i == 0
		to := end.Unix()
		if cur {
			to = now.Unix()
		}
		m := Month{Key: start.Format("2006-01"), Partial: cur, Devices: map[string]MonthDev{}}
		for _, d := range devs {
			md := MonthDev{Basis: None}
			switch {
			case d.Metered:
				md.Basis = Measured
				var kwh float64
				if cur && meter != nil {
					if _, month := meter(d.ID); month != nil {
						kwh, md.Known = *month, true
					}
				}
				if daily != nil {
					if k, days := daily(d.ID, start.Year(), start.Month()); days > 0 {
						md.Days = days
						if !md.Known {
							kwh, md.Known = k, true
						}
					}
				}
				md.KWh = round(kwh, 3)
			case d.Watt > 0 && secs != nil:
				md.Basis = Estimate
				if on, off, _, ok := secs(d.ID, start.Unix(), to); ok {
					md.KWh = round((float64(on)*d.Watt+float64(on+off)*d.StandbyW)/3.6e6, 3)
					md.UnknownSec = (to - start.Unix()) - (on + off)
					md.Known = on+off > 0
				}
			}
			md.Cost = round(md.KWh*tariff, 0)
			m.Devices[d.ID] = md
			if md.Known {
				m.Total.KWh += md.KWh
				m.Total.UnknownSec += md.UnknownSec
			}
		}
		m.Total.KWh = round(m.Total.KWh, 3)
		m.Total.Cost = round(m.Total.KWh*tariff, 0)
		rep.Months = append(rep.Months, m)
	}
	return rep
}

/* ---------------- catatan harian kWh terukur (POWR) ---------------- */

const dailyKeepDays = 400

// Daily menyimpan kWh harian dari meter perangkat (nilai tertinggi yang terlihat per hari),
// karena perangkat sendiri hanya melaporkan "hari ini" dan "bulan ini".
type Daily struct {
	path  string
	mu    sync.Mutex
	m     map[string]map[string]float64 // id -> "YYYY-MM-DD" -> kWh
	dirty bool
}

// OpenDaily memuat catatan dari path (belum ada = kosong).
func OpenDaily(path string) (*Daily, error) {
	d := &Daily{path: path, m: map[string]map[string]float64{}}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(strings.TrimSpace(sc.Text()), ",")
		if len(p) != 3 {
			continue
		}
		v, err := strconv.ParseFloat(p[2], 64)
		if err != nil || v < 0 {
			continue
		}
		d.put(p[1], p[0], v)
	}
	return d, nil
}

func (d *Daily) put(id, day string, kwh float64) {
	if d.m[id] == nil {
		d.m[id] = map[string]float64{}
	}
	if kwh > d.m[id][day] || !d.has(id, day) {
		d.m[id][day] = kwh
		d.dirty = true
	}
}

func (d *Daily) has(id, day string) bool { _, ok := d.m[id][day]; return ok }

// Record mencatat kWh hari ini; hanya naik (counter perangkat reset tengah malam).
func (d *Daily) Record(id string, now time.Time, loc *time.Location, kwh float64) {
	if kwh < 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.put(id, now.In(loc).Format("2006-01-02"), kwh)
}

// Month menjumlahkan kWh dan menghitung hari yang tercatat pada bulan (y, m).
func (d *Daily) Month(id string, y int, m time.Month) (float64, int) {
	prefix := fmt.Sprintf("%04d-%02d-", y, int(m))
	d.mu.Lock()
	defer d.mu.Unlock()
	var sum float64
	days := 0
	for day, v := range d.m[id] {
		if strings.HasPrefix(day, prefix) {
			sum += v
			days++
		}
	}
	return sum, days
}

// Flush menulis ke disk bila ada perubahan; hari yang lebih tua dari ~400 hari dibuang.
func (d *Daily) Flush(now time.Time, loc *time.Location) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.dirty {
		return nil
	}
	cut := now.In(loc).AddDate(0, 0, -dailyKeepDays).Format("2006-01-02")
	var lines []string
	for id, days := range d.m {
		for day, v := range days {
			if day < cut {
				delete(days, day)
				continue
			}
			lines = append(lines, fmt.Sprintf("%s,%s,%.3f", day, id, v))
		}
	}
	sort.Strings(lines)
	if err := os.MkdirAll(filepath.Dir(d.path), 0o755); err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, d.path); err != nil {
		return err
	}
	d.dirty = false
	return nil
}

/* ---------------- perbandingan: hari ini / minggu ini / bulan ini vs sebelumnya ---------------- */

// DailyRange menjumlahkan kWh terukur satu perangkat pada tanggal fromDay..toDay (inklusif, "YYYY-MM-DD")
// dan berapa hari yang tercatat.
type DailyRange func(id, fromDay, toDay string) (kwh float64, days int)

// CmpDev = hasil satu perangkat untuk tiap periode.
type CmpDev struct {
	Basis   string              `json:"basis"`
	Periods map[string]MonthDev `json:"periods"`
}

// CompareReport berisi periode: today, yesterday, week, last_week, month, last_month.
// Minggu dimulai Senin. Periode berjalan dihitung sampai sekarang; periode sebelumnya penuh.
type CompareReport struct {
	Tariff  float64           `json:"tariff"`
	WeekOf  string            `json:"week_start"` // tanggal Senin minggu ini
	Total   map[string]Period `json:"total"`
	Devices map[string]CmpDev `json:"devices"`
}

var ComparePeriods = []string{"today", "yesterday", "week", "last_week", "month", "last_month"}

func Compare(devs []Dev, secs Seconds, daily DailyRange, meter Meter, tariff float64, now time.Time, loc *time.Location) CompareReport {
	if loc == nil {
		loc = time.Local
	}
	ln := now.In(loc)
	y, mo, d := ln.Date()
	day0 := time.Date(y, mo, d, 0, 0, 0, 0, loc)
	weekStart := day0.AddDate(0, 0, -((int(ln.Weekday()) + 6) % 7))
	monthStart := time.Date(y, mo, 1, 0, 0, 0, 0, loc)
	type rng struct{ from, to time.Time } // to eksklusif
	R := map[string]rng{
		"today":      {day0, now},
		"yesterday":  {day0.AddDate(0, 0, -1), day0},
		"week":       {weekStart, now},
		"last_week":  {weekStart.AddDate(0, 0, -7), weekStart},
		"month":      {monthStart, now},
		"last_month": {monthStart.AddDate(0, -1, 0), monthStart},
	}
	const f = "2006-01-02"
	rep := CompareReport{Tariff: tariff, WeekOf: weekStart.Format(f), Total: map[string]Period{}, Devices: map[string]CmpDev{}}
	for _, name := range ComparePeriods {
		rep.Total[name] = Period{}
	}
	for _, dv := range devs {
		cd := CmpDev{Basis: None, Periods: map[string]MonthDev{}}
		for _, name := range ComparePeriods {
			r := R[name]
			md := MonthDev{Basis: None}
			switch {
			case dv.Metered:
				md.Basis, cd.Basis = Measured, Measured
				var kwh float64
				if daily != nil {
					last := r.to.Add(-time.Second) // hari terakhir yang tercakup
					kwh, md.Days = daily(dv.ID, r.from.Format(f), last.Format(f))
				}
				md.Known = md.Days > 0
				if meter != nil && (name == "today" || name == "month") {
					day, month := meter(dv.ID)
					src := day
					if name == "month" {
						src = month
					}
					if src != nil && *src > kwh {
						kwh, md.Known = *src, true
					}
				}
				md.KWh = round(kwh, 3)
			case dv.Watt > 0 && secs != nil:
				md.Basis, cd.Basis = Estimate, Estimate
				if on, off, _, ok := secs(dv.ID, r.from.Unix(), r.to.Unix()); ok {
					md.KWh = round((float64(on)*dv.Watt+float64(on+off)*dv.StandbyW)/3.6e6, 3)
					md.UnknownSec = (r.to.Unix() - r.from.Unix()) - (on + off)
					md.Known = on+off > 0
				}
			}
			md.Cost = round(md.KWh*tariff, 0)
			cd.Periods[name] = md
			if md.Known {
				t := rep.Total[name]
				t.KWh += md.KWh
				t.UnknownSec += md.UnknownSec
				rep.Total[name] = t
			}
		}
		rep.Devices[dv.ID] = cd
	}
	for name, t := range rep.Total {
		t.KWh = round(t.KWh, 3)
		t.Cost = round(t.KWh*tariff, 0)
		rep.Total[name] = t
	}
	return rep
}

// Range menjumlahkan kWh tercatat pada tanggal fromDay..toDay (inklusif).
func (d *Daily) Range(id, fromDay, toDay string) (float64, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var sum float64
	days := 0
	for day, v := range d.m[id] {
		if day >= fromDay && day <= toDay {
			sum += v
			days++
		}
	}
	return sum, days
}

// Fill mengisi hari yang belum tercatat (atau lebih kecil) dari sumber lain, mis. rekap menit POWR.
func (d *Daily) Fill(id string, perDay map[string]float64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for day, v := range perDay {
		if v > 0 {
			d.put(id, day, v)
		}
	}
}

// MergeFrom menyalin hari dari o ke d (nilai terbesar menang); mengembalikan jumlah hari yang berubah.
func (d *Daily) MergeFrom(o *Daily) int {
	o.mu.Lock()
	src := map[string]map[string]float64{}
	for id, days := range o.m {
		c := map[string]float64{}
		for k, v := range days {
			c[k] = v
		}
		src[id] = c
	}
	o.mu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for id, days := range src {
		for day, v := range days {
			if cur, ok := d.m[id][day]; !ok || v > cur {
				d.put(id, day, v)
				n++
			}
		}
	}
	return n
}
