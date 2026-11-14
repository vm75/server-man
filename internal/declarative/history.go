package declarative

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const HistoryLimit = 8

// History is private and adjacent to the manifest so it follows relocation.
// Entries are content-addressed and contain configuration, never secret files.
func (s Store) rememberRaw(data []byte, revision string) error {
	var err error
	path := s.Path + ".history"
	if err = os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("configuration history must be an owner-only directory")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	name := revision + ".yaml"
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			_ = root.Remove(name)
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	} else if !os.IsExist(err) {
		return err
	} else {
		existing, e := readArtifact(root, name)
		if e != nil || !bytes.Equal(existing, data) {
			return errors.New("existing configuration history is damaged or unsafe")
		}
		info, e := root.Lstat(name)
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return errors.New("configuration history entry must be a private regular file")
		}
		// Reusing an older revision makes it recent history again.
		if e = root.Chtimes(name, time.Now(), time.Now()); e != nil {
			return e
		}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	type item struct {
		name string
		time int64
	}
	var items []item
	for _, e := range entries {
		if e.Name() == name || !strings.HasSuffix(e.Name(), ".yaml") || len(e.Name()) != 69 {
			continue
		}
		i, err := e.Info()
		if err != nil {
			return err
		}
		items = append(items, item{e.Name(), i.ModTime().UnixNano()})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].time == items[j].time {
			return items[i].name < items[j].name
		}
		return items[i].time > items[j].time
	})
	for _, entry := range items[min(len(items), HistoryLimit-1):] {
		if err = root.Remove(entry.name); err != nil {
			return err
		}
	}
	d, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
