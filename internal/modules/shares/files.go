package shares

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"server-man/internal/core"
)

type snapshot struct {
	data   []byte
	exists bool
	mode   os.FileMode
}

func readFile(path string, noSymlink bool) (snapshot, error) {
	flags := os.O_RDONLY | syscall.O_NONBLOCK
	if noSymlink {
		flags |= syscall.O_NOFOLLOW
	}
	f, err := os.OpenFile(path, flags, 0)
	if errors.Is(err, os.ErrNotExist) {
		return snapshot{}, nil
	}
	if err != nil {
		return snapshot{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFile {
		return snapshot{}, errors.New("configuration must be a regular file at most 1 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil || len(data) > maxFile {
		return snapshot{}, errors.New("configuration could not be read within 1 MiB")
	}
	return snapshot{data: data, exists: true, mode: info.Mode().Perm()}, nil
}
func (s snapshot) revision() string {
	prefix := byte(0)
	if s.exists {
		prefix = 1
	}
	sum := sha256.Sum256(append([]byte{prefix}, s.data...))
	return hex.EncodeToString(sum[:])
}
func render(protocol string, defs []Definition) ([]byte, error) {
	if len(defs) > 64 {
		return nil, core.Invalid("Managed fragments are limited to 64 shares")
	}
	slices.SortFunc(defs, func(a, b Definition) int { return strings.Compare(a.Name, b.Name) })
	var text strings.Builder
	text.WriteString(header)
	for _, d := range defs {
		data, err := json.Marshal(d)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&text, "# server-man-share: %s\n", data)
		if protocol == "samba" {
			yes := func(value bool) string {
				if value {
					return "yes"
				}
				return "no"
			}
			fmt.Fprintf(&text, "[%s]\n\tpath = %s\n\tread only = %s\n\tguest ok = %s\n\tbrowseable = %s\n\tvalid users = %s\n", d.Name, d.Path, yes(d.ReadOnly), yes(d.Guest), yes(d.Browseable), strings.Join(d.Users, " "))
		} else {
			text.WriteString(strings.ReplaceAll(d.Path, " ", `\040`))
			for _, selector := range d.Clients {
				fmt.Fprintf(&text, " %s(%s)", selector, options(d))
			}
			text.WriteByte('\n')
		}
		text.WriteByte('\n')
	}
	if text.Len() > maxFile {
		return nil, core.Invalid("Managed fragment exceeds 1 MiB")
	}
	return []byte(text.String()), nil
}
func load(protocol string, s snapshot) ([]Definition, error) {
	defs := []Definition{}
	if !s.exists {
		return defs, nil
	}
	if !bytes.HasPrefix(s.data, []byte(header)) {
		return nil, errors.New("managed file is not owned by server-man; preserve it and choose an empty unused location")
	}
	seen := map[string]bool{}
	paths := map[string]bool{}
	for _, line := range strings.Split(string(s.data), "\n") {
		if !strings.HasPrefix(line, "# server-man-share: ") {
			continue
		}
		var d Definition
		dec := json.NewDecoder(strings.NewReader(strings.TrimPrefix(line, "# server-man-share: ")))
		dec.DisallowUnknownFields()
		if dec.Decode(&d) != nil {
			return nil, errors.New("managed share metadata is invalid")
		}
		normalized, err := validate(protocol, d, false)
		if err != nil {
			return nil, errors.New("managed share metadata is unsupported")
		}
		d = normalized
		if seen[d.Name] || (protocol == "nfs" && paths[d.Path]) {
			return nil, errors.New("duplicate managed share identity or export path")
		}
		seen[d.Name] = true
		paths[d.Path] = true
		defs = append(defs, d)
	}
	rendered, err := render(protocol, defs)
	if err != nil || !bytes.Equal(rendered, s.data) {
		return nil, errors.New("managed file was edited outside server-man; changes will not be overwritten")
	}
	return defs, nil
}

func temporary(dir string, data []byte, mode os.FileMode) (string, error) {
	f, err := os.CreateTemp(dir, ".server-man-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(name)
		}
	}()
	if err := f.Chmod(mode); err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	ok = true
	return name, nil
}
func syncDir(path string) error {
	f, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func publish(path, candidate string) error {
	if err := os.Rename(candidate, path); err != nil {
		return err
	}
	return syncDir(path)
}
func sameFile(path string, want snapshot) bool {
	current, err := readFile(path, true)
	return err == nil && current.revision() == want.revision()
}
func restore(path string, old snapshot, current []byte) error {
	now, err := readFile(path, true)
	if err != nil {
		return err
	}
	if !now.exists || !bytes.Equal(now.data, current) {
		return errors.New("rollback refused because the managed file changed externally")
	}
	if !old.exists {
		if err := os.Remove(path); err != nil {
			return err
		}
		return syncDir(path)
	}
	temp, err := temporary(filepath.Dir(path), old.data, old.mode)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	return publish(path, temp)
}
