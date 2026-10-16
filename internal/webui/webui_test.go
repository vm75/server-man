package webui

import (
	"io/fs"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedProductionUI(t *testing.T) {
	if _, err := fs.Stat(assets, "assets/dist/index.html"); err != nil {
		t.Skip("build frontend assets before verifying the production UI")
	}
	handler := Handler()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<title>server-man</title>") {
		t.Fatalf("index: status=%d, body=%s", w.Code, w.Body)
	}
	script := regexp.MustCompile(`src="(/assets/[^\"]+\.js)"`).FindStringSubmatch(w.Body.String())
	if len(script) != 2 {
		t.Fatal("production index has no bundled script")
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", script[1], nil))
	if w.Code != 200 || w.Body.Len() == 0 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("bundled script: status=%d, bytes=%d", w.Code, w.Body.Len())
	}
	for _, path := range []string{"/missing", "/assets/missing.js", "/assets/../../go.mod"} {
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code == 200 {
			t.Errorf("unexpected successful asset response for %s", path)
		}
	}
}
