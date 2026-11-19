package sched

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// Handler melayani /api/sched (GET daftar) dan /api/sched/{id} (PUT, DELETE); POST /api/sched membuat jadwal.
// Perubahan wajib memakai header X-HomeGW: 1 (perlindungan CSRF, sama seperti /api/iot).
func (e *Engine) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/sched"), "/")
		if r.Method != http.MethodGet && r.Header.Get("X-HomeGW") != "1" && r.Header.Get("X-Dashboard") != "1" {
			writeJSON(w, 403, map[string]string{"error": "header X-HomeGW diperlukan"})
			return
		}
		switch {
		case r.Method == http.MethodGet && id == "":
			writeJSON(w, 200, map[string]any{
				"has_location": e.hasL,
				"sun":          e.Sun(),
				"scenes":       e.Views(),
				"runs":         e.Runs(),
			})
		case (r.Method == http.MethodPost && id == "") || (r.Method == http.MethodPut && id != ""):
			var req Scene
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				writeJSON(w, 400, map[string]string{"error": "JSON tidak valid"})
				return
			}
			req.ID = id
			out, err := e.Put(req)
			switch {
			case err == nil:
				writeJSON(w, 200, out)
			case errors.Is(err, ErrInvalid):
				writeJSON(w, 400, map[string]string{"error": err.Error()})
			case errors.Is(err, os.ErrNotExist):
				writeJSON(w, 404, map[string]string{"error": "jadwal tidak ada"})
			default:
				writeJSON(w, 500, map[string]string{"error": err.Error()})
			}
		case r.Method == http.MethodDelete && id != "":
			switch err := e.Delete(id); {
			case err == nil:
				writeJSON(w, 200, map[string]string{"ok": "true"})
			case errors.Is(err, os.ErrNotExist):
				writeJSON(w, 404, map[string]string{"error": "jadwal tidak ada"})
			default:
				writeJSON(w, 500, map[string]string{"error": err.Error()})
			}
		default:
			writeJSON(w, 405, map[string]string{"error": "metode tidak didukung"})
		}
	})
}
