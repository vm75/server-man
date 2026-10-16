package monitor

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const historyLimit = 60

type Sampler struct {
	mu        sync.RWMutex
	previous  *reading
	current   Snapshot
	history   []Point
	processes []Process
	interval  time.Duration
	cancel    context.CancelFunc
	done      chan struct{}
}

func New(ctx context.Context, interval time.Duration) *Sampler {
	interval = min(10*time.Second, max(time.Second, interval))
	ctx, cancel := context.WithCancel(ctx)
	source := newSource()
	s := &Sampler{interval: interval, cancel: cancel, done: make(chan struct{})}
	sample := func() {
		reading, err := source.collect(ctx)
		s.mu.Lock()
		defer s.mu.Unlock()
		if err != nil {
			s.current.LastError = err.Error()
			return
		}
		s.accept(reading)
	}
	sample()
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sample()
			}
		}
	}()
	return s
}

// accept runs with the sampler lock held. Readers receive immutable sample data.
func (s *Sampler) accept(r reading) {
	seconds := 0.0
	if s.previous != nil {
		seconds = r.snapshot.At.Sub(s.previous.snapshot.At).Seconds()
	}
	var cpuTicks uint64
	if s.previous != nil {
		r.snapshot.CPU.Usage, cpuTicks = cpuDelta(r.cpu["cpu"], s.previous.cpu["cpu"])
	}
	coreNames := make([]string, 0, len(r.cpu))
	for name := range r.cpu {
		if name != "cpu" {
			coreNames = append(coreNames, name)
		}
	}
	sort.Slice(coreNames, func(i, j int) bool {
		a, _ := strconv.Atoi(strings.TrimPrefix(coreNames[i], "cpu"))
		b, _ := strconv.Atoi(strings.TrimPrefix(coreNames[j], "cpu"))
		return a < b
	})
	r.snapshot.CPU.Cores = make([]Core, 0, len(coreNames))
	for _, name := range coreNames {
		core := Core{Name: name}
		if s.previous != nil {
			if previous, ok := s.previous.cpu[name]; ok {
				core.Usage, _ = cpuDelta(r.cpu[name], previous)
			}
		}
		r.snapshot.CPU.Cores = append(r.snapshot.CPU.Cores, core)
	}
	point := Point{At: r.snapshot.At, CPU: r.snapshot.CPU.Usage, MemoryPercent: 100 * float64(r.snapshot.Memory.Used) / float64(r.snapshot.Memory.Total)}
	rxUnavailable, txUnavailable := false, false
	for _, name := range sortedKeys(r.network) {
		counter := r.network[name]
		var previous counterPair
		exists := false
		if s.previous != nil {
			previous, exists = s.previous.network[name]
		}
		rx, tx := rate(counter.first, previous.first, seconds, exists), rate(counter.second, previous.second, seconds, exists)
		r.snapshot.Networks = append(r.snapshot.Networks, Network{Name: name, RXBytes: counter.first, TXBytes: counter.second, RXBytesPerSecond: rx, TXBytesPerSecond: tx})
		if name != "lo" {
			rxUnavailable = rxUnavailable || rx == nil
			txUnavailable = txUnavailable || tx == nil
			if rx != nil {
				if point.RX == nil {
					point.RX = new(float64)
				}
				*point.RX += *rx
			}
			if tx != nil {
				if point.TX == nil {
					point.TX = new(float64)
				}
				*point.TX += *tx
			}
		}
	}
	if rxUnavailable {
		point.RX = nil
	}
	if txUnavailable {
		point.TX = nil
	}
	for _, name := range sortedKeys(r.disk) {
		counter := r.disk[name]
		var previous counterPair
		exists := false
		if s.previous != nil {
			previous, exists = s.previous.disk[name]
		}
		r.snapshot.DiskIO = append(r.snapshot.DiskIO, DiskIO{Name: name, ReadBytesPerSecond: rate(counter.first, previous.first, seconds, exists), WriteBytesPerSecond: rate(counter.second, previous.second, seconds, exists)})
	}
	s.processes = make([]Process, 0, len(r.processes))
	for pid, process := range r.processes {
		if s.previous != nil && cpuTicks > 0 && seconds > 0 {
			previous, exists := s.previous.processes[pid]
			if exists && process.start == previous.start && process.ticks >= previous.ticks {
				// Ratio of jiffies avoids assuming a fixed USER_HZ on the host.
				value := 100 * float64(process.ticks-previous.ticks) * float64(len(coreNames)) / float64(cpuTicks)
				value = min(100*float64(len(coreNames)), max(0, value))
				process.CPU = &value
			}
		}
		s.processes = append(s.processes, process.Process)
	}
	r.snapshot.IntervalSeconds = s.interval.Seconds()
	if len(s.history) > 0 && s.history[len(s.history)-1].At.After(r.snapshot.At) {
		s.history = nil
	}
	cutoff := r.snapshot.At.Add(-historyLimit * s.interval)
	first := 0
	for first < len(s.history) && s.history[first].At.Before(cutoff) {
		first++
	}
	if first > 0 {
		copy(s.history, s.history[first:])
		s.history = s.history[:len(s.history)-first]
	}
	s.history = append(s.history, point)
	if len(s.history) > historyLimit {
		copy(s.history, s.history[len(s.history)-historyLimit:])
		s.history = s.history[:historyLimit]
	}
	s.current = r.snapshot
	s.previous = &r
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func (s *Sampler) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := s.current
	result.History = append([]Point{}, s.history...)
	return result
}

func (s *Sampler) Processes(order, filter string, limit int) ProcessList {
	s.mu.RLock()
	processes := append([]Process{}, s.processes...)
	result := ProcessList{At: s.current.At, Limited: s.current.ProcessesLimited}
	s.mu.RUnlock()
	filter = strings.ToLower(strings.TrimSpace(filter))
	processes = slices.DeleteFunc(processes, func(p Process) bool {
		return filter != "" && !strings.Contains(strings.ToLower(p.Name), filter) && !strings.Contains(strconv.Itoa(p.PID), filter)
	})
	if limit < 1 {
		limit = 100
	}
	limit = min(500, limit)
	slices.SortFunc(processes, func(a, b Process) int {
		switch order {
		case "pid":
			return a.PID - b.PID
		case "name":
			if cmp := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); cmp != 0 {
				return cmp
			}
		case "memory":
			if a.Memory > b.Memory {
				return -1
			}
			if a.Memory < b.Memory {
				return 1
			}
		default:
			aCPU, bCPU := -1.0, -1.0
			if a.CPU != nil {
				aCPU = *a.CPU
			}
			if b.CPU != nil {
				bCPU = *b.CPU
			}
			if aCPU > bCPU {
				return -1
			}
			if aCPU < bCPU {
				return 1
			}
			if a.Memory > b.Memory {
				return -1
			}
			if a.Memory < b.Memory {
				return 1
			}
		}
		return a.PID - b.PID
	})
	result.Matched = len(processes)
	result.Processes = processes[:min(limit, len(processes))]
	return result
}

func (s *Sampler) Close() {
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
	}
}
