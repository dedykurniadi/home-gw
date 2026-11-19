// Paket energy memperkirakan pemakaian listrik dan biaya.
//   - Perangkat dengan "watt" di devices.json (mis. Sonoff Mini + lampu LED): estimasi
//     kWh = watt x lama ON (+ standby_watt x lama perangkat terdengar). Waktu "tidak terdengar" tidak ditebak.
//   - Perangkat yang mengukur sendiri (POWR316D): memakai kWh hari ini / bulan ini dari perangkat.
package energy

import (
	"math"
	"time"
)

const (
	Estimate = "estimate" // watt x lama ON
	Measured = "measured" // diukur perangkat
	None     = "none"     // watt belum diisi
)

// Seconds: lama ON/OFF/gap pada rentang; ok=false bila perangkat belum punya riwayat.
type Seconds func(id string, from, to int64) (on, off, gap int64, ok bool)

// Meter memberi kWh hari ini dan bulan ini yang diukur perangkat (nil bila tidak ada).
type Meter func(id string) (day, month *float64)

type Dev struct {
	ID       string
	Name     string
	Watt     float64 // daya saat ON (W); 0 = belum diisi
	StandbyW float64 // konsumsi modul Sonoff sendiri (W), dihitung selama perangkat terdengar
	Note     string
	Metered  bool // perangkat mengukur sendiri
}

type Period struct {
	KWh        float64 `json:"kwh"`
	Cost       float64 `json:"cost"`        // KWh x tarif (0 bila tarif kosong)
	UnknownSec int64   `json:"unknown_sec"` // rentang yang tidak diketahui (tidak terdengar / belum ada riwayat)
}

type Result struct {
	ID       string  `json:"id"`
	Basis    string  `json:"basis"`
	Watt     float64 `json:"watt,omitempty"`
	StandbyW float64 `json:"standby_watt,omitempty"`
	Note     string  `json:"note,omitempty"`
	Today    *Period `json:"today,omitempty"`
	Week     *Period `json:"week,omitempty"`  // 7 x 24 jam terakhir
	Month    *Period `json:"month,omitempty"` // sejak tanggal 1 pukul 00:00
}

type Report struct {
	Tariff  float64           `json:"tariff"`
	Devices map[string]Result `json:"devices"`
	Total   struct {
		Today Period `json:"today"`
		Month Period `json:"month"`
		N     int    `json:"n"` // jumlah perangkat yang ikut dihitung
	} `json:"total"`
}

func round(v float64, n int) float64 {
	p := math.Pow10(n)
	return math.Round(v*p) / p
}

func period(kwh, tariff float64, unknown int64) *Period {
	return &Period{KWh: round(kwh, 3), Cost: round(kwh*tariff, 0), UnknownSec: unknown}
}

// Compute menyusun laporan pada waktu now. Hari dan bulan mengikuti zona waktu loc.
func Compute(devs []Dev, secs Seconds, meter Meter, tariff float64, now time.Time, loc *time.Location) Report {
	if loc == nil {
		loc = time.Local
	}
	ln := now.In(loc)
	dayStart := time.Date(ln.Year(), ln.Month(), ln.Day(), 0, 0, 0, 0, loc).Unix()
	monthStart := time.Date(ln.Year(), ln.Month(), 1, 0, 0, 0, 0, loc).Unix()
	weekStart := now.Add(-7 * 24 * time.Hour).Unix()
	to := now.Unix()

	rep := Report{Tariff: tariff, Devices: map[string]Result{}}
	for _, d := range devs {
		r := Result{ID: d.ID, Basis: None, Note: d.Note, Watt: d.Watt, StandbyW: d.StandbyW}
		switch {
		case d.Metered:
			r.Basis = Measured
			if meter != nil {
				day, month := meter(d.ID)
				if day != nil {
					r.Today = period(*day, tariff, 0)
				}
				if month != nil {
					r.Month = period(*month, tariff, 0)
				}
			}
		case d.Watt > 0 && secs != nil:
			r.Basis = Estimate
			est := func(from int64) *Period {
				on, off, _, ok := secs(d.ID, from, to)
				if !ok {
					return nil
				}
				kwh := (float64(on)*d.Watt + float64(on+off)*d.StandbyW) / 3.6e6
				return period(kwh, tariff, (to-from)-(on+off))
			}
			r.Today, r.Week, r.Month = est(dayStart), est(weekStart), est(monthStart)
		}
		rep.Devices[d.ID] = r
		if r.Today != nil || r.Month != nil {
			rep.Total.N++
		}
		if r.Today != nil {
			rep.Total.Today.KWh += r.Today.KWh
			rep.Total.Today.UnknownSec += r.Today.UnknownSec
		}
		if r.Month != nil {
			rep.Total.Month.KWh += r.Month.KWh
			rep.Total.Month.UnknownSec += r.Month.UnknownSec
		}
	}
	rep.Total.Today.KWh = round(rep.Total.Today.KWh, 3)
	rep.Total.Month.KWh = round(rep.Total.Month.KWh, 3)
	rep.Total.Today.Cost = round(rep.Total.Today.KWh*tariff, 0)
	rep.Total.Month.Cost = round(rep.Total.Month.KWh*tariff, 0)
	return rep
}
