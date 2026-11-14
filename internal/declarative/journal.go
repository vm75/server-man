package declarative

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

var revisionPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (c Coordinator) ReadJournal() (Journal, error) {
	var j Journal
	f, err := os.OpenFile(c.Store.Path+".apply.json", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return j, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return j, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return j, errors.New("apply journal must be an owner-only regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil {
		return j, err
	}
	if len(raw) > MaxConfigBytes {
		return j, errors.New("apply journal too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&j) != nil || j.Version != 1 {
		return j, errors.New("invalid apply journal")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return j, errors.New("invalid apply journal")
	}
	if !revisionPattern.MatchString(j.DesiredRevision) || (j.BaseRevision != "missing" && !revisionPattern.MatchString(j.BaseRevision)) || len(j.Resources) > 192 {
		return j, errors.New("invalid journal revisions or resource limit")
	}
	if j.Mode != "now" && j.Mode != "next_start" {
		return j, errors.New("invalid journal apply mode")
	}
	switch j.Stage {
	case "prepared", "saved", "applying", "blocked", "failed", "complete", "aborted":
	default:
		return j, errors.New("invalid journal stage")
	}
	for key := range j.Resources {
		kind, id, ok := strings.Cut(key, "/")
		if !ok || (kind != "service" && kind != "share") || !validID(id) {
			return j, errors.New("invalid journal resource")
		}
	}
	return j, nil
}
func (c Coordinator) writeJournal(j *Journal) error {
	j.UpdatedAt = time.Now().UTC()
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	path := c.Store.Path + ".apply.json"
	if info, e := os.Lstat(path); e == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return errors.New("unsafe apply journal")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".server-man-apply-*")
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
