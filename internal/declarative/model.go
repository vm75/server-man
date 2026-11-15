// Package declarative owns the portable workload definition independently of transports.
package declarative

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

const MaxConfigBytes = 1 << 20

type Config struct {
	Version  int                `yaml:"version" json:"version"`
	Services map[string]Service `yaml:"services,omitempty" json:"services"`
	Storage  map[string]Storage `yaml:"storage,omitempty" json:"storage"`
	Shares   map[string]Share   `yaml:"shares,omitempty" json:"shares"`
	Bindings map[string]Binding `yaml:"bindings,omitempty" json:"bindings"`
	Secrets  map[string]string  `yaml:"secrets,omitempty" json:"secrets"`
}
type Storage struct {
	Format   string `yaml:"format,omitempty" json:"format,omitempty"`
	Path     string `yaml:"path" json:"path"`
	External bool   `yaml:"external,omitempty" json:"external"`
	Kind     string `yaml:"kind,omitempty" json:"kind"`
	Mount    string `yaml:"mount,omitempty" json:"mount,omitempty"`
}
type Binding struct {
	Kind string `yaml:"kind" json:"kind"`
	Name string `yaml:"name" json:"name"`
}
type Service struct {
	Name        string               `yaml:"name,omitempty" json:"name,omitempty"`
	Description string               `yaml:"description,omitempty" json:"description,omitempty"`
	Kind        string               `yaml:"kind" json:"kind"`
	Engine      string               `yaml:"engine,omitempty" json:"engine,omitempty"`
	Backend     string               `yaml:"backend,omitempty" json:"backend"`
	Enabled     bool                 `yaml:"enabled" json:"enabled"`
	DependsOn   []string             `yaml:"depends_on,omitempty" json:"dependsOn,omitempty"`
	Containers  map[string]Container `yaml:"containers,omitempty" json:"containers,omitempty"`
	VM          *VM                  `yaml:"vm,omitempty" json:"vm,omitempty"`
}
type Container struct {
	Image       string            `yaml:"image" json:"image"`
	Command     []string          `yaml:"command,omitempty" json:"command,omitempty"`
	Ports       []string          `yaml:"ports,omitempty" json:"ports,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty" json:"environment,omitempty"`
	EnvFiles    []string          `yaml:"env_files,omitempty" json:"envFiles,omitempty"`
	Mounts      []Mount           `yaml:"mounts,omitempty" json:"mounts,omitempty"`
	MemoryMiB   int               `yaml:"memory_mib,omitempty" json:"memoryMiB,omitempty"`
	CPUs        int               `yaml:"cpus,omitempty" json:"cpus,omitempty"`
}
type Mount struct {
	Storage  string `yaml:"storage" json:"storage"`
	Target   string `yaml:"target" json:"target"`
	ReadOnly bool   `yaml:"read_only,omitempty" json:"readOnly"`
}
type VM struct {
	RootSizeGiB  int      `yaml:"root_size_gib,omitempty" json:"rootSizeGiB,omitempty"`
	Architecture string   `yaml:"architecture,omitempty" json:"architecture,omitempty"`
	Machine      string   `yaml:"machine,omitempty" json:"machine,omitempty"`
	Firmware     string   `yaml:"firmware,omitempty" json:"firmware,omitempty"`
	UUID         string   `yaml:"uuid,omitempty" json:"uuid,omitempty"`
	MAC          string   `yaml:"mac,omitempty" json:"mac,omitempty"`
	Image        string   `yaml:"image,omitempty" json:"image,omitempty"`
	CPUs         int      `yaml:"cpus" json:"cpus"`
	MemoryMiB    int      `yaml:"memory_mib" json:"memoryMiB"`
	Disks        []string `yaml:"disks,omitempty" json:"disks,omitempty"`
	Network      string   `yaml:"network" json:"network"`
	Pool         string   `yaml:"pool,omitempty" json:"pool,omitempty"`
	Project      string   `yaml:"project,omitempty" json:"project,omitempty"`
}
type Share struct {
	Guest      bool     `yaml:"guest,omitempty" json:"guest"`
	Browseable bool     `yaml:"browseable,omitempty" json:"browseable"`
	RootSquash *bool    `yaml:"root_squash,omitempty" json:"rootSquash,omitempty"`
	Security   string   `yaml:"security,omitempty" json:"security,omitempty"`
	Protocol   string   `yaml:"protocol" json:"protocol"`
	Storage    string   `yaml:"storage" json:"storage"`
	Enabled    bool     `yaml:"enabled" json:"enabled"`
	ReadOnly   bool     `yaml:"read_only" json:"readOnly"`
	Users      []string `yaml:"users,omitempty" json:"users,omitempty"`
	Clients    []string `yaml:"clients,omitempty" json:"clients,omitempty"`
}

// Parse deliberately rejects aliases, custom tags and duplicate keys before typed
// decoding. Parser diagnostics never include scalar values (which may be private).
func Parse(data []byte) (Config, error) {
	var c Config
	if len(data) > MaxConfigBytes {
		return c, errors.New("configuration exceeds 1 MiB")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var node yaml.Node
	if err := dec.Decode(&node); err != nil {
		return c, errors.New("invalid YAML configuration")
	}
	if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return c, errors.New("configuration must be one mapping")
	}
	var check func(*yaml.Node) error
	check = func(n *yaml.Node) error {
		if n.Kind == yaml.AliasNode || n.Anchor != "" || n.Style&yaml.TaggedStyle != 0 {
			return fmt.Errorf("unsupported YAML construct at line %d", n.Line)
		}
		if n.Kind == yaml.MappingNode {
			seen := map[string]bool{}
			for i := 0; i < len(n.Content); i += 2 {
				k := n.Content[i]
				if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || seen[k.Value] {
					return fmt.Errorf("invalid or duplicate mapping key at line %d", k.Line)
				}
				seen[k.Value] = true
			}
		}
		for _, v := range n.Content {
			if err := check(v); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(&node); err != nil {
		return c, err
	}
	var extra yaml.Node
	if dec.Decode(&extra) != io.EOF {
		return c, errors.New("configuration must contain one document")
	}
	typed := yaml.NewDecoder(bytes.NewReader(data))
	typed.KnownFields(true)
	if typed.Decode(&c) != nil {
		return c, errors.New("configuration contains an unknown field or incorrect field type")
	}
	return c, c.Validate()
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

func validID(id string) bool { return idPattern.MatchString(id) }
func portablePath(p string) bool {
	return p != "" && !filepath.IsAbs(p) && filepath.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../") && !strings.ContainsAny(p, "\x00\r\n")
}
func (c *Config) Validate() error {
	if c.Version != 1 {
		return errors.New("unsupported configuration version; expected 1")
	}
	if len(c.Services) > 128 || len(c.Storage) > 256 || len(c.Shares) > 64 {
		return errors.New("configuration resource limit exceeded")
	}
	paths := map[string]bool{}
	for id, s := range c.Storage {
		if !validID(id) || (s.Kind != "" && s.Kind != "directory" && s.Kind != "file") {
			return errors.New("invalid storage definition")
		}
		if (!s.External && !portablePath(s.Path)) || (s.External && (!filepath.IsAbs(s.Path) || filepath.Clean(s.Path) != s.Path)) || strings.ContainsAny(s.Path, "\x00\r\n") {
			return errors.New("invalid storage path")
		}
		if paths[s.Path] {
			return errors.New("duplicate storage path")
		}
		paths[s.Path] = true
	}
	for id, p := range c.Secrets {
		if !validID(id) || !portablePath(p) {
			return errors.New("invalid secret reference")
		}
	}
	for id, b := range c.Bindings {
		if !validID(id) || b.Name == "" || strings.ContainsAny(b.Name, "\x00\r\n") {
			return errors.New("invalid host binding")
		}
		switch b.Kind {
		case "network", "bridge", "pool", "project":
		default:
			return errors.New("invalid binding kind")
		}
	}
	for id, s := range c.Services {
		if !validID(id) {
			return errors.New("invalid service ID")
		}
		switch s.Kind {
		case "container":
			if s.VM != nil || len(s.Containers) == 0 || len(s.Containers) > 32 {
				return errors.New("container application requires containers and no VM")
			}
			if s.Engine == "" {
				s.Engine = "podman"
			}
			if s.Backend == "" {
				if s.Engine == "docker" {
					s.Backend = "compose"
				} else {
					s.Backend = "quadlet"
				}
			}
			if (s.Engine != "podman" && s.Engine != "docker") || (s.Backend != "compose" && s.Backend != "quadlet") || (s.Engine == "docker" && s.Backend != "compose") {
				return errors.New("unsupported container engine/backend")
			}
			for name, v := range s.Containers {
				if !validID(name) || v.Image == "" || v.MemoryMiB < 0 || v.CPUs < 0 {
					return errors.New("invalid container definition")
				}
				for _, m := range v.Mounts {
					if _, ok := c.Storage[m.Storage]; !ok {
						return errors.New("container references missing storage")
					}
					if !filepath.IsAbs(m.Target) || filepath.Clean(m.Target) != m.Target {
						return errors.New("invalid mount target")
					}
				}
				for _, ref := range v.EnvFiles {
					if _, ok := c.Secrets[ref]; !ok {
						return errors.New("missing environment file reference")
					}
				}
			}
		case "vm":
			if s.Engine != "" || len(s.Containers) != 0 || s.VM == nil || (s.Backend != "libvirt" && s.Backend != "incus") {
				return errors.New("invalid VM backend or definition")
			}
			v := s.VM
			if v.CPUs < 1 || v.MemoryMiB < 128 {
				return errors.New("invalid VM resources")
			}
			if _, ok := c.Bindings[v.Network]; !ok {
				return errors.New("VM references missing network binding")
			}
			for _, disk := range v.Disks {
				if st, ok := c.Storage[disk]; !ok || st.Kind != "file" {
					return errors.New("VM disk must reference file storage")
				}
			}
		default:
			return errors.New("unsupported service kind")
		}
		c.Services[id] = s
	}
	state := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return errors.New("service dependency cycle")
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		s := c.Services[id]
		for _, d := range s.DependsOn {
			dep, ok := c.Services[d]
			if !ok {
				return errors.New("missing service dependency")
			}
			if s.Enabled && !dep.Enabled {
				return errors.New("enabled service depends on disabled service")
			}
			if err := visit(d); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for id := range c.Services {
		if err := visit(id); err != nil {
			return err
		}
	}
	for id, s := range c.Shares {
		if !validID(id) || (s.Protocol != "samba" && s.Protocol != "nfs") {
			return errors.New("invalid share definition")
		}
		st, ok := c.Storage[s.Storage]
		if !ok || st.Kind == "file" {
			return errors.New("share requires directory storage")
		}
		if s.Protocol == "samba" && !s.Guest && len(s.Users) == 0 {
			return errors.New("Samba share requires access users")
		}
		if s.Protocol == "nfs" && len(s.Clients) == 0 {
			return errors.New("NFS share requires clients")
		}
	}
	return c.validateFields()
}
func Encode(c Config) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return yaml.Marshal(c)
}

// Invalid YAML remains available for private repair/history, but partially
// decoded fields must never authorize native removals or identity decisions.
func previousConfiguration(raw []byte) Config {
	cfg, err := Parse(raw)
	if err != nil {
		return Config{Version: 1}
	}
	return cfg
}
