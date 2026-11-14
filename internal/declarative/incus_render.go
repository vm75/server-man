package declarative

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

var bindingNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

type incusDefinition struct {
	Architecture string                       `json:"architecture,omitempty"`
	Description  string                       `json:"description"`
	Config       map[string]string            `json:"config"`
	Devices      map[string]map[string]string `json:"devices"`
	Profiles     []string                     `json:"profiles"`
	Ephemeral    bool                         `json:"ephemeral"`
}

func incusInstance(id string) string {
	if len(id) <= 60 && !strings.Contains(id, "_") {
		return "sm-" + id
	}
	name := strings.ReplaceAll(id, "_", "-")
	if len(name) > 49 {
		name = name[:49]
	}
	return "sm-" + name + "-" + digest([]byte(id))[:10]
}
func incusProject(c Config, id string) (string, error) {
	s := c.Services[id]
	if s.VM == nil {
		return "", errors.New("missing Incus VM definition")
	}
	b, ok := c.Bindings[s.VM.Project]
	if !ok || b.Kind != "project" || !bindingNamePattern.MatchString(b.Name) {
		return "", errors.New("Incus requires an explicit valid project binding")
	}
	return b.Name, nil
}
func RenderIncus(c Config, key string) (incusDefinition, error) {
	if err := c.Validate(); err != nil {
		return incusDefinition{}, err
	}
	kind, id, ok := strings.Cut(key, "/")
	s, exists := c.Services[id]
	if !ok || kind != "service" || !exists || s.Kind != "vm" || s.Backend != "incus" {
		return incusDefinition{}, errors.New("resource is not an Incus VM")
	}
	v := s.VM
	if v.UUID == "" || v.MAC == "" {
		return incusDefinition{}, errors.New("persistent VM identity is required")
	}
	if _, err := incusProject(c, id); err != nil {
		return incusDefinition{}, err
	}
	pool, ok := c.Bindings[v.Pool]
	if !ok || pool.Kind != "pool" || !bindingNamePattern.MatchString(pool.Name) {
		return incusDefinition{}, errors.New("Incus requires an explicit valid pool binding")
	}
	network := c.Bindings[v.Network]
	if !bindingNamePattern.MatchString(network.Name) {
		return incusDefinition{}, errors.New("invalid Incus network binding")
	}
	if len(v.Disks) > 0 {
		return incusDefinition{}, errors.New("Incus custom disk mapping is not implemented; root storage is managed by the pool")
	}
	if len(s.DependsOn) > 0 {
		return incusDefinition{}, errors.New("Incus boot dependency integration is not implemented")
	}
	if v.Machine != "" {
		return incusDefinition{}, errors.New("Incus machine-type overrides are unsupported")
	}
	if v.Firmware != "" && v.Firmware != "uefi" {
		return incusDefinition{}, errors.New("initial Incus VM profile requires UEFI")
	}
	arch := v.Architecture
	if arch != "" && arch != "x86_64" && arch != "aarch64" {
		return incusDefinition{}, errors.New("unsupported Incus architecture")
	}
	size := v.RootSizeGiB
	if size == 0 {
		size = 20
	}
	settings := map[string]string{"limits.cpu": strconv.Itoa(v.CPUs), "limits.memory": fmt.Sprintf("%dMiB", v.MemoryMiB), "boot.autostart": strconv.FormatBool(s.Enabled), "security.secureboot": "true", "user.server-man.id": id, "user.server-man.identity": v.UUID, "user.server-man.fingerprint": resourceFingerprint(c, key), "user.server-man.runtime": runtimeFingerprint(c, key)}
	nic := map[string]string{"type": "nic", "name": "eth0", "hwaddr": v.MAC}
	if network.Kind == "network" {
		nic["network"] = network.Name
	} else {
		nic["nictype"] = "bridged"
		nic["parent"] = network.Name
	}
	return incusDefinition{Architecture: arch, Description: s.Description, Config: settings, Devices: map[string]map[string]string{"root": {"type": "disk", "path": "/", "pool": pool.Name, "size": fmt.Sprintf("%dGiB", size)}, "eth0": nic}, Profiles: []string{}, Ephemeral: false}, nil
}
func encodeIncus(d incusDefinition) ([]byte, error) {
	data, err := yaml.Marshal(d)
	if err != nil {
		return nil, err
	}
	return append([]byte(ownedHeader), append(data, '\n')...), nil
}
