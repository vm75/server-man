package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"server-man/internal/app"
	"server-man/internal/auth"
	"server-man/internal/config"
	"server-man/internal/core"
)

func TestHTTPAdminSetupAndPasswordSession(t *testing.T) {
	cfg, err := config.Defaults()
	if err != nil { t.Fatal(err) }
	root := t.TempDir()
	cfg.Path = filepath.Join(root, "server-man.yaml")
	cfg.TokenFile = filepath.Join(root, "token")
	cfg.StateDir = filepath.Join(root, "state")
	jobs, err := core.NewJobs(context.Background(), cfg.StateDir)
	if err != nil { t.Fatal(err) }
	defer jobs.Close()
	a, err := auth.LoadWithConfig(cfg)
	if err != nil { t.Fatal(err) }
	registry, err := app.Registry(cfg, jobs, nil, core.Runner{}, a.AdminCredentials)
	if err != nil { t.Fatal(err) }
	handler := Handler(a, registry)
	token, err := os.ReadFile(cfg.TokenFile)
	if err != nil { t.Fatal(err) }
	call := func(method, path, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" { r.Header.Set("Origin", origin) }
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	mode := call("GET", "/api/auth/mode", "", "")
	var state struct { AdminConfigured bool `json:"adminConfigured"` }
	if mode.Code != 200 || json.Unmarshal(mode.Body.Bytes(), &state) != nil || state.AdminConfigured {
		t.Fatal("setup mode not advertised")
	}
	setup := `{"token":"wrong","username":"admin","password":"correct horse battery staple"}`
	if w := call("POST", "/api/admin/setup", setup, ""); w.Code != 401 { t.Fatal(w.Code) }
	setup = `{"token":"`+strings.TrimSpace(string(token))+`","username":"admin","password":"correct horse battery staple"}`
	if w := call("POST", "/api/admin/setup", setup, "https://evil.example"); w.Code != 403 { t.Fatal(w.Code) }
	w := call("POST", "/api/admin/setup", setup, "")
	if w.Code != 200 || len(w.Result().Cookies()) != 1 { t.Fatalf("admin setup: %d %s", w.Code, w.Body.String()) }
	cookie := w.Result().Cookies()[0]
	mode = call("GET", "/api/auth/mode", "", "")
	if json.Unmarshal(mode.Body.Bytes(), &state) != nil || !state.AdminConfigured { t.Fatal("admin setup not reflected") }
	if w := call("POST", "/api/admin/setup", setup, ""); w.Code != 409 { t.Fatal("second setup", w.Code) }
	if w := call("POST", "/api/session", `{"token":"`+strings.TrimSpace(string(token))+`"}`, ""); w.Code != 401 {
		t.Fatal("token browser sign-in should be disabled after setup", w.Code)
	}
	if w := call("POST", "/api/session", `{"username":"admin","password":"wrong"}`, ""); w.Code != 401 { t.Fatal(w.Code) }
	if w := call("POST", "/api/session", `{"username":"admin","password":"correct horse battery staple"}`, ""); w.Code != 200 { t.Fatal(w.Code, w.Body.String()) }
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/status", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 { t.Fatal("setup browser session not authorized", w.Code) }
	if err := cfg.Validate(); err != nil { t.Fatal(err) }
	loaded, err := config.Load(cfg.Path)
	if err != nil || loaded.AdminUsername != "admin" { t.Fatal("admin not written to YAML", err) }
}
