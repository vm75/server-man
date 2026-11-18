package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type storedSessions struct {
	Scope    string           `json:"scope"`
	Sessions map[string]int64 `json:"sessions"`
}

// The scope invalidates saved sessions when administrator credentials change
// or the initial token-only login is upgraded to an administrator account.
func (a *Auth) sessionScope() string {
	h := sha256.New()
	_, _ = h.Write(a.digest[:])
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(a.username))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(a.passwordHash))
	return hex.EncodeToString(h.Sum(nil))
}

func (a *Auth) loadSessions() error {
	if a.sessionPath == "" {
		return nil
	}
	f, err := os.OpenFile(a.sessionPath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("browser sessions file must be owner-only and regular")
	}
	data, err := io.ReadAll(io.LimitReader(f, 16<<10+1))
	if err != nil {
		return err
	}
	if len(data) > 16<<10 {
		return errors.New("browser sessions file is oversized")
	}
	var stored storedSessions
	if err := json.Unmarshal(data, &stored); err != nil || len(stored.Sessions) > 32 {
		return errors.New("invalid browser sessions file")
	}
	if stored.Scope != a.sessionScope() {
		return nil
	}
	now := time.Now()
	for key, timestamp := range stored.Sessions {
		digest, err := hex.DecodeString(key)
		if err != nil || len(digest) != 32 {
			return errors.New("invalid browser session identifier")
		}
		expires := time.Unix(timestamp, 0)
		if expires.After(now) && !expires.After(now.Add(sessionLifetime)) {
			var fixed [32]byte
			copy(fixed[:], digest)
			a.sessions[fixed] = expires
		}
	}
	return nil
}

func (a *Auth) saveSessionsLocked() error {
	if a.sessionPath == "" {
		return nil
	}
	dir := filepath.Dir(a.sessionPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	records := storedSessions{Scope: a.sessionScope(), Sessions: make(map[string]int64, len(a.sessions))}
	for key, expires := range a.sessions {
		if time.Now().Before(expires) {
			records.Sessions[hex.EncodeToString(key[:])] = expires.Unix()
		}
	}
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".browser-sessions-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if fi, err := os.Lstat(a.sessionPath); err == nil {
		if !fi.Mode().IsRegular() || fi.Mode().Perm()&0077 != 0 {
			return errors.New("unsafe browser sessions file")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(tmp.Name(), a.sessionPath); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
