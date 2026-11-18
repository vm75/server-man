package declarative

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// A common typed model is rendered by both Podman backends. Reject invalid or
// unsupported options before publication rather than silently dropping them.
var (
	containerIDMap = regexp.MustCompile(`^[0-9]+:[0-9]+:[1-9][0-9]*$`)
	containerIdentity = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]*$|^[0-9]+$`)
	containerCapability = regexp.MustCompile(`^(ALL|[A-Z][A-Z0-9_]*)$`)
	containerSize = regexp.MustCompile(`^[0-9]+([kKmMgGtT][bB]?)?$`)
	containerUserNS = regexp.MustCompile(`^(keep-id(:uid=[0-9]+(,gid=[0-9]+)?)?|host|private|auto|nomap)$`)
)

func validContainerPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\x00\r\n:") 
}

func containerDuration(s string) bool {
	if s == "" { return true }
	d, err := time.ParseDuration(s)
	return err == nil && d > 0
}

func validateContainerOptions(engine, backend string, containers map[string]Container) error {
	for name, v := range containers {
		fail := func(field string) error { return fmt.Errorf("invalid container %s %s", name, field) }
		if len(v.Entrypoint) > 32 || len(v.Devices) > 32 || len(v.Tmpfs) > 32 || len(v.DependsOn) > 32 || len(v.UIDMaps) > 64 || len(v.GIDMaps) > 64 || len(v.SecurityOpt) > 32 || len(v.LogOptions) > 32 {
			return fail("option count")
		}
		if v.User != "" {
			parts := strings.Split(v.User, ":")
			if len(parts) > 2 { return fail("user") }
			for _, part := range parts {
				if !containerIdentity.MatchString(part) { return fail("user") }
			}
		}
		if v.UserNSMode != "" && !containerUserNS.MatchString(v.UserNSMode) { return fail("userns_mode") }
		if v.UserNSMode != "" && (len(v.UIDMaps) > 0 || len(v.GIDMaps) > 0) { return fail("incompatible user namespace mappings") }
		for _, mapping := range append(append([]string{}, v.UIDMaps...), v.GIDMaps...) {
			if !containerIDMap.MatchString(mapping) { return fail("UID/GID mapping") }
		}
		if engine == "docker" && (len(v.UIDMaps) > 0 || len(v.GIDMaps) > 0 || v.UserNSMode == "nomap" || strings.HasPrefix(v.UserNSMode, "keep-id")) {
			return errors.New("Podman user namespace settings require the Podman engine")
		}
		for _, group := range v.GroupAdd {
			if group != "keep-groups" && !containerIdentity.MatchString(group) { return fail("group_add") }
			if group == "keep-groups" && engine != "podman" { return fail("keep-groups requires Podman") }
		}
		if v.NetworkMode != "" && v.NetworkMode != "host" && v.NetworkMode != "none" && !strings.HasPrefix(v.NetworkMode, "service:") { return fail("network_mode") }
		if strings.HasPrefix(v.NetworkMode, "service:") {
			peer := strings.TrimPrefix(v.NetworkMode, "service:")
			if peer == name || !validID(peer) { return fail("network_mode peer") }
			if _, ok := containers[peer]; !ok { return fail("network_mode peer") }
			if len(v.Ports) > 0 { return fail("published ports with shared network namespace") }
		}
		for dep, condition := range v.DependsOn {
			if dep == name { return fail("self dependency") }
			upstream, ok := containers[dep]
			if !ok || (condition != "started" && condition != "healthy") { return fail("depends_on") }
			if condition == "healthy" && upstream.Healthcheck == nil { return fail("healthy dependency without healthcheck") }
		}
		for _, device := range v.Devices {
			parts := strings.Split(device, ":")
			if len(parts) > 3 || !validContainerPath(parts[0]) { return fail("device path") }
			if len(parts) >= 2 && !validContainerPath(parts[1]) { return fail("device target") }
			if len(parts) == 3 && (parts[2] == "" || strings.Trim(parts[2], "rwm") != "") { return fail("device permissions") }
		}
		for _, cap := range append(append([]string{}, v.CapAdd...), v.CapDrop...) {
			if !containerCapability.MatchString(cap) { return fail("capability") }
		}
		for _, option := range v.SecurityOpt {
			switch option {
			case "no-new-privileges", "no-new-privileges:true", "no-new-privileges:false", "label=disable", "seccomp=unconfined", "seccomp:unconfined":
			default:
				return fail("security_opt (unsupported setting)")
			}
		}
		for _, tmp := range v.Tmpfs {
			head, _, _ := strings.Cut(tmp, ":")
			if !validContainerPath(head) || strings.ContainsAny(tmp, "\x00\r\n \t") { return fail("tmpfs") }
		}
		if v.WorkingDir != "" && !validContainerPath(v.WorkingDir) { return fail("working_dir") }
		for _, part := range v.Entrypoint {
			if !literal(part) || len(part) > 8192 { return fail("entrypoint") }
		}
		if v.ShmSize != "" && !containerSize.MatchString(v.ShmSize) { return fail("shm_size") }
		if !containerDuration(v.StopGracePeriod) { return fail("stop_grace_period") }
		if v.PullPolicy != "" && v.PullPolicy != "missing" && v.PullPolicy != "always" && v.PullPolicy != "never" && v.PullPolicy != "newer" { return fail("pull_policy") }
		if v.Restart != "" && v.Restart != "no" && v.Restart != "always" && v.Restart != "unless-stopped" && v.Restart != "on-failure" { return fail("restart") }
		if backend == "compose" && v.Restart != "" && v.Restart != "no" {
			return errors.New("Compose restart supervision is not supported with independent boot enablement; use Quadlet or omit restart")
		}
		if v.LogDriver != "" && v.LogDriver != "k8s-file" && v.LogDriver != "journald" && v.LogDriver != "none" && v.LogDriver != "passthrough" && v.LogDriver != "passthrough-tty" { return fail("log_driver") }
		for key, val := range v.LogOptions {
			if !envName.MatchString(strings.ReplaceAll(key, "-", "_")) || !literal(val) || len(val) > 4096 { return fail("log_options") }
		}
		if v.Healthcheck != nil {
			h := v.Healthcheck
			if len(h.Test) < 2 || len(h.Test) > 32 || (h.Test[0] != "CMD" && h.Test[0] != "CMD-SHELL") { return fail("healthcheck command") }
			if h.Test[0] == "CMD-SHELL" && len(h.Test) != 2 { return fail("healthcheck shell command") }
			if !containerDuration(h.Interval) || !containerDuration(h.Timeout) || !containerDuration(h.StartPeriod) || h.Retries < 0 || h.Retries > 1000 { return fail("healthcheck timing") }
			for _, arg := range h.Test[1:] { if !literal(arg) || len(arg) > 8192 { return fail("healthcheck argument") } }
		}
		for _, m := range v.Mounts {
			if m.SELinux != "" && m.SELinux != "private" && m.SELinux != "shared" { return fail("mount SELinux mode") }
			if m.Chown && engine != "podman" { return fail("mount :U requires Podman") }
		}
	}
	// A cycle would deadlock systemd's unit ordering and Compose startup.
	state := map[string]int{}
	var visit func(string) error
	visit = func(name string) error {
		if state[name] == 1 { return errors.New("container dependency cycle") }
		if state[name] == 2 { return nil }
		state[name] = 1
		v := containers[name]
		for dep := range v.DependsOn { if err := visit(dep); err != nil { return err } }
		if strings.HasPrefix(v.NetworkMode, "service:") {
			if err := visit(strings.TrimPrefix(v.NetworkMode, "service:")); err != nil { return err }
		}
		state[name] = 2
		return nil
	}
	for name := range containers { if err := visit(name); err != nil { return err } }
	return nil
}

func stopSeconds(s string) int {
	if s == "" { return 0 }
	d, _ := time.ParseDuration(s)
	return int((d + time.Second - 1) / time.Second)
}

func volumeOptions(m Mount) string {
	options := []string{}
	if m.ReadOnly { options = append(options, "ro") } else { options = append(options, "rw") }
	if m.Chown { options = append(options, "U") }
	switch m.SELinux {
	case "private": options = append(options, "Z")
	case "shared": options = append(options, "z")
	}
	return strings.Join(options, ",")
}

func effectiveNoNewPrivileges(v Container) bool {
	value := !v.Privileged
	for _, opt := range v.SecurityOpt {
		if opt == "no-new-privileges" || opt == "no-new-privileges:true" { value = true }
		if opt == "no-new-privileges:false" { value = false }
	}
	return value
}

func effectiveRestart(v Container) string {
	if v.Restart == "" { return "no" }
	return v.Restart
}

// Only accept integer map values and fixed security flags; unlike a raw
// PodmanArgs field, these settings cannot turn into arbitrary command options.
// Podman's native size parser expects single-letter binary units, whereas
// Compose commonly uses strings such as "1gb" and "256mb".
func podmanSize(s string) string { return strings.TrimSuffix(strings.ToLower(s), "b") }
