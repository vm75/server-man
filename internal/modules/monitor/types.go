// Package monitor samples visible Linux resources without a monitoring agent.
package monitor

import "time"

type Host struct {
	Hostname     string `json:"hostname"`
	OS           string `json:"os"`
	Kernel       string `json:"kernel"`
	Architecture string `json:"architecture"`
	CPUModel     string `json:"cpuModel"`
}

type Core struct {
	Name  string   `json:"name"`
	Usage *float64 `json:"usage"`
}

type CPU struct {
	Usage        *float64 `json:"usage"`
	Cores        []Core   `json:"cores"`
	FrequencyMHz *float64 `json:"frequencyMHz"`
}

type Memory struct {
	Total     uint64 `json:"total"`
	Available uint64 `json:"available"`
	Used      uint64 `json:"used"`
	Cached    uint64 `json:"cached"`
	Buffers   uint64 `json:"buffers"`
	SwapTotal uint64 `json:"swapTotal"`
	SwapUsed  uint64 `json:"swapUsed"`
}

type Disk struct {
	Device     string `json:"device"`
	Mount      string `json:"mount"`
	Filesystem string `json:"filesystem"`
	Total      uint64 `json:"total"`
	Used       uint64 `json:"used"`
	Available  uint64 `json:"available"`
}

type DiskIO struct {
	Name                string   `json:"name"`
	ReadBytesPerSecond  *float64 `json:"readBytesPerSecond"`
	WriteBytesPerSecond *float64 `json:"writeBytesPerSecond"`
}

type Network struct {
	Name             string   `json:"name"`
	RXBytes          uint64   `json:"rxBytes"`
	TXBytes          uint64   `json:"txBytes"`
	RXBytesPerSecond *float64 `json:"rxBytesPerSecond"`
	TXBytesPerSecond *float64 `json:"txBytesPerSecond"`
}

type Temperature struct {
	Name    string  `json:"name"`
	Celsius float64 `json:"celsius"`
}

type Process struct {
	PID     int      `json:"pid"`
	Name    string   `json:"name"`
	State   string   `json:"state"`
	CPU     *float64 `json:"cpu"`
	Memory  uint64   `json:"memory"`
	Threads int      `json:"threads"`
	Nice    int      `json:"nice"`
}

type Point struct {
	At            time.Time `json:"at"`
	CPU           *float64  `json:"cpu"`
	MemoryPercent float64   `json:"memoryPercent"`
	RX            *float64  `json:"rx"`
	TX            *float64  `json:"tx"`
}

type Snapshot struct {
	Valid            bool          `json:"valid"`
	At               time.Time     `json:"at"`
	IntervalSeconds  float64       `json:"intervalSeconds"`
	Host             Host          `json:"host"`
	CPU              CPU           `json:"cpu"`
	Memory           Memory        `json:"memory"`
	UptimeSeconds    *float64      `json:"uptimeSeconds"`
	Load             *[3]float64   `json:"load"`
	Disks            []Disk        `json:"disks"`
	DiskIO           []DiskIO      `json:"diskIO"`
	Networks         []Network     `json:"networks"`
	Temperatures     []Temperature `json:"temperatures"`
	ProcessCount     int           `json:"processCount"`
	ProcessesLimited bool          `json:"processesLimited"`
	History          []Point       `json:"history"`
	Warnings         []string      `json:"warnings"`
	LastError        string        `json:"lastError,omitempty"`
}

type ProcessList struct {
	At        time.Time `json:"at"`
	Processes []Process `json:"processes"`
	Matched   int       `json:"matched"`
	Limited   bool      `json:"limited"`
}

type cpuCounter [8]uint64
type counterPair struct{ first, second uint64 }
type processCounter struct {
	Process
	ticks, start uint64
}

type reading struct {
	snapshot  Snapshot
	cpu       map[string]cpuCounter
	network   map[string]counterPair
	disk      map[string]counterPair
	processes map[int]processCounter
}
