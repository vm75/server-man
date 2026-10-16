package auth

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateTokenAndSessionLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth", "token")
	a, err := Load(path, "https://server.example.com")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	token := strings.TrimSpace(string(data))
	if len(token) < 32 {
		t.Fatal("short token")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("token is not private")
	}
	w := httptest.NewRecorder()
	if err := a.Login(w, token); err != nil {
		t.Fatal(err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].MaxAge <= 0 {
		t.Fatal("invalid session cookie")
	}
	r := httptest.NewRequest("GET", "https://server.example.com/api/status", nil)
	r.AddCookie(cookies[0])
	if !a.Authorized(r) {
		t.Fatal("valid session was rejected")
	}
	a.Logout(httptest.NewRecorder(), r)
	if a.Authorized(r) {
		t.Fatal("logged out session was accepted")
	}
	if err := a.Login(httptest.NewRecorder(), token+"wrong"); err == nil {
		t.Fatal("invalid token accepted")
	}
	reloaded, err := Load(path, "")
	if err != nil || !reloaded.MatchesToken(token) {
		t.Fatal("token did not survive reload")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, ""); err == nil {
		t.Fatal("publicly readable token accepted")
	}
}

func TestOriginAndBearerBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	a, err := Load(path, "https://server.example.com")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, tc := range []struct {
		name, target, origin string
		allowed              bool
	}{
		{"local client", "http://127.0.0.1:8080/api/status", "", true},
		{"development browser", "http://localhost:5173/api/status", "http://localhost:5173", true},
		{"public proxy", "https://server.example.com/api/status", "https://server.example.com", true},
		{"different origin", "http://127.0.0.1:8080/api/status", "https://evil.example", false},
		{"null origin", "http://127.0.0.1:8080/api/status", "null", false},
		{"rebinding host", "http://evil.example/api/status", "http://evil.example", false},
		{"unconfigured proxy", "https://other.example.com/api/status", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.target, nil)
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(data)))
			if got := a.Authorized(r); got != tc.allowed {
				t.Fatalf("allowed=%v, got=%v", tc.allowed, got)
			}
		})
	}
}

func TestTokenFileRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if _, err := Load(target, ""); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link, ""); err == nil {
		t.Fatal("symlink token accepted")
	}
}
