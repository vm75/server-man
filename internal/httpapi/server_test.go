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
	"time"

	"server-man/internal/app"
	"server-man/internal/auth"
	"server-man/internal/config"
	"server-man/internal/core"
	"server-man/internal/modules/monitor"
)

func TestHealthAndUnknownAPI(t *testing.T) {
	handler, token := testHandler(t, false)
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/healthz", http.StatusOK},
		{"/api/status", http.StatusOK},
		{"/api/missing", http.StatusNotFound},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080"+tc.path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: status %d", tc.path, w.Code)
		}
		if !json.Valid(w.Body.Bytes()) {
			t.Fatalf("%s: expected JSON", tc.path)
		}
		if w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("missing security header")
		}
	}
}

func TestDevelopmentHandlerRetainsAPIBoundaries(t *testing.T) {
	const origin = "http://192.0.2.10:8181"
	path := filepath.Join(t.TempDir(), "token")
	authentication, err := auth.Load(path, origin)
	if err != nil {
		t.Fatal(err)
	}
	token, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	handler := DevelopmentHandler(authentication, core.NewRegistry(false, nil))
	for _, tc := range []struct {
		name, path, host, origin string
		status                   int
	}{
		{"native health", "/healthz", "192.0.2.10:8181", "", http.StatusOK},
		{"API requires authentication", "/api/status", "192.0.2.10:8181", origin, http.StatusUnauthorized},
		{"MCP requires authentication", "/mcp", "192.0.2.10:8181", origin, http.StatusUnauthorized},
		{"UI rejects foreign host", "/", "foreign.example:8181", "", http.StatusForbidden},
		{"UI rejects foreign origin", "/@vite/client", "192.0.2.10:8181", "http://foreign.example", http.StatusForbidden},
		{"WebSocket rejects foreign origin", "/?token=fixture", "192.0.2.10:8181", "http://foreign.example", http.StatusForbidden},
		{"API rejects authenticated foreign origin", "/api/status", "192.0.2.10:8181", "http://foreign.example", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, origin+tc.path, nil)
			r.Host = tc.host
			r.Header.Set("Origin", tc.origin)
			if strings.HasPrefix(tc.path, "/?token=") {
				r.Header.Set("Connection", "Upgrade")
				r.Header.Set("Upgrade", "websocket")
			}
			if tc.name == "API rejects authenticated foreign origin" {
				r.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d, want=%d", w.Code, tc.status)
			}
			if w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing security headers")
			}
		})
	}
}

func testHandler(t *testing.T, allowWrites bool) (http.Handler, string) {
	return testHandlerWithMonitor(t, allowWrites, false)
}

func testHandlerWithMonitor(t *testing.T, allowWrites, includeMonitor bool) (http.Handler, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "token")
	authentication, err := auth.Load(path, "")
	if err != nil {
		t.Fatal(err)
	}
	token, _ := os.ReadFile(path)
	jobs, err := core.NewJobs(context.Background(), filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(jobs.Close)
	var monitoring *monitor.Sampler
	if includeMonitor {
		monitoring = monitor.New(context.Background(), time.Second)
		t.Cleanup(monitoring.Close)
	}
	registry, err := app.Registry(config.Config{AllowWrites: allowWrites, StateDir: root}, jobs, monitoring, core.Runner{})
	if err != nil {
		t.Fatal(err)
	}
	return Handler(authentication, registry), strings.TrimSpace(string(token))
}

func TestLiveMonitoringThroughAuthenticatedHTTP(t *testing.T) {
	handler, token := testHandlerWithMonitor(t, false, true)
	r := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/tools/monitor_snapshot", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var snapshot monitor.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !snapshot.Valid || snapshot.Memory.Total == 0 || len(snapshot.CPU.Cores) == 0 {
		t.Fatalf("invalid live HTTP response: status=%d valid=%v error=%s", w.Code, snapshot.Valid, snapshot.LastError)
	}
}

func TestSessionAuthenticationAndToolHTTPBoundary(t *testing.T) {
	handler, token := testHandler(t, false)
	for _, tc := range []struct {
		name, method, path, body, bearer, origin string
		status                                   int
	}{
		{"public health", "GET", "/healthz", "", "", "", 200},
		{"protected status", "GET", "/api/status", "", "", "", 401},
		{"incorrect bearer", "GET", "/api/status", "", "invalid", "", 401},
		{"valid bearer", "GET", "/api/status", "", token, "", 200},
		{"tool discovery", "GET", "/api/tools", "", token, "", 200},
		{"read tool", "POST", "/api/tools/jobs_list", `{}`, token, "", 200},
		{"unknown tool", "POST", "/api/tools/shell", `{}`, token, "", 404},
		{"unknown arguments", "POST", "/api/tools/system_status", `{"shell":"whoami"}`, token, "", 400},
		{"null arguments", "POST", "/api/tools/system_status", `null`, token, "", 400},
		{"writes denied", "POST", "/api/tools/jobs_cancel", `{"id":"not-a-job"}`, token, "", 403},
		{"cross origin", "POST", "/api/tools/system_status", `{}`, token, "https://evil.example", 403},
		{"malformed login", "POST", "/api/session", `{"token":"x","other":true}`, "", "", 400},
		{"bad login", "POST", "/api/session", `{"token":"incorrect"}`, "", "", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://127.0.0.1:8080"+tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			if tc.bearer != "" {
				r.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d, body=%s", w.Code, w.Body)
			}
		})
	}
	credentials, _ := json.Marshal(map[string]string{"token": token})
	r := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/session", strings.NewReader(string(credentials)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://127.0.0.1:8080")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || len(w.Result().Cookies()) != 1 {
		t.Fatalf("login failed: %d %s", w.Code, w.Body)
	}
	cookie := w.Result().Cookies()[0]
	r = httptest.NewRequest("GET", "http://127.0.0.1:8080/api/status", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("session cookie rejected")
	}
	r = httptest.NewRequest("DELETE", "http://127.0.0.1:8080/api/session", nil)
	r.AddCookie(cookie)
	handler.ServeHTTP(httptest.NewRecorder(), r)
	r = httptest.NewRequest("GET", "http://127.0.0.1:8080/api/status", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("session survived logout")
	}
}
