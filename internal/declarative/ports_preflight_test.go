package declarative

import (
	"strings"
	"testing"
)

func TestManagedHostPortOverlapIsScopedAndNonBlocking(t *testing.T) {
	c := Config{Services: map[string]Service{
		"alpha": {Kind: "container", Engine: "podman", Backend: "quadlet", Containers: map[string]Container{
			"web": {Ports: []string{"8080:80", "5353:53/udp", "80", "8080:81"}},
		}},
		"beta": {Kind: "container", Engine: "docker", Backend: "compose", Containers: map[string]Container{
			"api": {Ports: []string{"8080:8080/tcp", "5353:53/tcp"}},
		}},
		"gamma": {Kind: "container", Engine: "podman", Backend: "compose", Containers: map[string]Container{
			"dns": {Ports: []string{"5353:53/udp", "9090:90"}},
		}},
	}}
	issues := publishedPortIssues(c)
	if len(issues) != 4 {
		t.Fatalf("expected one warning per overlapping container/port: %+v", issues)
	}
	seen := map[string]bool{}
	for _, issue := range issues {
		if issue.Severity != "warning" || issue.Code != "host_port_overlap" || !strings.HasPrefix(issue.Field, "containers.") {
			t.Fatalf("unexpected overlap severity or field: %+v", issue)
		}
		key := issue.Resource + ":" + issue.Field + ":" + issue.Message
		if seen[key] {
			t.Fatal("duplicate issue for repeated host port", issue)
		}
		seen[key] = true
	}
	for _, want := range []string{"service/alpha", "service/beta", "service/gamma"} {
		found := false
		for _, issue := range issues {
			if issue.Resource == want {
				found = true
			}
		}
		if !found {
			t.Fatal("missing affected application", want)
		}
	}
	// Distinct protocols and engine-selected ephemeral host ports are not
	// collisions. A VM has no container host-publish surface.
	c.Services["alpha"] = Service{Kind: "container", Containers: map[string]Container{"web": {Ports: []string{"8080:80/udp", "80"}}}}
	c.Services["gamma"] = Service{Kind: "vm"}
	if issues := publishedPortIssues(c); len(issues) != 0 {
		t.Fatal("different protocols or implicit host ports produced a conflict", issues)
	}
}
