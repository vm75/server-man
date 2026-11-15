// Package auth authenticates a single administrator using a local random token.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const cookieName = "server-man_session"
const sessionLifetime = 8 * time.Hour

type Auth struct {
	digest       [32]byte
	publicOrigin string
	mu           sync.Mutex
	sessions     map[[32]byte]time.Time
}

// Load creates a token file if absent. Its contents are never written to logs.
func Load(path, publicOrigin string) (*Auth, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		var bytes [32]byte
		if _, err = rand.Read(bytes[:]); err == nil {
			_, err = io.WriteString(file, base64.RawURLEncoding.EncodeToString(bytes[:])+"\n")
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(path)
			return nil, fmt.Errorf("create token: %w", err)
		}
	} else if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create token file: %w", err)
	}
	file, err = os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open token file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("token file must be a regular file readable only by its owner (chmod 600)")
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(string(data))
	if len(data) > 4096 || len(token) < 32 || len(token) > 256 || strings.ContainsAny(token, "\r\n\t ") {
		return nil, errors.New("token file must contain a single token of 32 to 256 characters")
	}
	return &Auth{digest: sha256.Sum256([]byte(token)), publicOrigin: strings.TrimRight(publicOrigin, "/"), sessions: map[[32]byte]time.Time{}}, nil
}

func (a *Auth) MatchesToken(token string) bool {
	digest := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(digest[:], a.digest[:]) == 1
}

// ValidOrigin also rejects DNS rebinding through unexpected Host headers.
func (a *Auth) ValidOrigin(r *http.Request) bool {
	requestURL, err := url.Parse("http://" + r.Host)
	if err != nil || requestURL.Host == "" || requestURL.User != nil || requestURL.Path != "" || requestURL.RawQuery != "" || requestURL.Fragment != "" {
		return false
	}
	host := requestURL.Hostname()
	allowedHost := false
	if a.publicOrigin != "" {
		publicURL, _ := url.Parse(a.publicOrigin)
		allowedHost = strings.EqualFold(r.Host, publicURL.Host)
		if publicURL.Scheme != "http" {
			ip := net.ParseIP(host)
			allowedHost = allowedHost || strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
		}
	} else {
		allowedHost = strings.EqualFold(host, "localhost")
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			allowedHost = true
		}
	}
	if !allowedHost {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return origin == scheme+"://"+r.Host || (a.publicOrigin != "" && origin == a.publicOrigin)
}

func (a *Auth) Authorized(r *http.Request) bool {
	if !a.ValidOrigin(r) {
		return false
	}
	if parts := strings.Fields(r.Header.Get("Authorization")); len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && a.MatchesToken(parts[1]) {
		return true
	}
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	key := sha256.Sum256([]byte(cookie.Value))
	a.mu.Lock()
	defer a.mu.Unlock()
	expires, ok := a.sessions[key]
	if ok && time.Now().Before(expires) {
		return true
	}
	delete(a.sessions, key)
	return false
}

func (a *Auth) secureCookie() bool {
	origin, err := url.Parse(a.publicOrigin)
	return err == nil && origin.Scheme == "https"
}

func (a *Auth) Login(w http.ResponseWriter, token string) error {
	if !a.MatchesToken(token) {
		return errors.New("invalid access token")
	}
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return err
	}
	value := base64.RawURLEncoding.EncodeToString(bytes[:])
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for key, expires := range a.sessions {
		if !now.Before(expires) {
			delete(a.sessions, key)
		}
	}
	if len(a.sessions) >= 32 {
		return errors.New("too many active sessions; sign out from another browser or wait for a session to expire")
	}
	a.sessions[sha256.Sum256([]byte(value))] = now.Add(sessionLifetime)
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: value, Path: "/", HttpOnly: true, Secure: a.secureCookie(), SameSite: http.SameSiteStrictMode, MaxAge: int(sessionLifetime.Seconds())})
	return nil
}

func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil {
		a.mu.Lock()
		delete(a.sessions, sha256.Sum256([]byte(cookie.Value)))
		a.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", HttpOnly: true, Secure: a.secureCookie(), SameSite: http.SameSiteStrictMode, MaxAge: -1})
}
