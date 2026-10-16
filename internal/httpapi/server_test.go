package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthAndUnknownAPI(t *testing.T) {
	handler := Handler()
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/healthz", http.StatusOK},
		{"/api/status", http.StatusOK},
		{"/api/missing", http.StatusNotFound},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
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
