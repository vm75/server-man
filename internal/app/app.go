// Package app assembles management modules independently of their transports.
package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"serverman/internal/declarative"
	"time"

	"serverman/internal/admin"
	"serverman/internal/buildinfo"
	"serverman/internal/config"
	"serverman/internal/core"
	"serverman/internal/modules/monitor"
	"serverman/internal/modules/services"
	"serverman/internal/modules/shares"
	"serverman/internal/modules/updates"
)

func Registry(cfg config.Config, jobs *core.Jobs, monitoring *monitor.Sampler, runner core.Executor) (*core.Registry, error) {
	registry := core.NewRegistry(cfg.AllowWrites, jobs)
	startedAt := time.Now().UTC()
	var managed *declarative.Coordinator
	err := registry.AddModule(core.Module{ID: "system", Title: "Server", Available: true},
		core.Tool{Name: "system_capabilities", Title: "Backend capabilities", Description: "Read backend availability, unavailable reasons, and management write policy.", ReadOnly: true,
			Run: func(context.Context, core.Input, io.Writer) (any, error) {
				return map[string]any{"modules": registry.Modules(), "allowWrites": registry.AllowWrites}, nil
			}},
		core.Tool{Name: "system_status", Title: "Server status", Description: "Read the connected server identity and management write policy.", ReadOnly: true,
			Run: func(context.Context, core.Input, io.Writer) (any, error) {
				hostname, err := os.Hostname()
				if err != nil {
					return nil, err
				}
				return map[string]any{"hostname": hostname, "startedAt": startedAt, "version": buildinfo.Version, "allowWrites": registry.AllowWrites}, nil
			}},
		core.Tool{Name: "jobs_list", Title: "Recent jobs", Description: "List up to 64 recent management jobs, newest first. Fetch an individual job for output.", ReadOnly: true,
			Run: func(context.Context, core.Input, io.Writer) (any, error) { return jobs.List(), nil }},
		core.Tool{Name: "jobs_get", Title: "Job details", Description: "Read a management job's state, result, and bounded output.", ReadOnly: true, Fields: map[string]core.Field{"id": core.StringField("Job ID", true, 64)},
			Run: func(ctx context.Context, input core.Input, out io.Writer) (any, error) {
				return jobs.Get(input.String("id"))
			}},
		core.Tool{Name: "jobs_cancel", Title: "Cancel job", Description: "Request cancellation of a running management job. Completed host changes cannot be undone by cancellation.", Immediate: true, Fields: map[string]core.Field{"id": core.StringField("Job ID", true, 64)},
			Run: func(ctx context.Context, input core.Input, out io.Writer) (any, error) {
				if err := jobs.Cancel(input.String("id")); err != nil {
					return nil, err
				}
				return map[string]string{"status": "cancellation requested"}, nil
			}},
	)
	if err == nil {
		path := cfg.ManifestPath
		if path == "" {
			path = filepath.Join(cfg.StateDir, "server-man.yaml")
		}
		err = declarative.RegisterInspection(registry, declarative.Store{Path: path})
		if err == nil {
			coordinator := declarative.Coordinator{HealthCache: &declarative.HealthCache{}, Store: declarative.Store{Path: path}, Adapters: map[string]declarative.Adapter{"podman/quadlet": declarative.NewQuadlet(declarative.Store{Path: path}, runner), "podman/compose": declarative.NewCompose(declarative.Store{Path: path}, "podman", runner), "docker/compose": declarative.NewCompose(declarative.Store{Path: path}, "docker", runner), "libvirt": declarative.NewLibvirt(declarative.Store{Path: path}, cfg.LibvirtURI, runner), "incus": declarative.NewIncus(declarative.Store{Path: path}, runner)}}
			managed = &coordinator
			nativeShares := shares.New(cfg, runner)
			for _, protocol := range []string{"samba", "nfs"} {
				adapter := &declarative.ShareAdapter{Store: declarative.Store{Path: path}, Protocol: protocol, Native: nativeShares}
				if cfg.PrivilegedOperations {
					adapter.Helper = admin.Client{Runner: runner}
				}
				coordinator.Adapters[protocol] = adapter
			}
			err = declarative.RegisterCoordinator(registry, coordinator)
			if err == nil {
				err = declarative.RegisterLifecycle(registry, coordinator)
				if err == nil {
					err = declarative.RegisterStatus(registry, coordinator)
					if err == nil {
						err = declarative.RegisterShareStatus(registry, coordinator)
						if err == nil {
							err = declarative.RegisterEditing(registry, coordinator)
						}
					}
				}
			}
		}
	}
	if err == nil && monitoring != nil {
		err = monitor.Register(registry, monitoring)
	}
	if err == nil {
		err = services.New(cfg.SystemdScope, runner).Register(registry)
	}
	if err == nil {
		err = shares.New(cfg, runner).Register(registry)
	}
	if err == nil {
		err = updates.New(cfg, runner).Register(registry)
	}
	if err == nil {
		err = shares.RegisterSetup(registry, cfg, runner)
	}
	if err == nil && managed != nil {
		declarative.RouteManagedUnits(registry, *managed, cfg.SystemdScope)
	}
	if err == nil && cfg.PrivilegedOperations {
		admin.Route(registry, admin.Client{Runner: runner}, cfg.SystemdScope)
	}
	return registry, err
}
