package declarative

import (
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"
)

type HistoryEntry struct {
	Revision   string    `json:"revision"`
	ModifiedAt time.Time `json:"modifiedAt"`
	Valid      bool      `json:"valid"`
	Error      string    `json:"error,omitempty"`
}

func (s Store) historyRoot() (*os.Root, error) {
	path := s.Path + ".history"
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("configuration history must be an owner-only directory")
	}
	return os.OpenRoot(path)
}
func readHistory(root *os.Root, revision string) ([]byte, error) {
	if !revisionPattern.MatchString(revision) {
		return nil, errors.New("invalid history revision")
	}
	name := revision + ".yaml"
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("configuration history entry is unavailable or unsafe")
	}
	defer f.Close()
	raw, err := readPrivateConfiguration(f)
	if err != nil || digest(raw) != revision {
		return nil, errors.New("configuration history entry is damaged or unsafe")
	}
	return raw, nil
}
func (s Store) ReadHistory(revision string) ([]byte, error) {
	if !revisionPattern.MatchString(revision) {
		return nil, errors.New("invalid history revision")
	}
	root, err := s.historyRoot()
	if err != nil {
		return nil, errors.New("configuration history is unavailable or unsafe")
	}
	defer root.Close()
	return readHistory(root, revision)
}
func (s Store) History() ([]HistoryEntry, error) {
	result := []HistoryEntry{}
	root, err := s.historyRoot()
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return nil, errors.New("configuration history is unavailable or unsafe")
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(HistoryLimit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > HistoryLimit {
		return nil, errors.New("configuration history entry limit exceeded")
	}
	for _, entry := range entries {
		revision := strings.TrimSuffix(entry.Name(), ".yaml")
		if entry.Name() != revision+".yaml" || !revisionPattern.MatchString(revision) {
			return nil, errors.New("unexpected configuration history entry")
		}
		item := HistoryEntry{Revision: revision}
		if info, e := entry.Info(); e == nil {
			item.ModifiedAt = info.ModTime().UTC()
		}
		raw, e := readHistory(root, revision)
		if e != nil {
			item.Error = "History entry is damaged, inaccessible or unsafe."
		} else if _, e = Parse(raw); e != nil {
			item.Error = "This revision contains invalid configuration; inspect privately for recovery."
		} else {
			item.Valid = true
		}
		result = append(result, item)
	}
	slices.SortFunc(result, func(a, b HistoryEntry) int {
		if order := b.ModifiedAt.Compare(a.ModifiedAt); order != 0 {
			return order
		}
		return strings.Compare(a.Revision, b.Revision)
	})
	return result, nil
}
