package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"serverman/internal/core"
)

func TestCPUUsesDeltasWithoutDoubleCountingGuestTime(t *testing.T) {
	previous, err := parseCPU("cpu 100 10 20 800 30 2 3 4 50 5\ncpu0 100 10 20 800 30 2 3 4 50 5\n")
	if err != nil {
		t.Fatal(err)
	}
	current, err := parseCPU("cpu 140 10 30 850 30 2 3 4 80 5\ncpu0 140 10 30 850 30 2 3 4 80 5\n")
	if err != nil {
		t.Fatal(err)
	}
	usage, ticks := cpuDelta(current["cpu"], previous["cpu"])
	if usage == nil || *usage != 50 || ticks != 100 {
		t.Fatalf("CPU usage=%v ticks=%d", usage, ticks)
	}
	reset := current["cpu"]
	reset[0] = 1
	if usage, _ := cpuDelta(reset, current["cpu"]); usage != nil {
		t.Fatal("counter reset produced CPU usage")
	}
	decreasedIOWait := current["cpu"]
	decreasedIOWait[4]--
	if usage, _ := cpuDelta(decreasedIOWait, previous["cpu"]); usage == nil || *usage != 50 {
		t.Fatal("decreased iowait broke the sample")
	}
	for _, invalid := range []string{"", "cpu x 2 3 4", "cpu 1 2 3"} {
		if _, err := parseCPU(invalid); err == nil {
			t.Fatalf("invalid CPU input accepted: %q", invalid)
		}
	}
}

func TestMemoryAndProcessParsing(t *testing.T) {
	memory, err := parseMemory("MemTotal: 1000 kB\nMemAvailable: 600 kB\nCached: 200 kB\nSwapTotal: 100 kB\nSwapFree: 40 kB\n")
	if err != nil || memory.Used != 400*1024 || memory.SwapUsed != 60*1024 {
		t.Fatalf("memory=%+v err=%v", memory, err)
	}
	if _, err := parseMemory("MemTotal: not-a-number"); err == nil {
		t.Fatal("missing memory accepted")
	}
	process, err := parseProcess(processStat(42, "worker (a) b)", 30, 10, 500, 4), 4096)
	if err != nil || process.Name != "worker (a) b)" || process.ticks != 40 || process.start != 500 || process.Memory != 16384 {
		t.Fatalf("process=%+v err=%v", process, err)
	}
	if _, err := parseProcess("42 (vanished) S 1", 4096); err == nil {
		t.Fatal("truncated process stat accepted")
	}
}

func TestInitialSamplesRatesPIDReuseAndCounterResets(t *testing.T) {
	s := &Sampler{interval: 2 * time.Second}
	first := sampleReading(0)
	s.accept(first)
	if s.Snapshot().CPU.Usage != nil || s.Snapshot().Networks[0].RXBytesPerSecond != nil {
		t.Fatal("first sample fabricated rates")
	}
	second := sampleReading(1)
	second.processes[10] = processCounter{Process: Process{PID: 10, Name: "reused"}, start: 999, ticks: 1000}
	delete(second.processes, 20)
	s.accept(second)
	snapshot := s.Snapshot()
	if snapshot.CPU.Usage == nil || *snapshot.CPU.Usage != 50 {
		t.Fatalf("CPU=%+v", snapshot.CPU)
	}
	if *snapshot.Networks[0].RXBytesPerSecond != 250 || *snapshot.Networks[0].TXBytesPerSecond != 500 {
		t.Fatal("incorrect network rates")
	}
	if *snapshot.DiskIO[0].ReadBytesPerSecond != 512 {
		t.Fatal("incorrect I/O rates")
	}
	list := s.Processes("pid", "", 100)
	if len(list.Processes) != 2 || list.Processes[0].CPU != nil || list.Processes[1].CPU == nil || *list.Processes[1].CPU != 50 {
		t.Fatalf("processes=%+v", list.Processes)
	}
	third := sampleReading(2)
	third.cpu["cpu"] = cpuCounter{1}
	third.network["eth0"] = counterPair{1, 1}
	s.accept(third)
	if snapshot := s.Snapshot(); snapshot.CPU.Usage != nil || snapshot.Networks[0].RXBytesPerSecond != nil || snapshot.History[len(snapshot.History)-1].RX != nil {
		t.Fatal("counter reset produced a spike")
	}
}

func TestHistoryBoundsAndProcessSortingFiltering(t *testing.T) {
	s := &Sampler{interval: 2 * time.Second}
	for i := range 100 {
		s.accept(sampleReading(i))
	}
	oldView := s.Snapshot()
	if len(oldView.History) != 60 {
		t.Fatal("history is not bounded")
	}
	oldest := oldView.History[0].At
	s.accept(sampleReading(100))
	if oldView.History[0].At != oldest {
		t.Fatal("an earlier view's history was mutated")
	}
	list := s.Processes("memory", "", 1)
	if len(list.Processes) != 1 || list.Processes[0].PID != 20 || list.Matched != 3 {
		t.Fatalf("memory sort=%+v", list)
	}
	list = s.Processes("cpu", "WORKER", 500)
	if len(list.Processes) != 1 || list.Processes[0].PID != 30 {
		t.Fatalf("name filter=%+v", list)
	}
	if got := s.Processes("pid", "10", 10); got.Matched != 1 {
		t.Fatal("PID filter failed")
	}
	s.accept(sampleReading(1000))
	if len(s.Snapshot().History) != 1 {
		t.Fatal("samples older than the time window were retained")
	}
	s.accept(sampleReading(1))
	if view := s.Snapshot(); len(view.History) != 1 || view.Networks[0].RXBytesPerSecond != nil {
		t.Fatal("clock regression did not reset history and rates")
	}
}

func TestFixtureSourceMissingOptionalFilesAndMounts(t *testing.T) {
	s := fixtureSource(t)
	reading, err := s.collect(context.Background())
	if err != nil || !reading.snapshot.Valid || len(reading.snapshot.Warnings) == 0 || len(reading.processes) != 1 {
		t.Fatalf("reading=%+v error=%v", reading.snapshot, err)
	}
	if len(reading.snapshot.Temperatures) != 0 {
		t.Fatal("missing sensors fabricated temperatures")
	}
	if err := os.WriteFile(filepath.Join(s.proc, "loadavg"), []byte("NaN +Inf 1 1/2 3"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.sys, "class/thermal/thermal_zone0/temp")
	writeFixture(t, path, "NaN")
	reading, err = s.collect(context.Background())
	if err != nil || reading.snapshot.Load != nil || len(reading.snapshot.Temperatures) != 0 {
		t.Fatal("non-finite optional values accepted")
	}
	if _, err := json.Marshal(reading.snapshot); err != nil {
		t.Fatal(err)
	}
	mounts := parseMounts("1 0 8:1 / / rw - ext4 /dev/sda1 rw\n2 1 8:1 /some /srv/bind rw - ext4 /dev/sda1 rw\n3 1 8:2 / /mnt/my\\040disk rw - xfs /dev/sdb1 rw\n4 1 0:1 / /net rw - nfs server:/path rw\n")
	if len(mounts) != 2 || mounts[0].Mount != "/" || mounts[1].Mount != "/mnt/my disk" {
		t.Fatalf("mounts=%+v", mounts)
	}
	writeFixture(t, filepath.Join(s.sys, "block/sda/fixture"), "")
	counters := s.diskCounters("8 0 sda 1 0 100 0 1 0 200 0 0 0 0\n8 1 sda1 1 0 99 0 1 0 199 0 0 0 0\n7 0 loop0 1 0 100 0 1 0 200 0 0 0 0\n")
	if len(counters) != 1 || counters["sda"].first != 51200 || counters["sda"].second != 102400 {
		t.Fatalf("disk counters=%+v", counters)
	}
}

func TestRealLinuxSamplerAndToolBoundary(t *testing.T) {
	s := New(context.Background(), time.Second)
	defer s.Close()
	firstTime := s.Snapshot().At
	if snapshot := s.Snapshot(); !snapshot.Valid || snapshot.Memory.Total == 0 || len(snapshot.CPU.Cores) == 0 {
		t.Fatalf("real Linux sample unavailable: %s", snapshot.LastError)
	}
	registry := core.NewRegistry(false, nil)
	if err := Register(registry, s); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Invoke(context.Background(), "monitor_snapshot", json.RawMessage(`{}`), "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Invoke(context.Background(), "monitor_processes", json.RawMessage(`{"limit":501}`), "test"); err == nil {
		t.Fatal("oversized process query accepted")
	}
	var wait sync.WaitGroup
	for range 4 {
		wait.Go(func() {
			for range 140 {
				view := s.Snapshot()
				if _, err := json.Marshal(view); err != nil {
					t.Error(err)
				}
				_ = s.Processes("cpu", "", 50)
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
	wait.Wait()
	if snapshot := s.Snapshot(); !snapshot.At.After(firstTime) || snapshot.CPU.Usage == nil || len(snapshot.History) < 2 {
		t.Fatal("real sampler did not advance beyond its first sample")
	}
}

func sampleReading(index int) reading {
	return reading{
		snapshot: Snapshot{Valid: true, At: time.Unix(1_700_000_000+int64(index)*2, 0).UTC(), Memory: Memory{Total: 1000, Used: 200}, Networks: []Network{}, DiskIO: []DiskIO{}},
		cpu:      map[string]cpuCounter{"cpu": {uint64(200 + 100*index), 0, 0, uint64(800 + 100*index)}, "cpu0": {uint64(100 + 50*index), 0, 0, uint64(400 + 50*index)}, "cpu1": {uint64(100 + 50*index), 0, 0, uint64(400 + 50*index)}},
		network:  map[string]counterPair{"eth0": {uint64(1000 + 500*index), uint64(2000 + 1000*index)}},
		disk:     map[string]counterPair{"sda": {uint64(100 + 1024*index), uint64(200 + 2048*index)}},
		processes: map[int]processCounter{
			10: {Process: Process{PID: 10, Name: "demo", Memory: 10}, start: 100, ticks: uint64(50 + index*20)},
			20: {Process: Process{PID: 20, Name: "large", Memory: 200}, start: 200, ticks: uint64(100 + index*25)},
			30: {Process: Process{PID: 30, Name: "worker", Memory: 20}, start: 300, ticks: uint64(100 + index*50)},
		},
	}
}

func processStat(pid int, name string, user, system, start, rss int) string {
	fields := make([]string, 22)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0], fields[11], fields[12], fields[17], fields[19], fields[21] = "S", strconv.Itoa(user), strconv.Itoa(system), "2", strconv.Itoa(start), strconv.Itoa(rss)
	return fmt.Sprintf("%d (%s) %s", pid, name, strings.Join(fields, " "))
}

func writeFixture(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
func fixtureSource(t *testing.T) *source {
	root := t.TempDir()
	s := &source{proc: filepath.Join(root, "proc"), sys: filepath.Join(root, "sys"), etc: filepath.Join(root, "etc")}
	writeFixture(t, filepath.Join(s.proc, "stat"), "cpu 100 0 20 800 0 0 0 0\ncpu0 100 0 20 800 0 0 0 0\n")
	writeFixture(t, filepath.Join(s.proc, "meminfo"), "MemTotal: 1024 kB\nMemAvailable: 512 kB\n")
	writeFixture(t, filepath.Join(s.proc, "self/mountinfo"), "")
	writeFixture(t, filepath.Join(s.proc, "42/stat"), processStat(42, "fixture worker", 30, 10, 100, 4))
	writeFixture(t, filepath.Join(s.proc, "43/stat"), "truncated (vanished)")
	s.host = s.identity()
	return s
}

func BenchmarkLinuxCollect(b *testing.B) {
	s := newSource()
	for b.Loop() {
		r, err := s.collect(context.Background())
		if err != nil || !r.snapshot.Valid {
			b.Fatal(err)
		}
	}
}
