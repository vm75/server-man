package declarative

import (
	"bufio"
	"errors"
	"io"
	"path/filepath"
	"strings"
)

type MountInfo struct{ Point, Source string }

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
		if len(fields) < 10 || sep < 6 || sep+2 >= len(fields) {
			return nil, errors.New("invalid mount inventory")
		}
		result = append(result, MountInfo{mountUnescape(fields[4]), mountUnescape(fields[sep+2])})
		if len(result) > 8192 {
			return nil, errors.New("mount inventory limit exceeded")
		}
	}
	return result, scanner.Err()
}
func mountedSource(path string, mounts []MountInfo) string {
	point, source := "", ""
	for _, m := range mounts {
		rel, err := filepath.Rel(m.Point, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, "../") && len(m.Point) >= len(point) {
			point = m.Point
			source = m.Source
		}
	}
	return source
}
