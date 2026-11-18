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
	Short    string          `yaml:"-"`
}

// Podman Compose accepts SELinux :Z/:z and ownership :U as short bind
// options. Keep the non-creating long representation for ordinary mounts.
func (m composeMount) MarshalYAML() (any, error) {
	if m.Short != "" { return m.Short, nil }
	return struct {
		Type string `yaml:"type"`
		Source string `yaml:"source"`
		Target string `yaml:"target"`
		ReadOnly bool `yaml:"read_only"`
		Bind map[string]bool `yaml:"bind"`
	}{m.Type, m.Source, m.Target, m.ReadOnly, m.Bind}, nil
}
type composeDependency struct {
	Condition string `yaml:"condition"`
}
type composeHealth struct {
	Test []string `yaml:"test"`
	Interval string `yaml:"interval,omitempty"`
	Timeout string `yaml:"timeout,omitempty"`
	Retries int `yaml:"retries,omitempty"`
	StartPeriod string `yaml:"start_period,omitempty"`
}
type composeLogging struct {
	Driver string `yaml:"driver,omitempty"`
	Options map[string]string `yaml:"options,omitempty"`
}
type composePodman struct {
	InPod bool `yaml:"in_pod"`
}
type composeContainer struct {
	Image           string            `yaml:"image"`
	ContainerName   string            `yaml:"container_name,omitempty"`
	Command         []string          `yaml:"command,omitempty"`
	Entrypoint      []string          `yaml:"entrypoint,omitempty"`
	WorkingDir      string            `yaml:"working_dir,omitempty"`
	Environment     map[string]string `yaml:"environment,omitempty"`
	EnvFile         []string          `yaml:"env_file,omitempty"`
	Ports           []string          `yaml:"ports,omitempty"`
	Volumes         []composeMount    `yaml:"volumes,omitempty"`
	Labels          map[string]string `yaml:"labels"`
	Restart         string            `yaml:"restart"`
	MemLimit        string            `yaml:"mem_limit,omitempty"`
	CPUs            int               `yaml:"cpus,omitempty"`
	SecurityOpt     []string          `yaml:"security_opt"`
	User            string            `yaml:"user,omitempty"`
	UserNSMode      string            `yaml:"userns_mode,omitempty"`
	UIDMaps         []string          `yaml:"x-podman.uidmaps,omitempty"`
	GIDMaps         []string          `yaml:"x-podman.gidmaps,omitempty"`
	GroupAdd        []string          `yaml:"group_add,omitempty"`
	NetworkMode     string            `yaml:"network_mode,omitempty"`
	DependsOn       map[string]composeDependency `yaml:"depends_on,omitempty"`
	Devices         []string          `yaml:"devices,omitempty"`
	CapAdd          []string          `yaml:"cap_add,omitempty"`
	CapDrop         []string          `yaml:"cap_drop,omitempty"`
	Privileged      bool              `yaml:"privileged,omitempty"`
	ReadOnly        bool              `yaml:"read_only,omitempty"`
	Init            bool              `yaml:"init,omitempty"`
	TTY             bool              `yaml:"tty,omitempty"`
	Tmpfs           []string          `yaml:"tmpfs,omitempty"`
	ShmSize         string            `yaml:"shm_size,omitempty"`
	StopGracePeriod string            `yaml:"stop_grace_period,omitempty"`
	PullPolicy      string            `yaml:"pull_policy,omitempty"`
	Logging         *composeLogging   `yaml:"logging,omitempty"`
	Healthcheck     *composeHealth    `yaml:"healthcheck,omitempty"`
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
		out := composeContainer{Image: literalCompose(v.Image), ContainerName: v.ContainerName, Ports: v.Ports, Restart: "no", CPUs: v.CPUs, SecurityOpt: []string{}, Labels: map[string]string{"io.server-man.service": id, "io.server-man.container": name, "io.server-man.fingerprint": runtimeFingerprint(c, key)},
			Entrypoint: append([]string(nil), v.Entrypoint...), WorkingDir: literalCompose(v.WorkingDir), User: v.User, UserNSMode: v.UserNSMode, UIDMaps: v.UIDMaps, GIDMaps: v.GIDMaps, GroupAdd: v.GroupAdd,
			NetworkMode: v.NetworkMode, Devices: append([]string(nil), v.Devices...), CapAdd: v.CapAdd, CapDrop: v.CapDrop, Privileged: v.Privileged, ReadOnly: v.ReadOnly, Init: v.Init, TTY: v.TTY,
			Tmpfs: append([]string(nil), v.Tmpfs...), ShmSize: v.ShmSize, StopGracePeriod: v.StopGracePeriod, PullPolicy: v.PullPolicy}
		for i, item := range out.Entrypoint { out.Entrypoint[i] = literalCompose(item) }
		for i, item := range out.Devices { out.Devices[i] = literalCompose(item) }
		for i, item := range out.Tmpfs { out.Tmpfs[i] = literalCompose(item) }
		if effectiveNoNewPrivileges(v) { out.SecurityOpt = append(out.SecurityOpt, "no-new-privileges:true") }
		for _, opt := range v.SecurityOpt {
			if !strings.HasPrefix(opt, "no-new-privileges") { out.SecurityOpt = append(out.SecurityOpt, opt) }
		}
		if len(v.DependsOn) > 0 {
			out.DependsOn = map[string]composeDependency{}
			for dep, condition := range v.DependsOn { out.DependsOn[dep] = composeDependency{Condition:"service_"+condition} }
		}
		if v.Healthcheck != nil {
			h := v.Healthcheck
			out.Healthcheck = &composeHealth{Test: append([]string(nil), h.Test...), Interval: h.Interval, Timeout: h.Timeout, Retries: h.Retries, StartPeriod: h.StartPeriod}
			for i, arg := range out.Healthcheck.Test { out.Healthcheck.Test[i] = literalCompose(arg) }
		}
		if v.LogDriver != "" || len(v.LogOptions) > 0 {
			out.Logging = &composeLogging{Driver:v.LogDriver, Options:map[string]string{}}
			for k,val := range v.LogOptions { out.Logging.Options[k] = literalCompose(val) }
		}
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
			mount := composeMount{Type:"bind", Source:literalCompose(path), Target:literalCompose(m.Target), ReadOnly:m.ReadOnly, Bind:map[string]bool{"create_host_path":false}}
			if m.SELinux != "" || m.Chown {
				if strings.Contains(path, ":") || strings.Contains(m.Target, ":") {
					return nil, errors.New("SELinux or ownership mount path cannot be represented by Compose short syntax")
				}
				mount.Short = literalCompose(path + ":" + m.Target + ":" + volumeOptions(m))
			}
			out.Volumes = append(out.Volumes, mount)
		}
		if v.MemoryMiB > 0 {
			out.MemLimit = fmt.Sprintf("%dm", v.MemoryMiB)
		}
		services[name] = out
	}
	var podman *composePodman
	if s.Engine == "podman" {
		// Independent containers are necessary for custom UID/GID mappings
		// and per-container namespace choices. The provider may otherwise
		// create one pod with shared user/network namespaces.
		podman = &composePodman{InPod: false}
	}
	body, err := yaml.Marshal(struct {
		Name     string                      `yaml:"name"`
		Services map[string]composeContainer `yaml:"services"`
		Podman   *composePodman               `yaml:"x-podman,omitempty"`
	}{composeProject(id), services, podman})
	if err != nil {
		return nil, err
	}
	return append([]byte(ownedHeader), body...), nil
}
func systemdExecArg(s string) string { return unitValue(strings.ReplaceAll(s, "$", "$$")) }

// Both Compose providers resolve generated definitions from the explicit cwd.
// --project-directory is unnecessary here and is not supported by podman-compose.
func composeArguments(file, id string, args ...string) []string {
	return append([]string{"compose", "--project-name", composeProject(id), "--env-file", "/dev/null", "--file", file}, args...)
}

func composeEnvironment(engine string) []string {
	env := []string{"COMPOSE_ANSI=never", "COMPOSE_DISABLE_ENV_FILE=1", "PODMAN_COMPOSE_WARNING_LOGS=false"}
	if engine == "podman" {
		// Podman otherwise prefers Docker's Compose plugin when both are installed,
		// introducing an implicit API-socket dependency absent from podman-compose.
		env = append(env, "PODMAN_COMPOSE_PROVIDER=podman-compose")
	}
	return env
}

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
	if s.Engine == "podman" {
		// User units cannot order against the system network-online.target.
		// Use the same packaged bridge that Quadlet adds for rootless boot.
		fmt.Fprintf(&b, "Wants=%s\nAfter=%s\n", podmanNetworkOnlineUnit, podmanNetworkOnlineUnit)
	}
	for _, dep := range s.DependsOn {
		ds := c.Services[dep]
		if ds.Backend != "compose" || ds.Engine != s.Engine {
			return nil, errors.New("Compose dependencies currently require the same engine/backend")
		}
		fmt.Fprintf(&b, "Requires=%s\nAfter=%s\n", composeUnit(dep), composeUnit(dep))
	}
	// Keep the launcher cgroup alive: podman-compose's detached children can
	// remain in it. An inactive oneshot would immediately terminate them.
	b.WriteString("\n[Service]\nType=oneshot\nRemainAfterExit=yes\nTimeoutStartSec=900\n")
	for _, env := range composeEnvironment(s.Engine) {
		fmt.Fprintf(&b, "Environment=%s\n", unitValue(env))
	}
	directory, err := unitScalarValue(filepath.Dir(file))
	if err != nil {
		return nil, errors.New("Compose working directory contains unsupported whitespace or path escapes")
	}
	b.WriteString("WorkingDirectory=" + directory + "\n")
	args := append([]string{program}, composeArguments(file, id, "up", "--detach", "--no-build")...)
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
