package declarative

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

type composeMount struct {
	Type     string          `yaml:"type"`
	Source   string          `yaml:"source"`
	Target   string          `yaml:"target"`
	ReadOnly bool            `yaml:"read_only"`
	Bind     map[string]bool `yaml:"bind"`
}
type composeContainer struct {
	Image       string            `yaml:"image"`
	Command     []string          `yaml:"command,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty"`
	EnvFile     []string          `yaml:"env_file,omitempty"`
	Ports       []string          `yaml:"ports,omitempty"`
	Volumes     []composeMount    `yaml:"volumes,omitempty"`
	Labels      map[string]string `yaml:"labels"`
	Restart     string            `yaml:"restart"`
	MemLimit    string            `yaml:"mem_limit,omitempty"`
	CPUs        int               `yaml:"cpus,omitempty"`
	SecurityOpt []string          `yaml:"security_opt"`
}

func composeProject(id string) string { return "sm-" + id + "-" + digest([]byte(id))[:12] }
func composeUnit(id string) string    { return "sm-compose-" + id + ".service" }
func literalCompose(s string) string  { return strings.ReplaceAll(s, "$", "$$") }
func RenderCompose(store Store, c Config, key string) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	kind, id, ok := strings.Cut(key, "/")
	s, exists := c.Services[id]
	if !ok || kind != "service" || !exists || s.Kind != "container" || s.Backend != "compose" {
		return nil, errors.New("resource is not a Compose application")
	}
	services := map[string]composeContainer{}
	for _, name := range slices.Sorted(maps.Keys(s.Containers)) {
		v := s.Containers[name]
		out := composeContainer{Image: literalCompose(v.Image), Ports: v.Ports, Restart: "no", CPUs: v.CPUs, SecurityOpt: []string{"no-new-privileges:true"}, Labels: map[string]string{"io.server-man.service": id, "io.server-man.container": name, "io.server-man.fingerprint": containerRuntimeFingerprint(c, key)}}
		for _, arg := range v.Command {
			out.Command = append(out.Command, literalCompose(arg))
		}
		if v.Environment != nil {
			out.Environment = map[string]string{}
			for k, value := range v.Environment {
				out.Environment[k] = literalCompose(value)
			}
		}
		for _, ref := range v.EnvFiles {
			path := store.Resolve(c.Secrets[ref])
			if !filepath.IsAbs(path) {
				return nil, errors.New("manifest path must be absolute")
			}
			out.EnvFile = append(out.EnvFile, literalCompose(path))
		}
		for _, m := range v.Mounts {
			path := store.Resolve(c.Storage[m.Storage].Path)
			if !filepath.IsAbs(path) {
				return nil, errors.New("manifest path must be absolute")
			}
			out.Volumes = append(out.Volumes, composeMount{"bind", literalCompose(path), literalCompose(m.Target), m.ReadOnly, map[string]bool{"create_host_path": false}})
		}
		if v.MemoryMiB > 0 {
			out.MemLimit = fmt.Sprintf("%dm", v.MemoryMiB)
		}
		services[name] = out
	}
	body, err := yaml.Marshal(struct {
		Name     string                      `yaml:"name"`
		Services map[string]composeContainer `yaml:"services"`
	}{composeProject(id), services})
	if err != nil {
		return nil, err
	}
	return append([]byte(ownedHeader), body...), nil
}
func systemdExecArg(s string) string { return unitValue(strings.ReplaceAll(s, "$", "$$")) }

// RenderComposeBoot creates a boot-only launcher. It never supervises containers
// and never restarts them at daemon restart; the generated Compose restart policy
// is explicitly "no". This keeps disabled boot policy independent of prior runs.
func RenderComposeBoot(c Config, key, program, file string) ([]byte, error) {
	_, id, _ := strings.Cut(key, "/")
	s := c.Services[id]
	if !filepath.IsAbs(program) || !filepath.IsAbs(file) {
		return nil, errors.New("Compose bootstrap paths must be absolute")
	}
	var b strings.Builder
	b.WriteString(ownedHeader + "[Unit]\nDescription=server-man Compose boot startup\n")
	for _, dep := range s.DependsOn {
		ds := c.Services[dep]
		if ds.Backend != "compose" || ds.Engine != s.Engine {
			return nil, errors.New("Compose dependencies currently require the same engine/backend")
		}
		fmt.Fprintf(&b, "Requires=%s\nAfter=%s\n", composeUnit(dep), composeUnit(dep))
	}
	b.WriteString("\n[Service]\nType=oneshot\nRemainAfterExit=no\nTimeoutStartSec=900\nEnvironment=COMPOSE_DISABLE_ENV_FILE=1\nEnvironment=COMPOSE_ANSI=never\nEnvironment=PODMAN_COMPOSE_WARNING_LOGS=false\n")
	args := []string{program, "compose", "--project-name", composeProject(id), "--project-directory", filepath.Dir(file), "--env-file", "/dev/null", "--file", file, "up", "--detach", "--no-build"}
	b.WriteString("ExecStart=")
	for i, arg := range args {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(systemdExecArg(arg))
	}
	b.WriteString("\n\n[Install]\nWantedBy=default.target\n")
	return []byte(b.String()), nil
}
