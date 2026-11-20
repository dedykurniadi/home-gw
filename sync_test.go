package main

import (
	"encoding/json"
	"testing"
)

func TestSyncEventsNil(t *testing.T) {
	res := syncEvents(nil, nil, 0, 100)
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Now   int64 `json:"now"`
		More  bool  `json:"more"`
		Next  int64 `json:"next"`
		OnOff []any `json:"onoff"`
		Power []any `json:"power"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.More {
		t.Fatal("more harus false saat log kosong")
	}
	if len(out.OnOff) != 0 || len(out.Power) != 0 {
		t.Fatalf("onoff dan power harus kosong, dapat %d dan %d", len(out.OnOff), len(out.Power))
	}
}
