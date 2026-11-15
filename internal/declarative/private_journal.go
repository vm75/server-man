package declarative

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Journals contain private operation metadata, never native output or secrets.
func readPrivateJournal(path string, value any) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("journal must be an owner-only regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > MaxConfigBytes {
		return errors.New("journal too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return errors.New("invalid private journal")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("invalid private journal")
	}
	return nil
}

func writePrivateJournal(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > MaxConfigBytes {
		return errors.New("journal too large")
	}
	if info, e := os.Lstat(path); e == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return errors.New("unsafe private journal")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".server-man-journal-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
