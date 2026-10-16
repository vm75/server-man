package compose

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type Container struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Service string `json:"service"`
	Image   string `json:"image"`
	State   string `json:"state"`
	Status  string `json:"status"`
	Health  string `json:"health"`
	Ports   string `json:"ports"`
	PodID   string `json:"podID,omitempty"`
	PodName string `json:"podName,omitempty"`
	Infra   bool   `json:"infra"`
	labels  map[string]string
	project string
}
type Pod struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	State      string `json:"state"`
	Containers int    `json:"containers"`
}

func objects(output string) ([]json.RawMessage, error) {
	data := bytes.TrimSpace([]byte(output))
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		return []json.RawMessage{}, nil
	}
	if data[0] == '[' {
		var values []json.RawMessage
		err := json.Unmarshal(data, &values)
		return values, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	values := []json.RawMessage{}
	for {
		var value json.RawMessage
		err := decoder.Decode(&value)
		if err == io.EOF {
			return values, nil
		}
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		if len(values) > 4096 {
			return nil, errors.New("container inventory exceeds limit")
		}
	}
}

func parseContainers(output string) ([]Container, error) {
	values, err := objects(output)
	if err != nil {
		return nil, err
	}
	if len(values) > 4096 {
		return nil, errors.New("container inventory exceeds limit")
	}
	result := make([]Container, 0, len(values))
	for _, value := range values {
		var row struct {
			ID         string
			Name       string
			Names      json.RawMessage
			Service    string
			Project    string
			Image      string
			State      string
			Status     string
			Health     string
			Ports      json.RawMessage
			Pod        string
			PodName    string
			IsInfra    bool
			Labels     json.RawMessage
			Publishers []struct {
				URL           string
				PublishedPort int
				TargetPort    int
				Protocol      string
			}
		}
		if err := json.Unmarshal(value, &row); err != nil || row.ID == "" {
			return nil, errors.New("container status JSON is invalid")
		}
		name := row.Name
		if name == "" && len(row.Names) > 0 {
			if json.Unmarshal(row.Names, &name) != nil {
				var names []string
				if json.Unmarshal(row.Names, &names) == nil && len(names) > 0 {
					name = names[0]
				}
			}
		}
		labels := map[string]string{}
		if len(row.Labels) > 0 && !bytes.Equal(row.Labels, []byte("null")) {
			if json.Unmarshal(row.Labels, &labels) != nil {
				var text string
				if json.Unmarshal(row.Labels, &text) != nil {
					return nil, errors.New("container labels JSON is invalid")
				}
				labels = map[string]string{}
				for pair := range strings.SplitSeq(text, ",") {
					if key, value, ok := strings.Cut(pair, "="); ok {
						labels[key] = value
					}
				}
			}
		}
		service := row.Service
		if service == "" {
			service = labels["com.docker.compose.service"]
			if service == "" {
				service = labels["io.podman.compose.service"]
			}
		}
		project := row.Project
		if project == "" {
			project = labels["com.docker.compose.project"]
			if project == "" {
				project = labels["io.podman.compose.project"]
			}
		}
		ports := ""
		if len(row.Ports) > 0 && !bytes.Equal(row.Ports, []byte("null")) {
			if json.Unmarshal(row.Ports, &ports) != nil {
				var entries []struct {
					HostIP        string `json:"host_ip"`
					HostPort      int    `json:"host_port"`
					ContainerPort int    `json:"container_port"`
					Protocol      string `json:"protocol"`
				}
				if json.Unmarshal(row.Ports, &entries) != nil {
					return nil, errors.New("container ports JSON is invalid")
				}
				parts := []string{}
				for _, entry := range entries {
					parts = append(parts, port(entry.HostIP, entry.HostPort, entry.ContainerPort, entry.Protocol))
				}
				ports = strings.Join(parts, ", ")
			}
		}
		if len(row.Publishers) > 0 {
			parts := []string{}
			for _, entry := range row.Publishers {
				parts = append(parts, port(entry.URL, entry.PublishedPort, entry.TargetPort, entry.Protocol))
			}
			ports = strings.Join(parts, ", ")
		}
		health := row.Health
		if health == "" {
			if strings.Contains(row.Status, "(unhealthy)") {
				health = "unhealthy"
			} else if strings.Contains(row.Status, "(healthy)") {
				health = "healthy"
			}
		}
		result = append(result, Container{ID: row.ID, Name: strings.TrimPrefix(name, "/"), Service: service, Image: row.Image, State: strings.ToLower(row.State), Status: row.Status, Health: health, Ports: ports, PodID: row.Pod, PodName: row.PodName, Infra: row.IsInfra, labels: labels, project: project})
	}
	slices.SortFunc(result, func(a, b Container) int {
		if a.Service != b.Service {
			return strings.Compare(a.Service, b.Service)
		}
		return strings.Compare(a.Name, b.Name)
	})
	return result, nil
}

func port(ip string, host, target int, protocol string) string {
	if protocol == "" {
		protocol = "tcp"
	}
	value := strconv.Itoa(target) + "/" + protocol
	if host > 0 {
		if ip == "" {
			ip = "0.0.0.0"
		}
		value = net.JoinHostPort(ip, strconv.Itoa(host)) + " → " + value
	}
	return value
}

func matches(container Container, project Project) bool {
	if directory := container.labels["com.docker.compose.project.working_dir"]; directory != "" {
		return filepath.Clean(directory) == filepath.Clean(project.Directory) || (project.realDirectory != "" && filepath.Clean(directory) == project.realDirectory)
	}
	if files := container.labels["com.docker.compose.project.config_files"]; files != "" {
		paths := strings.Split(files, ",")
		return slices.Contains(paths, project.File) || (project.realFile != "" && slices.Contains(paths, project.realFile))
	}
	return container.project != "" && container.project == project.Name
}

func summarize(containers []Container) (total, running int, state string) {
	if len(containers) == 0 {
		return 0, 0, "not_created"
	}
	paused, degraded := 0, false
	for _, container := range containers {
		if container.State == "running" {
			running++
		}
		if container.State == "paused" {
			paused++
		}
		degraded = degraded || container.Health == "unhealthy" || container.State == "dead" || container.State == "restarting"
	}
	switch {
	case degraded:
		state = "degraded"
	case running == len(containers):
		state = "running"
	case paused == len(containers):
		state = "paused"
	case running > 0:
		state = "partial"
	default:
		state = "stopped"
	}
	return len(containers), running, state
}

func parsePods(output string, containers []Container) ([]Pod, error) {
	values, err := objects(output)
	if err != nil {
		return nil, err
	}
	result := []Pod{}
	for _, value := range values {
		var row struct {
			ID                 string
			Name               string
			Status             string
			NumContainers      int
			NumberOfContainers int
			Containers         []json.RawMessage
		}
		if err := json.Unmarshal(value, &row); err != nil || row.ID == "" {
			return nil, errors.New("invalid pod inventory")
		}
		if !slices.ContainsFunc(containers, func(c Container) bool { return c.PodID == row.ID }) {
			continue
		}
		result = append(result, Pod{ID: row.ID, Name: row.Name, State: strings.ToLower(row.Status), Containers: max(row.NumContainers, row.NumberOfContainers, len(row.Containers))})
	}
	return result, nil
}
