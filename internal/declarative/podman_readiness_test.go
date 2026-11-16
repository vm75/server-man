package declarative

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"server-man/internal/core"
)

const podmanNetworkFixture = `"networkBackendInfo":{"backend":"netavark","path":"/fixture/netavark","dns":{"path":"/fixture/aardvark-dns"}}`

func TestPodmanEffectiveNetworkPrerequisites(t *testing.T) {
	for _, tc := range []struct {
		name, cmd, pasta, slirp, code string
		rootless                      bool
	}{
		{"missing pasta", "pasta", "", "/usr/bin/slirp4netns", "missing_dependency", true},
		{"pasta installed", "pasta", "/usr/bin/pasta", "", "", true},
		{"configured slirp", "slirp4netns", "", "/usr/bin/slirp4netns", "", true},
		{"missing slirp", "slirp4netns", "/usr/bin/pasta", "", "missing_dependency", true},
		{"rootful", "", "", "", "", false},
		{"unknown selection", "unrecognized-private-value", "", "", "network_helper_unknown", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := fmt.Sprintf(`{"host":{%s,"cgroupVersion":"v2","security":{"rootless":%t},"rootlessNetworkCmd":%q,"pasta":{"executable":%q},"slirp4netns":{"executable":%q}}}`, podmanNetworkFixture, tc.rootless, tc.cmd, tc.pasta, tc.slirp)
			for _, backend := range []string{"quadlet", "compose"} {
				issues := podmanInfoIssues(output, backend, backend == "quadlet")
				if tc.code == "" {
					if len(issues) != 0 {
						t.Fatal(issues)
					}
				} else if len(issues) != 1 || issues[0].Code != tc.code || issues[0].Resource != "backend/podman/"+backend || issues[0].Remedy == "" {
					t.Fatal(issues)
				}
				if strings.Contains(fmt.Sprint(issues), "unrecognized-private-value") {
					t.Fatal("runtime metadata leaked")
				}
			}
		})
	}
	for _, output := range []string{"not-json private-value", "null", "{}", `{"host":{"security":{"rootless":"private-value"}}}`} {
		issues := podmanInfoIssues(output, "quadlet", true)
		if len(issues) != 1 || issues[0].Code != "runtime_info_unavailable" || strings.Contains(fmt.Sprint(issues), "private-value") {
			t.Fatal(issues)
		}
	}
	output := fmt.Sprintf(`{"host":{%s,"cgroupVersion":"v1","security":{"rootless":false}}}`, podmanNetworkFixture)
	if issues := podmanInfoIssues(output, "quadlet", true); len(issues) != 1 || issues[0].Code != "unsupported_cgroups" {
		t.Fatal(issues)
	}
	if issues := podmanInfoIssues(output, "compose", false); len(issues) != 0 {
		t.Fatal(issues)
	}
}

func TestPodmanApplicationNetworkDependencies(t *testing.T) {
	for _, tc := range []struct{ backend, path, dns, field, code string }{
		{"netavark", "/fixture/netavark", "/fixture/dns", "", ""},
		{"netavark", "", "/fixture/dns", "networkBackend", "missing_dependency"},
		{"netavark", "/fixture/netavark", "", "dns", "missing_dependency"},
		{"private-unknown-backend", "", "", "networkBackend", "unsupported_network_backend"},
		{"", "", "", "networkBackend", "network_backend_unknown"},
	} {
		for _, rootless := range []bool{true, false} {
			info := map[string]any{"host": map[string]any{"security": map[string]bool{"rootless": rootless}, "cgroupVersion": "v2", "rootlessNetworkCmd": "pasta", "pasta": map[string]string{"executable": "/fixture/pasta"}, "networkBackendInfo": map[string]any{"backend": tc.backend, "path": tc.path, "dns": map[string]string{"path": tc.dns}}}}
			raw, err := json.Marshal(info)
			if err != nil {
				t.Fatal(err)
			}
			for _, backend := range []string{"quadlet", "compose"} {
				issues := podmanInfoIssues(string(raw), backend, backend == "quadlet")
				if tc.code == "" {
					if len(issues) != 0 {
						t.Fatal(issues)
					}
				} else if len(issues) != 1 || issues[0].Code != tc.code || issues[0].Field != tc.field || issues[0].Remedy == "" {
					t.Fatal(issues)
				}
				if strings.Contains(fmt.Sprint(issues), "private-unknown-backend") {
					t.Fatal("native metadata leaked")
				}
			}
		}
	}
}

type podmanInfoRecorder struct{ commands []core.Command }

func (r *podmanInfoRecorder) Run(context.Context, core.Command, io.Writer) error { return nil }
func (r *podmanInfoRecorder) Capture(_ context.Context, cmd core.Command) (string, error) {
	r.commands = append(r.commands, cmd)
	if cmd.Program == "podman" && cmd.Args[0] == "info" {
		return fmt.Sprintf(`{"host":{%s,"cgroupVersion":"v2","security":{"rootless":true},"rootlessNetworkCmd":"pasta","pasta":{"executable":""}}}`, podmanNetworkFixture), nil
	}
	return "", nil
}
func TestBothPodmanAdaptersReportMissingNetworkingHelper(t *testing.T) {
	r := &podmanInfoRecorder{}
	q := &Quadlet{Podman: "podman", Generator: "quadlet", Runner: r}
	p := &Compose{Engine: "podman", Program: "podman", Runner: r}
	for _, issues := range [][]Issue{q.Prerequisites(context.Background()), p.Prerequisites(context.Background())} {
		found := false
		for _, issue := range issues {
			if issue.Field == "network" && issue.Code == "missing_dependency" && strings.Contains(issue.Remedy, "passt") {
				found = true
			}
		}
		if !found {
			t.Fatal(issues)
		}
	}
	probes := 0
	for _, cmd := range r.commands {
		if cmd.Program == "podman" && cmd.Args[0] == "info" {
			probes++
			if strings.Join(cmd.Args, " ") != "info --format json" {
				t.Fatal(cmd)
			}
		}
	}
	if probes != 2 {
		t.Fatal("redundant or missing runtime probes", r.commands)
	}
}
