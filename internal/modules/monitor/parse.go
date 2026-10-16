package monitor

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func parseCPU(text string) (map[string]cpuCounter, error) {
	result := map[string]cpuCounter{}
	for line := range strings.SplitSeq(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if !strings.HasPrefix(fields[0], "cpu") {
			break
		}
		if len(fields) < 5 {
			return nil, errors.New("incomplete CPU counters")
		}
		var value cpuCounter
		// guest and guest_nice are already included in user and nice.
		for i := 1; i < min(len(fields), 9); i++ {
			number, err := strconv.ParseUint(fields[i], 10, 64)
			if err != nil {
				return nil, errors.New("invalid CPU counter")
			}
			value[i-1] = number
		}
		result[fields[0]] = value
	}
	if _, ok := result["cpu"]; !ok {
		return nil, errors.New("aggregate CPU counters are missing")
	}
	return result, nil
}

func parseMemory(text string) (Memory, error) {
	values := map[string]uint64{}
	for line := range strings.SplitSeq(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		number, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		values[strings.TrimSuffix(fields[0], ":")] = number * 1024
	}
	total := values["MemTotal"]
	if total == 0 {
		return Memory{}, errors.New("memory total is missing")
	}
	available, ok := values["MemAvailable"]
	if !ok {
		available = values["MemFree"] + values["Buffers"] + values["Cached"] + values["SReclaimable"]
	}
	available = min(total, available)
	return Memory{Total: total, Available: available, Used: total - available, Cached: values["Cached"] + values["SReclaimable"], Buffers: values["Buffers"], SwapTotal: values["SwapTotal"], SwapUsed: values["SwapTotal"] - min(values["SwapTotal"], values["SwapFree"])}, nil
}

func parseNetwork(text string) (map[string]counterPair, error) {
	result := map[string]counterPair{}
	for line := range strings.SplitSeq(text, "\n") {
		name, data, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(data)
		if len(fields) < 16 {
			return nil, errors.New("incomplete network counters")
		}
		rx, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return nil, errors.New("invalid network receive counter")
		}
		tx, err := strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			return nil, errors.New("invalid network transmit counter")
		}
		result[strings.TrimSpace(name)] = counterPair{rx, tx}
	}
	return result, nil
}

func parseProcess(text string, pageSize uint64) (processCounter, error) {
	open, close := strings.IndexByte(text, '('), strings.LastIndexByte(text, ')')
	if open < 1 || close < open {
		return processCounter{}, errors.New("invalid process stat")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(text[:open]))
	if err != nil {
		return processCounter{}, err
	}
	fields := strings.Fields(text[close+1:])
	if len(fields) < 22 {
		return processCounter{}, errors.New("incomplete process stat")
	}
	values := map[int]int64{}
	for _, index := range []int{11, 12, 16, 17, 19, 21} {
		number, err := strconv.ParseInt(fields[index], 10, 64)
		if err != nil {
			return processCounter{}, fmt.Errorf("invalid process field %d", index+3)
		}
		values[index] = number
	}
	if values[11] < 0 || values[12] < 0 || values[19] < 0 {
		return processCounter{}, errors.New("negative process counter")
	}
	return processCounter{Process: Process{PID: pid, Name: text[open+1 : close], State: fields[0], Memory: uint64(max(0, values[21])) * pageSize, Threads: int(values[17]), Nice: int(values[16])}, ticks: uint64(values[11] + values[12]), start: uint64(values[19])}, nil
}

func cpuDelta(current, previous cpuCounter) (usage *float64, total uint64) {
	var idle uint64
	for i, value := range current {
		if value < previous[i] {
			// Linux can decrement iowait. Clamp that component, reset other counters.
			if i == 4 {
				continue
			}
			return nil, 0
		}
		difference := value - previous[i]
		total += difference
		if i == 3 || i == 4 {
			idle += difference
		}
	}
	if total == 0 {
		return nil, 0
	}
	value := min(100.0, max(0.0, 100*float64(total-idle)/float64(total)))
	return &value, total
}

func rate(current, previous uint64, seconds float64, exists bool) *float64 {
	if !exists || current < previous || seconds <= 0 {
		return nil
	}
	value := float64(current-previous) / seconds
	return &value
}
