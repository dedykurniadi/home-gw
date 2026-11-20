package main

import (
	"sort"
	"time"

	"homegw/internal/onoff"
	"homegw/internal/powerlog"
)

// syncEvents menggabungkan kejadian ON/OFF dan daya (>= since), memotong pada limit kejadian tertua,
// supaya klien bisa menarik bertahap: ulangi dengan since = next selama more=true.
func syncEvents(olog *onoff.Log, plog *powerlog.Registry, since int64, limit int) any {
	type item struct {
		t     int64
		on    *onoff.SyncEvent
		power *powerlog.SyncEvent
	}
	var all []item
	if olog != nil {
		evs := olog.EventsSince(since)
		for i := range evs {
			all = append(all, item{t: evs[i].T, on: &evs[i]})
		}
	}
	if plog != nil {
		evs := plog.EventsSince(since)
		for i := range evs {
			all = append(all, item{t: evs[i].T, power: &evs[i]})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].t < all[j].t })
	more := false
	if len(all) > limit {
		// jangan memotong di tengah detik yang sama: sertakan semua kejadian dengan t terakhir yang sama
		cut := limit
		for cut < len(all) && all[cut].t == all[cut-1].t {
			cut++
		}
		more = cut < len(all)
		all = all[:cut]
	}
	res := struct {
		Now   int64                `json:"now"`
		More  bool                 `json:"more"`
		Next  int64                `json:"next"` // since berikutnya bila more=true (inklusif; klien memakai upsert)
		OnOff []onoff.SyncEvent    `json:"onoff"`
		Power []powerlog.SyncEvent `json:"power"`
	}{Now: time.Now().Unix(), More: more, OnOff: []onoff.SyncEvent{}, Power: []powerlog.SyncEvent{}}
	for _, it := range all {
		if it.on != nil {
			res.OnOff = append(res.OnOff, *it.on)
		} else {
			res.Power = append(res.Power, *it.power)
		}
		res.Next = it.t
	}
	return res
}
