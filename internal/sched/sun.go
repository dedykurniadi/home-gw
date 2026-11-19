package sched

import (
	"math"
	"time"
)

// sunTimes menghitung waktu terbit dan terbenam matahari pada tanggal kalender `day`
// (di zona waktu day.Location()) untuk koordinat lat/lon (derajat, timur positif).
// ok=false bila matahari tidak terbit/terbenam hari itu (wilayah kutub).
// Rumus "sunrise equation"; akurasi sekitar 1-2 menit, cukup untuk penjadwalan lampu.
func sunTimes(day time.Time, lat, lon float64) (rise, set time.Time, ok bool) {
	y, m, d := day.Date()
	noon := time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
	jd := float64(noon.Unix())/86400 + 2440587.5
	n := math.Round(jd - 2451545.0)
	js := n - lon/360
	rad := math.Pi / 180
	M := math.Mod(357.5291+0.98560028*js, 360)
	C := 1.9148*math.Sin(M*rad) + 0.02*math.Sin(2*M*rad) + 0.0003*math.Sin(3*M*rad)
	lam := math.Mod(M+C+180+102.9372, 360)
	jt := 2451545.0 + js + 0.0053*math.Sin(M*rad) - 0.0069*math.Sin(2*lam*rad)
	sinD := math.Sin(lam*rad) * math.Sin(23.4397*rad)
	cosD := math.Cos(math.Asin(sinD))
	cosW := (math.Sin(-0.833*rad) - math.Sin(lat*rad)*sinD) / (math.Cos(lat*rad) * cosD)
	if cosW < -1 || cosW > 1 {
		return
	}
	w := math.Acos(cosW) / rad
	conv := func(j float64) time.Time {
		return time.Unix(int64(math.Round((j-2440587.5)*86400)), 0).In(day.Location())
	}
	return conv(jt - w/360), conv(jt + w/360), true
}
