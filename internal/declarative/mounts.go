package declarative

import (
	"bufio"
	"errors"
	"io"
	"path/filepath"
	"strings"
)

type MountInfo struct {
	Point, Source string
	ReadOnly      bool
}

// Mount and superblock flags both matter: bind mounts may be rw while the
// underlying filesystem is ro. This inventory is read-only and bounded.
func mountHasOption(options, option string) bool {
	for _, item := range strings.Split(options, ",") {
		if item == option {
			return true
		}
	}
	return false
}

func mountUnescape(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}

// ParseMountInfo extracts only mount identity, never file data. Mount order is
// significant: the final matching mount at an identical point is the visible one.
func ParseMountInfo(r io.Reader) ([]MountInfo, error) {
	scanner := bufio.NewScanner(io.LimitReader(r, 4<<20))
	scanner.Buffer(make([]byte, 4096), 64<<10)
	var result []MountInfo
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		sep := -1
		for i, f := range fields {
			if f == "-" {
				sep = i
				break
			}
		}
		if len(fields) < 10 || sep < 6 || sep+3 >= len(fields) {
			return nil, errors.New("invalid mount inventory")
		}
		result = append(result, MountInfo{
			Point:    mountUnescape(fields[4]),
			Source:   mountUnescape(fields[sep+2]),
			ReadOnly: mountHasOption(fields[5], "ro") || mountHasOption(fields[sep+3], "ro"),
		})
		if len(result) > 8192 {
			return nil, errors.New("mount inventory limit exceeded")
		}
	}
	return result, scanner.Err()
}
// The last of equally specific mounts is the visible one. Never infer
// writability from the process's privilege or an ancestor mount alone.
func mountedFilesystem(path string, mounts []MountInfo) (MountInfo, bool) {
	selected, found := MountInfo{}, false
	for _, m := range mounts {
		rel, err := filepath.Rel(m.Point, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && (!found || len(m.Point) >= len(selected.Point)) {
			selected, found = m, true
		}
	}
	return selected, found
}

func mountedSource(path string, mounts []MountInfo) string {
	selected, ok := mountedFilesystem(path, mounts)
	if !ok {
		return ""
	}
	return selected.Source
}
