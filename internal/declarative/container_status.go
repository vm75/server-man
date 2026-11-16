package declarative

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

type ContainerMemberStatus struct {
	Name string `json:"name"`
	State string `json:"state"`
}

// Native unit activity remains conservative for apply/removal safety. Inventory
// separately describes how many desired container members are actually running.
func observeContainerGroup(obs *Observation, c Config, key string, containers []managedContainer) error {
	_, id, _ := strings.Cut(key, "/")
	expected := c.Services[id].Containers
	running, seen := map[string]bool{}, map[string]bool{}
	nativeStates := map[string]string{}
	fingerprint := runtimeFingerprint(c, key)
	for _, container := range containers {
		if seen[container.Name] {
			return errors.New("duplicate managed container identity")
		}
		seen[container.Name] = true
		if _, managed := expected[container.Name]; managed {
			state := container.State
			switch state {
			case "running", "created", "exited", "stopped", "paused", "restarting", "dead", "configured":
			default:
				state = "unknown"
			}
			nativeStates[container.Name] = state
		}
		if container.State != "running" {
			continue
		}
		obs.Active = true
		if container.Fingerprint != fingerprint {
			obs.ActiveMatches = false
		}
		if _, exists := expected[container.Name]; exists {
			running[container.Name] = true
		}
	}
	// Return only configured member names, sorted and without native labels,
	// environment or other potentially sensitive engine data. A missing row
	// means the member is not reported running, not that its data is absent.
	obs.Members = make([]ContainerMemberStatus, 0, len(expected))
	for _, name := range slices.Sorted(maps.Keys(expected)) {
		state := nativeStates[name]
		if state == "" { state = "not_running" }
		obs.Members = append(obs.Members, ContainerMemberStatus{Name: name, State: state})
	}
	obs.State = "stopped"
	if len(running) == len(expected) && len(running) > 0 {
		obs.State = "running"
	} else if len(running) > 0 {
		obs.State = "partially_running"
		obs.Issues = append(obs.Issues, Issue{"warning", key, "runtime", "partial_container_group", fmt.Sprintf("%d of %d configured containers are running.", len(running), len(expected)), "Check the container runtime. Use Start to start missing members, or Stop to stop the application."})
	} else if obs.Active {
		// A live/activating unit or an old member may still need safe shutdown,
		// but it does not establish that a desired container is running.
		obs.State = "unknown"
		obs.ActiveMatches = false
		obs.Issues = append(obs.Issues, Issue{"warning", key, "runtime", "container_state_mismatch", "No configured containers are running, but native activity is still present.", "Wait for startup to finish and recheck. If it does not finish, inspect the container runtime and use Start or Stop to recover."})
	}
	return nil
}

type managedContainer struct{ Name, State, Fingerprint string }

func parseManagedContainers(output string) ([]managedContainer, error) {
	var rows []json.RawMessage
	if strings.HasPrefix(strings.TrimSpace(output), "[") {
		if json.Unmarshal([]byte(output), &rows) != nil {
			return nil, errors.New("invalid engine inventory")
		}
	} else {
		decoder := json.NewDecoder(strings.NewReader(output))
		for {
			var row json.RawMessage
			err := decoder.Decode(&row)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, errors.New("invalid engine inventory")
			}
			rows = append(rows, row)
			if len(rows) > 4096 {
				return nil, errors.New("engine inventory limit exceeded")
			}
		}
	}
	if len(rows) > 4096 {
		return nil, errors.New("engine inventory limit exceeded")
	}
	result := []managedContainer{}
	for _, row := range rows {
		var item struct {
			Labels json.RawMessage
			State  string
		}
		if json.Unmarshal(row, &item) != nil {
			return nil, errors.New("invalid container inventory row")
		}
		labels := map[string]string{}
		if json.Unmarshal(item.Labels, &labels) != nil {
			var labelString string
			if json.Unmarshal(item.Labels, &labelString) != nil {
				return nil, errors.New("unsupported engine labels")
			}
			for _, pair := range strings.Split(labelString, ",") {
				k, v, ok := strings.Cut(pair, "=")
				if ok {
					labels[k] = v
				}
			}
		}
		result = append(result, managedContainer{labels["io.server-man.container"], strings.ToLower(item.State), labels["io.server-man.fingerprint"]})
	}
	return result, nil
}
