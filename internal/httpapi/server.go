package httpapi

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"serverman/internal/webui"
)

var StartedAt = time.Now().UTC()

func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		hostname, _ := os.Hostname()
		JSON(w, http.StatusOK, map[string]any{"hostname": hostname, "startedAt": StartedAt, "version": "dev"})
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		JSON(w, http.StatusNotFound, map[string]string{"error": "endpoint not found"})
	})
	mux.Handle("/", webui.Handler())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		mux.ServeHTTP(w, r)
	})
}

func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
