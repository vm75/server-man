package declarative

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"server-man/internal/core"
)

// ManagedLogs returns only explicitly requested, bounded native output.
// Runtime logs may contain application secrets and are never included in inventory.
type ManagedLogs struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
	Source    string `json:"source"`
}

type ManagedLogAdapter interface {
	Logs(context.Context, Config, string, int) (ManagedLogs, error)
}

func (c Coordinator) ServiceLogs(ctx context.Context, id string, tail int) (ManagedLogs, error) {
	if !validID(id) {
		return ManagedLogs{}, core.Invalid("Invalid managed application ID")
	}
	if tail == 0 {
		tail = 200
	}
	if tail < 1 || tail > 2000 {
		return ManagedLogs{}, core.Invalid("Log lines must be between 1 and 2000")
	}
	snapshot, err := c.Store.Read()
	if err != nil {
		return ManagedLogs{}, core.Unavailable("Configuration is invalid or inaccessible; repair it before reading managed logs")
	}
	if _, exists := snapshot.Config.Services[id]; !exists {
		return ManagedLogs{}, &core.Error{Code: "not_found", Message: "Managed application not found", Status: 404}
	}
	key := "service/" + id
	reader, ok := c.Adapters[backend(snapshot.Config, key)].(ManagedLogAdapter)
	if !ok {
		return ManagedLogs{}, core.Unavailable("This backend has no scoped managed log reader; use the guest's own console or native guest logging")
	}
	return reader.Logs(ctx, snapshot.Config, key, tail)
}

func boundedManagedLogs(ctx context.Context, runner core.Executor, command core.Command, source string) (ManagedLogs, error) {
	if command.Program == "" || runner == nil {
		return ManagedLogs{}, core.Unavailable("Native log reader is unavailable")
	}
	command.Timeout = 10 * time.Second
	output := core.NewTail(128 << 10)
	if err := runner.Run(ctx, command, output); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ManagedLogs{}, err
		}
		return ManagedLogs{}, core.Unavailable("Managed logs could not be read; check backend availability and account permissions")
	}
	text, truncated := output.Snapshot()
	return ManagedLogs{Text: text, Truncated: truncated, Source: source}, nil
}

// Quadlet's generated user services are the authoritative per-container units.
// journalctl merges their messages without reading unrelated user services.
func (q *Quadlet) Logs(ctx context.Context, c Config, key string, tail int) (ManagedLogs, error) {
	_, id, _ := strings.Cut(key, "/")
	if q.Journalctl == "" {
		return ManagedLogs{}, core.Unavailable("journalctl is unavailable")
	}
	args := []string{"--user", "--no-pager", "--quiet", "--output=short-iso", "--lines", strconv.Itoa(tail)}
	for _, unit := range quadletUnits(c, id) {
		args = append(args, "--unit", unit)
	}
	return boundedManagedLogs(ctx, q.Runner, core.Command{Program: q.Journalctl, Args: args, Env: []string{"SYSTEMD_COLORS=0", "SYSTEMD_URLIFY=0"}}, "systemd-journal")
}

// Both Compose engines expose group-scoped logs using the same explicit project
// identity and generated definition used by lifecycle operations.
func (p *Compose) Logs(ctx context.Context, c Config, key string, tail int) (ManagedLogs, error) {
	_, id, _ := strings.Cut(key, "/")
	if p.Program == "" {
		return ManagedLogs{}, core.Unavailable("The selected Compose engine is unavailable")
	}
	command := p.command(p.file(key), id, "logs", "--tail", strconv.Itoa(tail))
	return boundedManagedLogs(ctx, p.Runner, command, p.Engine+"-compose")
}

// Incus offers a scoped VM instance log, not a guest application log. Check the
// instance's private server-man identity before invoking the native log reader.
func (i *Incus) Logs(ctx context.Context, c Config, key string, tail int) (ManagedLogs, error) {
	_, id, _ := strings.Cut(key, "/")
	if i.Program == "" {
		return ManagedLogs{}, core.Unavailable("Incus is unavailable")
	}
	_, exists, err := i.read(ctx, c, key)
	if err != nil {
		return ManagedLogs{}, core.Unavailable("Incus managed instance ownership or availability could not be verified")
	}
	if !exists {
		return ManagedLogs{}, core.Unavailable("The managed Incus VM does not exist")
	}
	project, err := incusProject(c, id)
	if err != nil {
		return ManagedLogs{}, core.Unavailable("Incus project binding is unavailable")
	}
	// Incus does not expose a portable tail option for --show-log. Bound the
	// retained output instead; the log contains native instance diagnostics.
	command := core.Command{Program: i.Program, Args: []string{"--force-local", "--project", project, "info", "local:" + incusInstance(id), "--show-log"}}
	return boundedManagedLogs(ctx, i.Runner, command, "incus-instance")
}

func RegisterManagedLogs(r *core.Registry, c Coordinator) error {
	min, max := 1.0, 2000.0
	return r.AddModule(core.Module{ID: "managed_logs", Title: "Managed application logs", Available: true},
		core.Tool{Name: "managed_service_logs", Title: "Read managed application logs", Description: "Explicitly read bounded recent logs for one configured managed application. Runtime logs may contain private application data. This never starts a workload or reads another application.", ReadOnly: true,
			Fields: map[string]core.Field{
				"service": core.StringField("Managed application ID", true, 63),
				"tail": {Type: "integer", Minimum: &min, Maximum: &max, Description: "Most recent entries per native log reader; default 200"},
			},
			Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
				return c.ServiceLogs(ctx, in.String("service"), int(in.Number("tail")))
			}},
	)
}

