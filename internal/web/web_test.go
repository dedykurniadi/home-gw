package web

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestIoTOnlyMode(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(New(Handlers{
		IoT: func() any { return []int{} }, IoTOnly: true,
		Control: func(id, cmd string) error { return nil },
	}, hub))
	defer srv.Close()
	get := func(p string) string {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	idx := get("/")
	if !strings.Contains(idx, `<body class="iot-only">`) || !strings.Contains(idx, "<title>IoT Dashboard</title>") {
		t.Fatal("index harus bermode hanya-IoT")
	}
	if st := get("/api/state"); strings.Contains(st, "clients") || !strings.Contains(st, `"iot"`) {
		t.Fatalf("state tanpa clients: %s", st)
	}
	resp, err := http.Get(srv.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 256)
	n, _ := resp.Body.Read(buf)
	if strings.Contains(string(buf[:n]), "event: net") {
		t.Fatalf("SSE tidak boleh memuat net: %s", buf[:n])
	}
}
