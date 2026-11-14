package declarative

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const HistoryLimit = 8

// History is private and adjacent to the manifest so it follows relocation.
// Entries are content-addressed and contain configuration, never secret files.
func (s Store) remember(snapshot Snapshot) error {
	data, err := Encode(snapshot.Config)
	if err != nil {
		return err
	}
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
	name := snapshot.Revision + ".yaml"
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
		if !strings.HasSuffix(e.Name(), ".yaml") || len(e.Name()) != 69 {
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
	for _, entry := range items[min(len(items), HistoryLimit):] {
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
