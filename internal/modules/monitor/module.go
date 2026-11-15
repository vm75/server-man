package monitor

import (
	"context"
	"io"

	"server-man/internal/core"
)

func Register(registry *core.Registry, sampler *Sampler) error {
	minimum, maximum := 1.0, 500.0
	return registry.AddModule(core.Module{ID: "monitor", Title: "Resource monitoring", Available: true},
		core.Tool{Name: "monitor_snapshot", Title: "Live resources", Description: "Read cached Linux CPU, memory, disk, network, sensors, load and uptime with up to 60 history points. Null rates mean the first sample, a reset counter, or unavailable data. Check lastError and at for stale data.", ReadOnly: true,
			Run: func(context.Context, core.Input, io.Writer) (any, error) { return sampler.Snapshot(), nil }},
		core.Tool{Name: "monitor_processes", Title: "Process activity", Description: "Read sampled process activity, sorted and filtered before limiting results. CPU uses 100% per logical core. Process command arguments and environments are never collected.", ReadOnly: true,
			Fields: map[string]core.Field{"sort": core.EnumField("Order by CPU (default), memory, PID, or name", false, "cpu", "memory", "pid", "name"), "filter": core.StringField("Case insensitive process name or PID filter", false, 128), "limit": {Type: "integer", Minimum: &minimum, Maximum: &maximum, Description: "Maximum rows, default 100"}},
			Run: func(ctx context.Context, input core.Input, out io.Writer) (any, error) {
				return sampler.Processes(input.String("sort"), input.String("filter"), int(input.Number("limit"))), nil
			}},
	)
}
