package monitor

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const processLimit = 4096

type source struct {
	proc, sys, etc string
	host           Host
}

func newSource() *source {
	s := &source{proc: "/proc", sys: "/sys", etc: "/etc"}
	s.host = s.identity()
	return s
}

func (s *source) identity() Host {
	hostname, _ := os.Hostname()
	host := Host{Hostname: hostname, OS: "Linux", Architecture: runtime.GOARCH, CPUModel: "Unknown CPU"}
	if data, err := os.ReadFile(filepath.Join(s.proc, "sys/kernel/osrelease")); err == nil {
		host.Kernel = strings.TrimSpace(string(data))
	}
	if data, err := os.ReadFile(filepath.Join(s.etc, "os-release")); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			if key, value, ok := strings.Cut(line, "="); ok && key == "PRETTY_NAME" {
				host.OS = strings.Trim(value, `"'`)
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(s.proc, "cpuinfo")); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			if key, value, ok := strings.Cut(line, ":"); ok && (strings.TrimSpace(key) == "model name" || strings.TrimSpace(key) == "Hardware") {
				host.CPUModel = strings.TrimSpace(value)
				break
			}
		}
	}
	return host
}

func (s *source) collect(ctx context.Context) (reading, error) {
	r := reading{snapshot: Snapshot{At: time.Now().UTC(), Host: s.host, Disks: []Disk{}, DiskIO: []DiskIO{}, Networks: []Network{}, Temperatures: []Temperature{}, Warnings: []string{}}, disk: map[string]counterPair{}}
	read := func(name string) (string, error) {
		data, err := os.ReadFile(filepath.Join(s.proc, name))
		return string(data), err
	}
	text, err := read("stat")
	if err != nil {
		return r, fmt.Errorf("read CPU counters: %w", err)
	}
	r.cpu, err = parseCPU(text)
	if err != nil {
		return r, err
	}
	text, err = read("meminfo")
	if err != nil {
		return r, fmt.Errorf("read memory counters: %w", err)
	}
	r.snapshot.Memory, err = parseMemory(text)
	if err != nil {
		return r, err
	}
	if text, err := read("uptime"); err == nil {
		fields := strings.Fields(text)
		if len(fields) > 0 {
			if value, err := strconv.ParseFloat(fields[0], 64); err == nil && finite(value) && value >= 0 {
				r.snapshot.UptimeSeconds = &value
			}
		}
	}
	if r.snapshot.UptimeSeconds == nil {
		r.snapshot.Warnings = append(r.snapshot.Warnings, "Uptime is unavailable.")
	}
	if text, err := read("loadavg"); err == nil {
		fields := strings.Fields(text)
		if len(fields) >= 3 {
			var loads [3]float64
			valid := true
			for i := range 3 {
				loads[i], err = strconv.ParseFloat(fields[i], 64)
				valid = valid && err == nil && finite(loads[i]) && loads[i] >= 0
			}
			if valid {
				r.snapshot.Load = &loads
			}
		}
	}
	if r.snapshot.Load == nil {
		r.snapshot.Warnings = append(r.snapshot.Warnings, "Load averages are unavailable.")
	}
	if text, err := read("net/dev"); err == nil {
		r.network, err = parseNetwork(text)
		if err != nil {
			r.snapshot.Warnings = append(r.snapshot.Warnings, err.Error())
		}
	} else {
		r.snapshot.Warnings = append(r.snapshot.Warnings, "Network counters are unavailable.")
	}
	if text, err := read("diskstats"); err == nil {
		r.disk = s.diskCounters(text)
	} else {
		r.snapshot.Warnings = append(r.snapshot.Warnings, "Disk I/O counters are unavailable.")
	}
	r.snapshot.Disks, err = s.disks()
	if err != nil {
		r.snapshot.Warnings = append(r.snapshot.Warnings, err.Error())
	}
	r.processes, r.snapshot.ProcessesLimited, err = s.processes(ctx)
	if err != nil {
		r.snapshot.Warnings = append(r.snapshot.Warnings, "Process inventory is unavailable.")
	}
	r.snapshot.ProcessCount = len(r.processes)
	if r.snapshot.ProcessesLimited {
		r.snapshot.Warnings = append(r.snapshot.Warnings, "Process sampling is limited to 4096 visible processes.")
	}
	r.snapshot.Temperatures = s.temperatures()
	if data, err := os.ReadFile(filepath.Join(s.proc, "cpuinfo")); err == nil {
		var sum float64
		count := 0
		for line := range strings.SplitSeq(string(data), "\n") {
			if key, value, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(key) == "cpu MHz" {
				if mhz, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && finite(mhz) && mhz > 0 {
					sum += mhz
					count++
				}
			}
		}
		if count > 0 {
			value := sum / float64(count)
			r.snapshot.CPU.FrequencyMHz = &value
		}
	}
	if ctx.Err() != nil {
		return r, ctx.Err()
	}
	r.snapshot.Valid = true
	return r, nil
}

func (s *source) processes(ctx context.Context) (map[int]processCounter, bool, error) {
	result := map[int]processCounter{}
	directory, err := os.Open(s.proc)
	if err != nil {
		return result, false, err
	}
	defer directory.Close()
	scanned := 0
	for {
		names, err := directory.Readdirnames(256)
		for _, name := range names {
			if ctx.Err() != nil {
				return result, false, ctx.Err()
			}
			if _, err := strconv.Atoi(name); err != nil {
				continue
			}
			if scanned >= processLimit {
				return result, true, nil
			}
			scanned++
			data, err := os.ReadFile(filepath.Join(s.proc, name, "stat"))
			if err != nil {
				continue
			} // Processes can exit or become inaccessible mid-sample.
			process, err := parseProcess(string(data), uint64(os.Getpagesize()))
			if err == nil {
				result[process.PID] = process
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, false, err
		}
	}
	return result, false, nil
}

func (s *source) diskCounters(text string) map[string]counterPair {
	result := map[string]counterPair{}
	for line := range strings.SplitSeq(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 14 || strings.HasPrefix(fields[2], "loop") || strings.HasPrefix(fields[2], "ram") {
			continue
		}
		// /sys/block contains whole devices; partitions would duplicate their I/O.
		if _, err := os.Stat(filepath.Join(s.sys, "block", fields[2])); err != nil {
			continue
		}
		read, err := strconv.ParseUint(fields[5], 10, 64)
		if err != nil {
			continue
		}
		written, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			continue
		}
		result[fields[2]] = counterPair{read * 512, written * 512}
	}
	return result
}

func (s *source) disks() ([]Disk, error) {
	data, err := os.ReadFile(filepath.Join(s.proc, "self/mountinfo"))
	if err != nil {
		return []Disk{}, fmt.Errorf("Mounted filesystems are unavailable.")
	}
	mounts := parseMounts(string(data))
	result := []Disk{}
	failed := false
	for _, mount := range mounts {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(mount.Mount, &stat); err != nil {
			failed = true
			continue
		}
		mount.Total = stat.Blocks * uint64(stat.Bsize)
		mount.Used = (stat.Blocks - min(stat.Blocks, stat.Bfree)) * uint64(stat.Bsize)
		mount.Available = stat.Bavail * uint64(stat.Bsize)
		result = append(result, mount)
		if len(result) == 32 {
			break
		}
	}
	if failed {
		return result, fmt.Errorf("Some mounted filesystems could not be read.")
	}
	return result, nil
}

func parseMounts(text string) []Disk {
	local := []string{"ext2", "ext3", "ext4", "xfs", "btrfs", "zfs", "f2fs", "bcachefs", "overlay", "vfat", "exfat", "ntfs3"}
	byDevice := map[string]Disk{}
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for line := range strings.SplitSeq(text, "\n") {
		left, right, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		fields, source := strings.Fields(left), strings.Fields(right)
		if len(fields) < 6 || len(source) < 2 || !slices.Contains(local, source[0]) {
			continue
		}
		mount := unescape.Replace(fields[4])
		if !filepath.IsAbs(mount) {
			continue
		}
		key := fields[2] + ":" + source[0]
		previous, exists := byDevice[key]
		if exists && len(previous.Mount) <= len(mount) {
			continue
		}
		byDevice[key] = Disk{Device: unescape.Replace(source[1]), Mount: mount, Filesystem: source[0]}
	}
	result := make([]Disk, 0, len(byDevice))
	for _, mount := range byDevice {
		result = append(result, mount)
	}
	slices.SortFunc(result, func(a, b Disk) int { return strings.Compare(a.Mount, b.Mount) })
	return result
}

func (s *source) temperatures() []Temperature {
	result := []Temperature{}
	paths, _ := filepath.Glob(filepath.Join(s.sys, "class/thermal/thermal_zone*/temp"))
	for _, path := range paths {
		if len(result) >= 16 {
			break
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
		if err != nil || !finite(value) || value < -40000 || value > 150000 {
			continue
		}
		name := filepath.Base(filepath.Dir(path))
		if data, err := os.ReadFile(filepath.Join(filepath.Dir(path), "type")); err == nil {
			name = strings.TrimSpace(string(data))
		}
		result = append(result, Temperature{Name: name, Celsius: value / 1000})
	}
	return result
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
