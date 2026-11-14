package declarative

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

var ErrConflict = errors.New("configuration changed; reload before saving")

// Store persists desired definitions only. Runtime activation belongs to the
// coordinator. Callers must hold the shared management lock across Save/apply.
type Store struct{ Path string }
type Snapshot struct {
	Config   Config `json:"config"`
	Revision string `json:"revision"`
}

func digest(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }
func (s Store) Read() (Snapshot, error) {
	var result Snapshot
	f, err := os.OpenFile(s.Path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return result, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return result, errors.New("configuration must be an owner-only regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil {
		return result, err
	}
	result.Revision = digest(data)
	result.Config, err = Parse(data)
	return result, err
}
func (s Store) Save(c Config, expected string) (Snapshot, error) {
	data, err := Encode(c)
	if err != nil {
		return Snapshot{}, err
	}
	if len(data) > MaxConfigBytes {
		return Snapshot{}, errors.New("configuration exceeds 1 MiB")
	}
	old, err := s.Read()
	if err != nil && !os.IsNotExist(err) {
		return Snapshot{}, err
	}
	current := "missing"
	if err == nil {
		current = old.Revision
	}
	if expected != current {
		return Snapshot{}, ErrConflict
	}
	// The management job lock serializes writers across application processes.
	dir := filepath.Dir(s.Path)
	f, err := os.CreateTemp(dir, ".server-man-config-*")
	if err != nil {
		return Snapshot{}, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return Snapshot{}, err
	}
	if closeErr != nil {
		return Snapshot{}, closeErr
	}
	// Recheck immediately before replacement; this also detects external edits.
	latest, readErr := s.Read()
	if current == "missing" {
		if !os.IsNotExist(readErr) {
			return Snapshot{}, ErrConflict
		}
	} else if readErr != nil || latest.Revision != current {
		return Snapshot{}, ErrConflict
	}
	if err = os.Rename(f.Name(), s.Path); err != nil {
		return Snapshot{}, err
	}
	d, err := os.Open(dir)
	if err != nil {
		return Snapshot{}, err
	}
	defer d.Close()
	if err = d.Sync(); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Config: c, Revision: digest(data)}, nil
}

// Resolve interprets portable paths against the manifest, never the working directory.
// Filesystem containment and symlink validation are performed by preflight.
func (s Store) Resolve(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(filepath.Dir(s.Path), path)
}
