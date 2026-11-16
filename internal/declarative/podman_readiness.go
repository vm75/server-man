package declarative

import "encoding/json"

// Use Podman's effective configuration and its own helper discovery rather than
// assuming the server process and engine search the same executable paths.
func podmanInfoIssues(output, backend string, requireCgroupsV2 bool) []Issue {
	var info struct {
		Host struct {
			CgroupVersion      string `json:"cgroupVersion"`
			RootlessNetworkCmd string `json:"rootlessNetworkCmd"`
			Security           struct {
				Rootless *bool `json:"rootless"`
			} `json:"security"`
			Pasta struct {
				Executable string `json:"executable"`
			} `json:"pasta"`
			Slirp struct {
				Executable string `json:"executable"`
			} `json:"slirp4netns"`
		} `json:"host"`
	}
	issue := func(field, code, message, remedy string) Issue {
		return Issue{"error", "backend/podman/" + backend, field, code, message, remedy}
	}
	if json.Unmarshal([]byte(output), &info) != nil || info.Host.Security.Rootless == nil {
		return []Issue{issue("runtime", "runtime_info_unavailable", "Podman runtime prerequisites could not be read.", "Check Podman installation and runtime access, then recheck configuration.")}
	}
	var issues []Issue
	if requireCgroupsV2 && info.Host.CgroupVersion != "v2" {
		issues = append(issues, issue("cgroups", "unsupported_cgroups", "Quadlet requires cgroup v2.", "Configure a host with cgroup v2 before activating Quadlet services."))
	}
	if !*info.Host.Security.Rootless {
		return issues
	}
	var executable, name, packages string
	switch info.Host.RootlessNetworkCmd {
	case "pasta":
		executable, name, packages = info.Host.Pasta.Executable, "pasta", "passt"
	case "slirp4netns":
		executable, name, packages = info.Host.Slirp.Executable, "slirp4netns", "slirp4netns"
	default:
		return append(issues, issue("network", "network_helper_unknown", "Podman's effective rootless networking helper could not be checked.", "Check Podman's rootless network configuration and installed networking helper, then recheck."))
	}
	if executable == "" {
		issues = append(issues, issue("network", "missing_dependency", "Podman's configured rootless networking helper is missing: "+name+".", "Install the native "+packages+" package through Software or rerun the installer with Podman selected, then recheck configuration."))
	}
	return issues
}
