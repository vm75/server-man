package declarative

import (
	"maps"
	"slices"
	"strings"
)

// Port declarations with explicit host ports may collide between managed
// containers, irrespective of provider. An unqualified container port asks
// the engine to choose an ephemeral host port and is not a static conflict.
// These warnings do not prevent running the applications at different times.
func publishedPortIssues(c Config) []Issue {
	type owner struct {
		resource, field string
	}
	declared := map[string][]owner{}
	for _, id := range slices.Sorted(maps.Keys(c.Services)) {
		service := c.Services[id]
		if service.Kind != "container" {
			continue
		}
		for _, name := range slices.Sorted(maps.Keys(service.Containers)) {
			for _, mapping := range service.Containers[name].Ports {
				port, protocol, explicitProtocol := strings.Cut(mapping, "/")
				parts := strings.Split(port, ":")
				if len(parts) != 2 {
					continue
				}
				if !explicitProtocol {
					protocol = "tcp"
				}
				key := protocol + "/" + parts[0]
				declared[key] = append(declared[key], owner{"service/" + id, "containers." + name + ".ports"})
			}
		}
	}
	issues := []Issue{}
	for _, port := range slices.Sorted(maps.Keys(declared)) {
		owners := declared[port]
		if len(owners) < 2 {
			continue
		}
		seen := map[owner]bool{}
		for _, current := range owners {
			if seen[current] {
				continue
			}
			seen[current] = true
			issues = append(issues, Issue{
				Severity: "warning", Resource: current.resource, Field: current.field, Code: "host_port_overlap",
				Message: "Managed host port " + port + " is declared by multiple containers; simultaneous starts may fail.",
				Remedy:  "Choose distinct host ports for concurrent applications or stop the other application before starting this one.",
			})
		}
	}
	return issues
}
