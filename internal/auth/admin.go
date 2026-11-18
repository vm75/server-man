package auth

import (
	"strings"
	"time"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"syscall"

	"server-man/internal/config"
)

// LoadWithConfig binds administrator credentials and browser sessions to the
// private unified configuration and state directory. Token bearer access stays
// available for MCP clients even after the browser switches to passwords.
func LoadWithConfig(cfg config.Config) (*Auth, error) {
	a, err := Load(cfg.TokenFile, cfg.PublicOrigin)
	if err != nil {
		return nil, err
	}
	a.username, a.passwordHash = cfg.AdminUsername, cfg.AdminPasswordHash
	a.configPath, a.stateDir = cfg.Path, cfg.StateDir
	a.sessionPath = filepath.Join(cfg.StateDir, "browser-sessions.json")
	if err := a.loadSessions(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Auth) AdminConfigured() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.username != ""
}

func (a *Auth) AdminCredentials() (string, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.username, a.passwordHash
}

func hashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 256 {
		return "", errors.New("administrator password must be 12 to 256 characters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, config.AdminPasswordIterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s",
		config.AdminPasswordIterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

func matchesPassword(password, encoded string) bool {
	if !config.ValidAdminPasswordHash(encoded) {
		return false
	}
	parts := splitHash(encoded)
	salt, _ := base64.RawStdEncoding.DecodeString(parts[2])
	want, _ := base64.RawStdEncoding.DecodeString(parts[3])
	got, err := pbkdf2.Key(sha256.New, password, salt, config.AdminPasswordIterations, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

func splitHash(encoded string) []string {
	// Format is checked by ValidAdminPasswordHash before this helper.
	return strings.Split(encoded, "$")
}

var ErrInvalidCredentials = errors.New("invalid credentials")
var ErrRateLimited = errors.New("too many login attempts; try again later")
var ErrTooManySessions = errors.New("too many active sessions; sign out from another browser or wait for a session to expire")

type failedAttempts struct {
	Count int
	Reset time.Time
}

func clientID(client []string) string {
	if len(client) == 0 {
		return ""
	}
	host, _, err := net.SplitHostPort(client[0])
	if err != nil {
		host = client[0]
	}
	if len(host) > 128 {
		return ""
	}
	return host
}

// At most 128 source addresses are tracked, with a five-attempt/five-minute
// limit. Do not trust Forwarded/X-Forwarded-For headers for authentication.
func (a *Auth) allowAttemptLocked(client []string) bool {
	key := clientID(client)
	if key == "" {
		return true
	}
	entry := a.attempts[key]
	return !time.Now().Before(entry.Reset) || entry.Count < 5
}

func (a *Auth) recordAttemptLocked(client []string, success bool) {
	key := clientID(client)
	if key == "" {
		return
	}
	if success {
		delete(a.attempts, key)
		return
	}
	if len(a.attempts) >= 128 {
		for other := range a.attempts {
			if time.Now().After(a.attempts[other].Reset) {
				delete(a.attempts, other)
			}
		}
	}
	if len(a.attempts) >= 128 && a.attempts[key].Reset.IsZero() {
		return
	}
	entry := a.attempts[key]
	if time.Now().After(entry.Reset) {
		entry = failedAttempts{Reset: time.Now().Add(5 * time.Minute)}
	}
	entry.Count++
	a.attempts[key] = entry
}

func (a *Auth) LoginPassword(w http.ResponseWriter, username, password string, client ...string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.allowAttemptLocked(client) {
		return ErrRateLimited
	}
	if a.username == "" || !matchesPassword(password, a.passwordHash) ||
		subtle.ConstantTimeCompare([]byte(username), []byte(a.username)) != 1 {
		a.recordAttemptLocked(client, false)
		return ErrInvalidCredentials
	}
	a.recordAttemptLocked(client, true)
	return a.issueSessionLocked(w)
}

func (a *Auth) SetupAdmin(w http.ResponseWriter, token, username, password string, client ...string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.allowAttemptLocked(client) {
		return ErrRateLimited
	}
	if !a.MatchesToken(token) {
		a.recordAttemptLocked(client, false)
		return ErrInvalidCredentials
	}
	if a.username != "" {
		return config.ErrAdminConfigured
	}
	a.recordAttemptLocked(client, true)
	if !config.ValidAdminUsername(username) {
		return errors.New("admin username must start with a letter and contain only letters, numbers, _, - or . (up to 64 characters)")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	if a.configPath == "" || a.stateDir == "" {
		return errors.New("administrator setup requires a configured YAML and state directory")
	}
	// Coordinate with managed configuration jobs in this and other processes.
	if err := os.MkdirAll(a.stateDir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(a.stateDir, "mutation.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if info, err := lock.Stat(); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("unsafe management mutation lock")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("another management operation is running; retry administrator setup")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if err := config.SetupAdmin(a.configPath, username, hash); err != nil {
		return err
	}
	a.username, a.passwordHash = username, hash
	a.sessions = make(map[[32]byte]time.Time) // Bootstrap token cookies cannot survive setup.
	return a.issueSessionLocked(w)
}
