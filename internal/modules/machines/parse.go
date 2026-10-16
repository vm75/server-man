package machines

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

func machine(backend, name, state, kind string) Machine {
	return Machine{Backend: backend, Name: name, State: strings.ToLower(state), Kind: kind, Manageable: validName(backend, name), IPs: []string{}}
}

func parseLXD(text string) ([]Machine, error) {
	var rows []struct {
		Name         string `json:"name"`
		Status       string `json:"status"`
		Type         string `json:"type"`
		Architecture string `json:"architecture"`
		Location     string `json:"location"`
		State        *struct {
			Memory struct {
				Usage *uint64 `json:"usage"`
			} `json:"memory"`
			Network map[string]struct {
				Addresses []struct {
					Address string `json:"address"`
					Scope   string `json:"scope"`
				} `json:"addresses"`
			} `json:"network"`
		} `json:"state"`
	}
	if !strings.HasPrefix(strings.TrimSpace(text), "[") {
		return nil, errors.New("expected instance array")
	}
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		return nil, err
	}
	if len(rows) > 1024 {
		return nil, errors.New("instance inventory exceeds 1024")
	}
	result := []Machine{}
	for _, row := range rows {
		if row.Name == "" || row.Status == "" {
			return nil, errors.New("instance is missing identity/state")
		}
		m := machine("lxd", row.Name, row.Status, row.Type)
		if m.Kind == "" {
			m.Kind = "container"
		}
		m.Architecture, m.Location = row.Architecture, row.Location
		if row.State != nil {
			m.MemoryBytes = row.State.Memory.Usage
			for _, network := range row.State.Network {
				for _, address := range network.Addresses {
					if address.Scope == "global" {
						m.IPs = append(m.IPs, address.Address)
					}
				}
			}
			slices.Sort(m.IPs)
		}
		result = append(result, m)
	}
	sortMachines(result)
	return result, nil
}

func parseLXC(text string) ([]Machine, error) {
	result := []Machine{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "NAME" || strings.Trim(fields[0], "-") == "" {
			continue
		}
		if len(fields) != 2 {
			return nil, errors.New("unsupported LXC inventory row")
		}
		if !slices.Contains([]string{"STOPPED", "STARTING", "RUNNING", "STOPPING", "ABORTING", "FREEZING", "FROZEN", "THAWED"}, fields[1]) {
			return nil, errors.New("unsupported LXC state")
		}
		result = append(result, machine("lxc", fields[0], fields[1], "container"))
		if len(result) > 1024 {
			return nil, errors.New("LXC inventory exceeds 1024")
		}
	}
	sortMachines(result)
	return result, nil
}

func parseKVM(text string) ([]Machine, error) {
	result := []Machine{}
	states := map[string]string{"0": "unknown", "1": "running", "2": "blocked", "3": "paused", "4": "shutting down", "5": "shut off", "6": "crashed", "7": "suspended"}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Domain: '") && strings.HasSuffix(line, "'") {
			name := strings.TrimSuffix(strings.TrimPrefix(line, "Domain: '"), "'")
			if name == "" {
				return nil, errors.New("domain has no name")
			}
			result = append(result, machine("kvm", name, "unknown", "virtual-machine"))
			if len(result) > 1024 {
				return nil, errors.New("domain inventory exceeds 1024")
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || len(result) == 0 {
			return nil, errors.New("unsupported domain statistics")
		}
		m := &result[len(result)-1]
		switch key {
		case "state.state":
			if state, ok := states[value]; ok {
				m.State = state
			}
		case "balloon.current":
			if n, err := strconv.ParseUint(value, 10, 54); err == nil {
				n *= 1024
				m.MemoryBytes = &n
			}
		case "vcpu.current":
			if n, err := strconv.Atoi(value); err == nil && n >= 0 {
				m.CPUs = &n
			}
		}
	}
	sortMachines(result)
	return result, nil
}

func parseDetails(backend, text string) (Machine, error) {
	fields := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == "IP" {
			fields[key] += value + " "
		} else {
			fields[key] = value
		}
	}
	if fields["Name"] == "" || fields["State"] == "" {
		return Machine{}, fmt.Errorf("%s returned unsupported machine details", backend)
	}
	m := machine(backend, fields["Name"], fields["State"], "container")
	if backend == "kvm" {
		m.Kind, m.UUID = "virtual-machine", fields["UUID"]
		if enabled := fields["Autostart"]; enabled == "enable" || enabled == "disable" {
			value := enabled == "enable"
			m.Autostart = &value
		}
		if persistent := fields["Persistent"]; persistent == "yes" || persistent == "no" {
			value := persistent == "yes"
			m.Persistent = &value
		}
		if n, err := strconv.Atoi(fields["CPU(s)"]); err == nil && n >= 0 {
			m.CPUs = &n
		}
		parts := strings.Fields(fields["Used memory"])
		if len(parts) == 2 && parts[1] == "KiB" {
			if n, err := strconv.ParseUint(parts[0], 10, 54); err == nil {
				n *= 1024
				m.MemoryBytes = &n
			}
		}
	} else {
		m.IPs = strings.Fields(fields["IP"])
	}
	return m, nil
}

func sortMachines(items []Machine) {
	slices.SortFunc(items, func(a, b Machine) int { return strings.Compare(a.Name, b.Name) })
}
