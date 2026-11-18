package auth

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"server-man/internal/config"
)

func TestAdminSetupPasswordAndPersistedCookie(t *testing.T) {
	cfg, err := config.Defaults()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg.Path = filepath.Join(root, "config", "server-man.yaml")
	cfg.TokenFile = filepath.Join(root, "config", "token")
	cfg.StateDir = filepath.Join(root, "state")
	cfg.PublicOrigin = "https://server.example.com"
	a, err := LoadWithConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfg.TokenFile)
	if err != nil { t.Fatal(err) }
	token := strings.TrimSpace(string(data))
	w := httptest.NewRecorder()
	if err := a.Login(w, token); err != nil { t.Fatal(err) }
	initial := w.Result().Cookies()[0]
	if err := a.SetupAdmin(httptest.NewRecorder(), "wrong-token", "admin", "correct horse battery staple"); err != ErrInvalidCredentials {
		t.Fatalf("invalid setup token accepted: %v", err)
	}
	w = httptest.NewRecorder()
	if err := a.SetupAdmin(w, token, "admin", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if !a.AdminConfigured() {
		t.Fatal("admin setup not active")
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.MaxAge < 24 * 60 * 60 {
		t.Fatal("browser cookie should persist, remain HttpOnly and use Secure on HTTPS")
	}
	loaded, err := config.Load(cfg.Path)
	if err != nil || loaded.AdminUsername != "admin" || !strings.HasPrefix(loaded.AdminPasswordHash, "pbkdf2-sha256$600000$") {
		t.Fatal("credentials not stored safely", err)
	}
	if strings.Contains(loaded.AdminPasswordHash, "correct horse") {
		t.Fatal("plaintext password stored")
	}
	if err := a.Login(httptest.NewRecorder(), token); err == nil {
		t.Fatal("token browser login allowed after admin setup")
	}
	for _, pair := range [][2]string{{"other", "correct horse battery staple"}, {"admin", "bad password"}} {
		if err := a.LoginPassword(httptest.NewRecorder(), pair[0], pair[1]); err != ErrInvalidCredentials {
			t.Fatalf("bad credentials accepted: %v", err)
		}
	}
	if err := a.LoginPassword(httptest.NewRecorder(), "admin", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	loaded.TokenFile = cfg.TokenFile
	loaded.StateDir = cfg.StateDir
	loaded.PublicOrigin = cfg.PublicOrigin
	restarted, err := LoadWithConfig(loaded)
	if err != nil { t.Fatal(err) }
	request := httptest.NewRequest("GET", "https://server.example.com/api/status", nil)
	request.AddCookie(cookie)
	if !restarted.Authorized(request) {
		t.Fatal("cookie did not survive server restart")
	}
	oldRequest := httptest.NewRequest("GET", "https://server.example.com/api/status", nil)
	oldRequest.AddCookie(initial)
	if restarted.Authorized(oldRequest) {
		t.Fatal("bootstrap token cookie survived setup")
	}
	if err := restarted.Logout(httptest.NewRecorder(), request); err != nil { t.Fatal(err) }
	again, err := LoadWithConfig(loaded)
	if err != nil { t.Fatal(err) }
	if again.Authorized(request) {
		t.Fatal("logged-out cookie survived restart")
	}
	info, err := os.Stat(filepath.Join(cfg.StateDir, "browser-sessions.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("sessions file is not private", err)
	}
}

func TestPasswordLoginRateLimit(t *testing.T) {
	cfg, err := config.Defaults()
	if err != nil { t.Fatal(err) }
	root := t.TempDir()
	cfg.Path = filepath.Join(root, "server-man.yaml")
	cfg.TokenFile = filepath.Join(root, "token")
	cfg.StateDir = filepath.Join(root, "state")
	a, err := LoadWithConfig(cfg)
	if err != nil { t.Fatal(err) }
	token, err := os.ReadFile(cfg.TokenFile)
	if err != nil { t.Fatal(err) }
	if err := a.SetupAdmin(httptest.NewRecorder(), strings.TrimSpace(string(token)), "admin", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := a.LoginPassword(httptest.NewRecorder(), "admin", "incorrect", "192.0.2.1:12345"); err != ErrInvalidCredentials {
			t.Fatal(err)
		}
	}
	if err := a.LoginPassword(httptest.NewRecorder(), "admin", "correct horse battery staple", "192.0.2.1:12345"); err != ErrRateLimited {
		t.Fatal("rate limit did not block login", err)
	}
	if err := a.LoginPassword(httptest.NewRecorder(), "admin", "correct horse battery staple", "192.0.2.2:12345"); err != nil {
		t.Fatal("separate client was incorrectly rate limited", err)
	}
}
