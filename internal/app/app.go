// Package app assembles management modules independently of their transports.
package app

import (
	"context"
	"io"
	"os"
	"time"

	"serverman/internal/core"
)

func Registry(allowWrites bool, jobs *core.Jobs) (*core.Registry, error) {
	registry := core.NewRegistry(allowWrites, jobs)
	startedAt := time.Now().UTC()
	err := registry.AddModule(core.Module{ID: "system", Title: "Server", Available: true},
		core.Tool{Name: "system_status", Title: "Server status", Description: "Read the connected server identity and management write policy.", ReadOnly: true,
			Run: func(context.Context, core.Input, io.Writer) (any, error) {
				hostname, err := os.Hostname()
				if err != nil {
					return nil, err
				}
				return map[string]any{"hostname": hostname, "startedAt": startedAt, "version": "dev", "allowWrites": registry.AllowWrites}, nil
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
	return registry, err
}
