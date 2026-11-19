// Paket web menyajikan frontend (ter-embed), state JSON, aliran SSE, dan kontrol IoT.
package web

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"owrt-dashboard/internal/sonoff"
)

//go:embed static
var staticFS embed.FS

// ErrInvalid dikembalikan Handlers.Configure bila nilai pengaturan ditolak (pesan dipakai apa adanya).
var ErrInvalid = errors.New("permintaan tidak valid")

// ConfigReq: kolom yang tidak dikirim (nil) tidak diubah.
type ConfigReq struct {
	Startup    *string `json:"startup"`
	Pulse      *string `json:"pulse"`
	PulseWidth *int    `json:"pulse_width"`
	LED        *string `json:"led"`
}

// ErrNotFound dikembalikan Handlers.Control untuk id perangkat yang tidak ada.
var ErrNotFound = errors.New("tidak ditemukan")

type Handlers struct {
	// Net (opsional): tabel client jaringan. nil = tanpa jaringan.
	Net func() any
	IoT func() any
	// Sched (opsional): penjadwalan otomatis, dilayani di /api/sched.
	Sched   http.Handler
	Control func(id, cmd string) error
	// Configure (opsional): ubah pengaturan perangkat (startup, inching, LED).
	Configure func(id string, r ConfigReq) error
	// OnOffHistory (opsional): riwayat ON/OFF; ok=false bila perangkat tidak punya riwayat.
	OnOffHistory func(id string, hours int) (any, bool)
	// Energy (opsional): estimasi/ukuran pemakaian listrik dan biaya per perangkat.
	Energy func() any
	// SetEnergyCfg (opsional): ubah watt beban, konsumsi modul Sonoff dan catatan satu perangkat.
	SetEnergyCfg func(id string, watt, standby float64, note string) error
	// EnergyMonthly (opsional): rekap n bulan terakhir (kWh dan Rp per perangkat), n=1..12.
	EnergyMonthly func(n int) any
	// EnergyCompare (opsional): hari ini/minggu ini/bulan ini dibanding periode sebelumnya, per perangkat.
	EnergyCompare func() any
	// Power (opsional): status ringkas log daya per perangkat.
	Power func() any
	// PowerHistory (opsional): data grafik untuk satu perangkat; ok=false bila tidak ada log-nya.
	PowerHistory func(id string, hours int) (any, bool)

	// DiscoveredDevices (opsional): daftar perangkat mDNS yang ditemukan di LAN tapi belum diadopsi.
	DiscoveredDevices func() any
	// Discover (opsional): picu query mDNS baru untuk mencari perangkat.
	Discover func()
	// AddDevice (opsional): simpan/adopsi perangkat baru ke database dan manager.
	AddDevice func(d sonoff.Device) error
	// DeleteDevice (opsional): hapus perangkat dari database dan manager.
	DeleteDevice func(id string) error
	// Devices (opsional): daftar konfigurasi perangkat tersimpan.
	Devices func() any
}

type event struct {
	name string
	data []byte
}

// Hub menyebarkan event ke semua klien SSE. Tiap event membawa state lengkap,
// jadi event lama yang terbuang saat klien lambat tidak masalah.
type Hub struct {
	mu   sync.Mutex
	subs map[chan event]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[chan event]struct{}{}} }

func (h *Hub) subscribe() chan event {
	ch := make(chan event, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) unsubscribe(ch chan event) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *Hub) Publish(name string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	ev := event{name, data}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
			select { // penuh: buang yang terlama
			case <-ch:
			default:
			}
			select {
			case ch <- ev:
			default:
			}
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func New(h Handlers, hub *Hub) http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(staticFS, "static")
	files := http.FileServer(http.FS(sub))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	}))

	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		out := map[string]any{"iot": h.IoT()}
		if h.Net != nil {
			out["clients"] = h.Net()
		}
		if h.Power != nil {
			out["power"] = h.Power()
		}
		writeJSON(w, 200, out)
	})

	mux.HandleFunc("/api/power/", func(w http.ResponseWriter, r *http.Request) {
		if h.PowerHistory == nil {
			writeJSON(w, 404, map[string]string{"error": "log daya nonaktif"})
			return
		}
		hours := 24
		if v := r.URL.Query().Get("hours"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 48 {
				writeJSON(w, 400, map[string]string{"error": "hours harus 1-48"})
				return
			}
			hours = n
		}
		data, ok := h.PowerHistory(strings.TrimPrefix(r.URL.Path, "/api/power/"), hours)
		if !ok {
			writeJSON(w, 404, map[string]string{"error": "perangkat tidak punya log daya"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, data)
	})

	if h.EnergyCompare != nil {
		mux.HandleFunc("/api/energy/compare", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 200, h.EnergyCompare())
		})
	}
	if h.EnergyMonthly != nil {
		mux.HandleFunc("/api/energy/monthly", func(w http.ResponseWriter, r *http.Request) {
			n, err := strconv.Atoi(r.URL.Query().Get("months"))
			if err != nil || n < 1 {
				n = 6
			}
			if n > 12 {
				n = 12
			}
			writeJSON(w, 200, h.EnergyMonthly(n))
		})
	}
	mux.HandleFunc("/api/energy/cfg/", func(w http.ResponseWriter, r *http.Request) {
		if h.SetEnergyCfg == nil {
			writeJSON(w, 404, map[string]string{"error": "pengaturan energi nonaktif"})
			return
		}
		if r.Method != http.MethodPut || r.Header.Get("X-Dashboard") != "1" {
			writeJSON(w, 403, map[string]string{"error": "PUT dengan header X-Dashboard diperlukan"})
			return
		}
		var req struct {
			Watt     float64 `json:"watt"`
			StandbyW float64 `json:"standby_watt"`
			Note     string  `json:"note"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, 400, map[string]string{"error": "JSON tidak valid"})
			return
		}
		switch err := h.SetEnergyCfg(strings.TrimPrefix(r.URL.Path, "/api/energy/cfg/"), req.Watt, req.StandbyW, req.Note); {
		case err == nil:
			writeJSON(w, 200, map[string]string{"ok": "true"})
		case errors.Is(err, ErrNotFound):
			writeJSON(w, 404, map[string]string{"error": "perangkat tidak ada"})
		case errors.Is(err, ErrInvalid):
			writeJSON(w, 400, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, 500, map[string]string{"error": err.Error()})
		}
	})
	mux.HandleFunc("/api/energy", func(w http.ResponseWriter, r *http.Request) {
		if h.Energy == nil {
			writeJSON(w, 404, map[string]string{"error": "estimasi listrik nonaktif"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, h.Energy())
	})

	mux.HandleFunc("/api/onoff/", func(w http.ResponseWriter, r *http.Request) {
		if h.OnOffHistory == nil {
			writeJSON(w, 404, map[string]string{"error": "riwayat ON/OFF nonaktif"})
			return
		}
		hours := 24
		if v := r.URL.Query().Get("hours"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 800 {
				writeJSON(w, 400, map[string]string{"error": "hours harus 1-800"})
				return
			}
			hours = n
		}
		data, ok := h.OnOffHistory(strings.TrimPrefix(r.URL.Path, "/api/onoff/"), hours)
		if !ok {
			writeJSON(w, 404, map[string]string{"error": "belum ada riwayat untuk perangkat ini"})
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, data)
	})

	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming tidak didukung", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		ch := hub.subscribe()
		defer hub.unsubscribe(ch)
		write := func(name string, data []byte) error {
			_, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
			fl.Flush()
			return err
		}
		initial := map[string]func() any{"iot": h.IoT}
		if h.Net != nil {
			initial["net"] = h.Net
		}
		if h.Power != nil {
			initial["power"] = h.Power
		}
		for name, get := range initial {
			data, _ := json.Marshal(get())
			if write(name, data) != nil {
				return
			}
		}
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case ev := <-ch:
				if write(ev.name, ev.data) != nil {
					return
				}
			case <-tick.C:
				fmt.Fprint(w, ": ping\n\n")
				fl.Flush()
			}
		}
	})

	if h.Sched != nil {
		mux.Handle("/api/sched", h.Sched)
		mux.Handle("/api/sched/", h.Sched)
	}

	mux.HandleFunc("/api/iot/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeJSON(w, 405, map[string]string{"error": "gunakan POST"})
			return
		}
		// Header kustom memicu preflight CORS, sehingga halaman web lain tidak bisa
		// menekan tombol dari browser pengguna (CSRF).
		if r.Header.Get("X-Dashboard") != "1" {
			writeJSON(w, 403, map[string]string{"error": "header X-Dashboard diperlukan"})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/iot/")
		if strings.HasSuffix(id, "/config") {
			id = strings.TrimSuffix(id, "/config")
			if h.Configure == nil {
				writeJSON(w, 404, map[string]string{"error": "pengaturan nonaktif"})
				return
			}
			var req ConfigReq
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				writeJSON(w, 400, map[string]string{"error": "JSON tidak valid"})
				return
			}
			switch err := h.Configure(id, req); {
			case err == nil:
				writeJSON(w, 200, map[string]string{"ok": "true"})
			case errors.Is(err, ErrNotFound):
				writeJSON(w, 404, map[string]string{"error": "perangkat tidak ada"})
			case errors.Is(err, ErrInvalid):
				writeJSON(w, 400, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, 502, map[string]string{"error": err.Error()})
			}
			return
		}
		var body struct {
			Cmd string `json:"cmd"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
			writeJSON(w, 400, map[string]string{"error": "JSON tidak valid"})
			return
		}
		if body.Cmd != "on" && body.Cmd != "off" && body.Cmd != "toggle" {
			writeJSON(w, 400, map[string]string{"error": "cmd harus on, off, atau toggle"})
			return
		}
		switch err := h.Control(id, body.Cmd); {
		case err == nil:
			writeJSON(w, 200, map[string]string{"ok": "true"})
		case errors.Is(err, ErrNotFound):
			writeJSON(w, 404, map[string]string{"error": "perangkat tidak ada"})
		default:
			writeJSON(w, 502, map[string]string{"error": err.Error()})
		}
	})

	if h.DiscoveredDevices != nil {
		mux.HandleFunc("/api/devices/discovered", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 200, h.DiscoveredDevices())
		})
	}
	if h.Discover != nil {
		mux.HandleFunc("/api/devices/discover", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.Header.Get("X-Dashboard") != "1" {
				writeJSON(w, 403, map[string]string{"error": "POST dengan header X-Dashboard diperlukan"})
				return
			}
			h.Discover()
			writeJSON(w, 200, map[string]string{"ok": "true"})
		})
	}
	mux.HandleFunc("/api/devices", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if h.Devices != nil {
				w.Header().Set("Cache-Control", "no-store")
				writeJSON(w, 200, h.Devices())
				return
			}
			writeJSON(w, 404, map[string]string{"error": "tidak didukung"})
		case http.MethodPost:
			if h.AddDevice == nil {
				writeJSON(w, 404, map[string]string{"error": "tambah perangkat nonaktif"})
				return
			}
			if r.Header.Get("X-Dashboard") != "1" {
				writeJSON(w, 403, map[string]string{"error": "POST dengan header X-Dashboard diperlukan"})
				return
			}
			var d sonoff.Device
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&d); err != nil {
				writeJSON(w, 400, map[string]string{"error": "JSON tidak valid"})
				return
			}
			d.ID = strings.TrimSpace(d.ID)
			d.Name = strings.TrimSpace(d.Name)
			d.Key = strings.TrimSpace(d.Key)
			if d.ID == "" {
				writeJSON(w, 400, map[string]string{"error": "ID perangkat wajib diisi"})
				return
			}
			if d.Name == "" {
				writeJSON(w, 400, map[string]string{"error": "Nama perangkat wajib diisi"})
				return
			}
			if d.Key == "" {
				writeJSON(w, 400, map[string]string{"error": "Device Key wajib diisi"})
				return
			}
			if d.Kind == "" {
				d.Kind = "switch"
			}
			if err := h.AddDevice(d); err != nil {
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "device": d})
		default:
			w.Header().Set("Allow", "GET, POST")
			writeJSON(w, 405, map[string]string{"error": "metode tidak diizinkan"})
		}
	})
	mux.HandleFunc("/api/devices/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.Header().Set("Allow", "DELETE")
			writeJSON(w, 405, map[string]string{"error": "gunakan DELETE"})
			return
		}
		if r.Header.Get("X-Dashboard") != "1" {
			writeJSON(w, 403, map[string]string{"error": "DELETE dengan header X-Dashboard diperlukan"})
			return
		}
		if h.DeleteDevice == nil {
			writeJSON(w, 404, map[string]string{"error": "hapus perangkat nonaktif"})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/devices/")
		if id == "" {
			writeJSON(w, 400, map[string]string{"error": "ID perangkat tidak valid"})
			return
		}
		if err := h.DeleteDevice(id); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]string{"ok": "true"})
	})
	return mux
}
