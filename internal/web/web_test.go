package web

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"owrt-dashboard/internal/sonoff"
)

func testServer(t *testing.T, ctl func(id, cmd string) error) (*httptest.Server, *Hub) {
	hub := NewHub()
	srv := httptest.NewServer(New(Handlers{
		Net:     func() any { return []map[string]string{{"ip": "10.0.0.1"}} },
		IoT:     func() any { return []map[string]string{{"id": "a"}} },
		Control: ctl,
	}, hub))
	t.Cleanup(srv.Close)
	return srv, hub
}

func post(t *testing.T, url, body string, hdr bool) int {
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	if hdr {
		req.Header.Set("X-Dashboard", "1")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestControl(t *testing.T) {
	var gotID, gotCmd string
	srv, _ := testServer(t, func(id, cmd string) error {
		gotID, gotCmd = id, cmd
		switch id {
		case "hilang":
			return ErrNotFound
		case "rusak":
			return errors.New("timeout")
		}
		return nil
	})
	u := srv.URL + "/api/iot/"
	if c := post(t, u+"a", `{"cmd":"on"}`, false); c != 403 {
		t.Fatalf("tanpa header harus 403, dapat %d", c)
	}
	if gotID != "" {
		t.Fatal("kontrol tidak boleh terpanggil tanpa header")
	}
	if c := post(t, u+"a", `{"cmd":"on"}`, true); c != 200 || gotID != "a" || gotCmd != "on" {
		t.Fatalf("normal: %d %s %s", c, gotID, gotCmd)
	}
	if c := post(t, u+"a", `{"cmd":"hapus"}`, true); c != 400 {
		t.Fatalf("cmd tidak valid harus 400, dapat %d", c)
	}
	if c := post(t, u+"a", `bukan json`, true); c != 400 {
		t.Fatalf("json rusak harus 400, dapat %d", c)
	}
	if c := post(t, u+"hilang", `{"cmd":"on"}`, true); c != 404 {
		t.Fatalf("harus 404, dapat %d", c)
	}
	if c := post(t, u+"rusak", `{"cmd":"on"}`, true); c != 502 {
		t.Fatalf("harus 502, dapat %d", c)
	}
	resp, _ := http.Get(u + "a")
	resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Fatalf("GET harus 405, dapat %d", resp.StatusCode)
	}
}

func TestStateAndSSE(t *testing.T) {
	srv, hub := testServer(t, nil)
	resp, err := http.Get(srv.URL + "/api/state")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("state: %v", err)
	}
	resp.Body.Close()

	resp, err = http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type: %s", ct)
	}
	lines := make(chan string, 32)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	want := map[string]bool{"event: net": false, "event: iot": false}
	deadline := time.After(2 * time.Second)
	for !(want["event: net"] && want["event: iot"]) {
		select {
		case l := <-lines:
			if _, ok := want[l]; ok {
				want[l] = true
			}
		case <-deadline:
			t.Fatalf("event awal tidak lengkap: %v", want)
		}
	}
	hub.Publish("iot", []string{"baru"})
	for {
		select {
		case l := <-lines:
			if strings.Contains(l, `"baru"`) {
				return
			}
		case <-deadline:
			t.Fatal("event publish tidak sampai")
		}
	}
}

func TestWebSocket(t *testing.T) {
	var mu sync.Mutex
	var gotID, gotCmd string
	srv, hub := testServer(t, func(id, cmd string) error {
		mu.Lock()
		gotID, gotCmd = id, cmd
		mu.Unlock()
		if id == "rusak" {
			return errors.New("perangkat rusak")
		}
		return nil
	})

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer ws.Close()

	type msg struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}

	msgs := make(chan msg, 32)
	go func() {
		for {
			var m msg
			if err := ws.ReadJSON(&m); err != nil {
				return
			}
			msgs <- m
		}
	}()

	want := map[string]bool{"net": false, "iot": false}
	deadline := time.After(2 * time.Second)
	for !(want["net"] && want["iot"]) {
		select {
		case m := <-msgs:
			if _, ok := want[m.Type]; ok {
				want[m.Type] = true
			}
		case <-deadline:
			t.Fatalf("pesan awal websocket tidak lengkap: %v", want)
		}
	}

	// Test broadcast
	hub.Publish("iot", []string{"ws-baru"})
	received := false
	for !received {
		select {
		case m := <-msgs:
			if m.Type == "iot" && strings.Contains(string(m.Data), "ws-baru") {
				received = true
			}
		case <-deadline:
			t.Fatal("publish via websocket tidak sampai")
		}
	}

	// Test action control
	if err := ws.WriteJSON(map[string]string{"action": "control", "id": "test1", "cmd": "on"}); err != nil {
		t.Fatalf("kirim action control: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if gotID != "test1" || gotCmd != "on" {
		t.Fatalf("kontrol tidak terpanggil dengan benar: id=%s cmd=%s", gotID, gotCmd)
	}
	mu.Unlock()

	// Test action toggle
	if err := ws.WriteJSON(map[string]string{"action": "toggle", "id": "test2"}); err != nil {
		t.Fatalf("kirim action toggle: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if gotID != "test2" || gotCmd != "toggle" {
		t.Fatalf("toggle tidak terpanggil dengan benar: id=%s cmd=%s", gotID, gotCmd)
	}
	mu.Unlock()

	// Test error response
	if err := ws.WriteJSON(map[string]string{"action": "control", "id": "rusak", "cmd": "off"}); err != nil {
		t.Fatalf("kirim action rusak: %v", err)
	}
	gotErr := false
	for !gotErr {
		select {
		case m := <-msgs:
			if m.Type == "error" && strings.Contains(string(m.Data), "perangkat rusak") {
				gotErr = true
			}
		case <-deadline:
			t.Fatal("error response tidak diterima via websocket")
		}
	}
}

func TestStatic(t *testing.T) {
	srv, _ := testServer(t, nil)
	resp, err := http.Get(srv.URL + "/")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("index: %v %v", err, resp)
	}
	resp.Body.Close()
	if resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatal("index harus no-cache")
	}
}

func TestPowerHistoryEndpoint(t *testing.T) {
	var gotID string
	var gotHours int
	srv := httptest.NewServer(New(Handlers{
		Net: func() any { return []int{} }, IoT: func() any { return []int{} },
		Power: func() any { return map[string]int{"ac": 1} },
		PowerHistory: func(id string, h int) (any, bool) {
			gotID, gotHours = id, h
			return map[string]string{"ok": "1"}, id == "ac"
		},
	}, NewHub()))
	t.Cleanup(srv.Close)
	get := func(path string) int {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := get("/api/power/ac"); c != 200 || gotID != "ac" || gotHours != 24 {
		t.Fatalf("default: %d %q %d", c, gotID, gotHours)
	}
	if c := get("/api/power/ac?hours=6"); c != 200 || gotHours != 6 {
		t.Fatalf("hours=6: %d %d", c, gotHours)
	}
	for _, p := range []string{"/api/power/ac?hours=0", "/api/power/ac?hours=99", "/api/power/ac?hours=x"} {
		if c := get(p); c != 400 {
			t.Fatalf("%s: %d", p, c)
		}
	}
	if c := get("/api/power/lain"); c != 404 {
		t.Fatalf("id tak dikenal: %d", c)
	}
}

func TestConfigEndpoint(t *testing.T) {
	var got ConfigReq
	var gotID string
	hub := NewHub()
	srv := httptest.NewServer(New(Handlers{
		Net: func() any { return nil }, IoT: func() any { return nil },
		Control: func(string, string) error { return nil },
		Configure: func(id string, r ConfigReq) error {
			gotID, got = id, r
			switch id {
			case "hilang":
				return ErrNotFound
			case "salah":
				return errors.New("permintaan tidak valid: startup harus on, off, atau stay")
			case "tolak":
				return ErrInvalid
			}
			return nil
		},
	}, hub))
	defer srv.Close()
	u := srv.URL + "/api/iot/"
	if c := post(t, u+"a/config", `{"led":"off"}`, false); c != 403 {
		t.Fatalf("tanpa header harus 403, dapat %d", c)
	}
	if c := post(t, u+"a/config", `{"led":"off","pulse_width":2000}`, true); c != 200 || gotID != "a" || got.LED == nil || *got.LED != "off" || got.PulseWidth == nil || *got.PulseWidth != 2000 || got.Startup != nil {
		t.Fatalf("normal: %d %s %+v", c, gotID, got)
	}
	if c := post(t, u+"a/config", `{"bukan_field":1}`, true); c != 400 {
		t.Fatalf("field asing harus 400, dapat %d", c)
	}
	if c := post(t, u+"a/config", `{`, true); c != 400 {
		t.Fatalf("JSON rusak harus 400, dapat %d", c)
	}
	if c := post(t, u+"hilang/config", `{"led":"on"}`, true); c != 404 {
		t.Fatalf("id tak ada harus 404, dapat %d", c)
	}
	if c := post(t, u+"tolak/config", `{"led":"on"}`, true); c != 400 {
		t.Fatalf("ErrInvalid harus 400, dapat %d", c)
	}
	if c := post(t, u+"salah/config", `{"led":"on"}`, true); c != 502 {
		t.Fatalf("error lain harus 502, dapat %d", c)
	}
}

func TestOnOffEndpoint(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(New(Handlers{
		Net: func() any { return nil }, IoT: func() any { return nil },
		Control: func(string, string) error { return nil },
		OnOffHistory: func(id string, hours int) (any, bool) {
			if id != "a" {
				return nil, false
			}
			return map[string]int{"hours": hours}, true
		},
	}, hub))
	defer srv.Close()
	get := func(p string) (int, string) {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b := make([]byte, 200)
		n, _ := resp.Body.Read(b)
		return resp.StatusCode, string(b[:n])
	}
	if c, b := get("/api/onoff/a?hours=72"); c != 200 || !strings.Contains(b, `"hours":72`) {
		t.Fatalf("%d %s", c, b)
	}
	if c, _ := get("/api/onoff/a"); c != 200 {
		t.Fatalf("default %d", c)
	}
	if c, _ := get("/api/onoff/a?hours=0"); c != 400 {
		t.Fatalf("hours=0 harus 400, dapat %d", c)
	}
	if c, _ := get("/api/onoff/a?hours=801"); c != 400 {
		t.Fatalf("hours=801 harus 400, dapat %d", c)
	}
	if c, _ := get("/api/onoff/x"); c != 404 {
		t.Fatalf("id tak ada harus 404, dapat %d", c)
	}
}

func TestEnergyEndpoint(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(New(Handlers{
		Net: func() any { return []int{} }, IoT: func() any { return []int{} },
		Control: func(string, string) error { return nil },
		Energy:  func() any { return map[string]any{"tariff": 1500} },
	}, hub))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/api/energy")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("energy: %v %v", err, resp)
	}
	resp.Body.Close()
	srv2, _ := testServer(t, func(string, string) error { return nil })
	r2, _ := http.Get(srv2.URL + "/api/energy")
	if r2.StatusCode != 404 {
		t.Fatalf("tanpa Energy harus 404, dapat %d", r2.StatusCode)
	}
	r2.Body.Close()
}

func TestDeviceEndpoints(t *testing.T) {
	var added sonoff.Device
	var deleted string
	var discoveredCalls int

	hub := NewHub()
	srv := httptest.NewServer(New(Handlers{
		DiscoveredDevices: func() any {
			return []sonoff.Discovered{{ID: "disc1", IP: "10.0.0.2"}}
		},
		Discover: func() {
			discoveredCalls++
		},
		AddDevice: func(d sonoff.Device) error {
			added = d
			return nil
		},
		DeleteDevice: func(id string) error {
			deleted = id
			return nil
		},
		Devices: func() any {
			return []sonoff.Device{{ID: "dev1", Name: "D1"}}
		},
	}, hub))
	defer srv.Close()

	// GET /api/devices/discovered
	resp, err := http.Get(srv.URL + "/api/devices/discovered")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("discovered: %v %v", err, resp)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "disc1") {
		t.Fatalf("discovered harus memuat disc1: %s", b)
	}

	// POST /api/devices/discover tanpa header
	if st := post(t, srv.URL+"/api/devices/discover", "", false); st != 403 {
		t.Fatalf("discover tanpa header harus 403, dapat %d", st)
	}
	// POST /api/devices/discover dengan header
	if st := post(t, srv.URL+"/api/devices/discover", "", true); st != 200 || discoveredCalls != 1 {
		t.Fatalf("discover gagal: st=%d calls=%d", st, discoveredCalls)
	}

	// POST /api/devices untuk tambah / adopsi perangkat
	body := `{"id":"1000c47454","name":"Lampu Teras","key":"1234567890abcdef1234567890abcdef","kind":"switch"}`
	if st := post(t, srv.URL+"/api/devices", body, false); st != 403 {
		t.Fatalf("POST devices tanpa header harus 403, dapat %d", st)
	}
	if st := post(t, srv.URL+"/api/devices", body, true); st != 200 || added.ID != "1000c47454" || added.Name != "Lampu Teras" {
		t.Fatalf("POST devices gagal: st=%d added=%+v", st, added)
	}

	// DELETE /api/devices/{id}
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/devices/1000c47454", nil)
	req.Header.Set("X-Dashboard", "1")
	delResp, err := http.DefaultClient.Do(req)
	if err != nil || delResp.StatusCode != 200 || deleted != "1000c47454" {
		t.Fatalf("DELETE devices gagal: %v st=%v deleted=%s", err, delResp, deleted)
	}
	delResp.Body.Close()
}
