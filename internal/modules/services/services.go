// Package services manages systemd service units in a fixed system/user scope.
package services

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"server-man/internal/core"
)

var unitName = regexp.MustCompile(`^(?:[A-Za-z0-9_@:.+-]|\\x[0-9A-Fa-f]{2})+\.service$`)
var properties = []string{"Id", "Description", "LoadState", "ActiveState", "SubState", "UnitFileState", "MainPID", "Result", "ExecMainCode", "ExecMainStatus", "ActiveEnterTimestamp", "InactiveEnterTimestamp", "FragmentPath", "CanStart", "CanStop", "CanReload"}

type Service struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	Enabled     string `json:"enabled"`
	Preset      string `json:"preset,omitempty"`
}
type Inventory struct {
	Scope            string    `json:"scope"`
	Available        bool      `json:"available"`
	JournalAvailable bool      `json:"journalAvailable"`
	Reason           string    `json:"reason,omitempty"`
	Services         []Service `json:"services"`
	Warnings         []string  `json:"warnings"`
}
type Details struct {
	Service
	MainPID        *uint64 `json:"mainPID,omitempty"`
	Result         string  `json:"result,omitempty"`
	ExecMainCode   string  `json:"execMainCode,omitempty"`
	ExecMainStatus string  `json:"execMainStatus,omitempty"`
	ActiveSince    string  `json:"activeSince,omitempty"`
	InactiveSince  string  `json:"inactiveSince,omitempty"`
	FragmentPath   string  `json:"fragmentPath,omitempty"`
	CanStart       bool    `json:"canStart"`
	CanStop        bool    `json:"canStop"`
	CanReload      bool    `json:"canReload"`
}
type Logs struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}
type Backend struct {
	scope, systemctl, journalctl string
	runner                       core.Executor
}

func New(scope string, runner core.Executor) *Backend {
	if scope == "" {
		scope = "system"
	}
	b := &Backend{scope: scope, runner: runner}
	b.systemctl, _ = exec.LookPath("systemctl")
	b.journalctl, _ = exec.LookPath("journalctl")
	return b
}
func validUnit(name string) bool {
	return len(name) <= 255 && !strings.HasPrefix(name, "-") && unitName.MatchString(name)
}
func (b *Backend) command(action string, args ...string) core.Command {
	return core.Command{Program: b.systemctl, Args: append([]string{"--" + b.scope, "--no-pager", "--no-ask-password", action}, args...), Env: []string{"SYSTEMD_COLORS=0", "SYSTEMD_URLIFY=0"}, Timeout: 12 * time.Second}
}

func (b *Backend) List(ctx context.Context) (Inventory, error) {
	result := Inventory{Scope: b.scope, Available: b.systemctl != "", JournalAvailable: b.journalctl != "", Services: []Service{}, Warnings: []string{}}
	if !result.Available {
		result.Reason = "systemctl was not found; install systemd tools and restart server-man"
		return result, nil
	}
	merged := map[string]Service{}
	for _, action := range []string{"list-unit-files", "list-units"} {
		args := []string{"--type=service", "--full", "--plain", "--no-legend", "--output=json"}
		if action == "list-units" {
			args = append(args, "--all")
		}
		text, err := b.runner.Capture(ctx, b.command(action, args...))
		if err != nil {
			result.Warnings = append(result.Warnings, action+" unavailable; check the configured system/user bus and permissions")
			continue
		}
		items, err := parseList(text, action == "list-unit-files")
		if err != nil {
			result.Warnings = append(result.Warnings, action+" returned unsupported or oversized inventory")
			continue
		}
		for _, item := range items {
			old, ok := merged[item.Name]
			if !ok {
				old = Service{Name: item.Name, Load: "unknown", Active: "unknown", Sub: "unknown", Enabled: "unknown"}
			}
			if action == "list-unit-files" {
				old.Enabled, old.Preset = item.Enabled, item.Preset
			} else {
				old.Description, old.Load, old.Active, old.Sub = item.Description, item.Load, item.Active, item.Sub
			}
			merged[item.Name] = old
			if len(merged) > 4096 {
				return result, core.Unavailable("Service inventory exceeds 4096 units")
			}
		}
	}
	for _, item := range merged {
		result.Services = append(result.Services, item)
	}
	slices.SortFunc(result.Services, func(a, c Service) int { return strings.Compare(a.Name, c.Name) })
	return result, nil
}

func (b *Backend) Details(ctx context.Context, name string) (Details, error) {
	if !validUnit(name) {
		return Details{}, core.Invalid("Expected one exact .service unit name; patterns, paths and flags are unsupported")
	}
	if b.systemctl == "" {
		return Details{}, core.Unavailable("systemctl is unavailable")
	}
	text, err := b.runner.Capture(ctx, b.command("show", "--property="+strings.Join(properties, ","), "--", name))
	if err != nil {
		return Details{}, core.Unavailable("Service details unavailable; check the unit and configured system/user bus")
	}
	values := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && slices.Contains(properties, key) {
			values[key] = value
		}
	}
	if values["LoadState"] == "not-found" {
		return Details{}, &core.Error{Code: "not_found", Message: "Service unit not found", Status: 404}
	}
	if !validUnit(values["Id"]) || values["LoadState"] == "" || values["ActiveState"] == "" {
		return Details{}, core.Unavailable("systemctl returned unsupported service details")
	}
	d := Details{Service: Service{Name: values["Id"], Description: values["Description"], Load: values["LoadState"], Active: values["ActiveState"], Sub: values["SubState"], Enabled: values["UnitFileState"]}, Result: values["Result"], ExecMainCode: values["ExecMainCode"], ExecMainStatus: values["ExecMainStatus"], ActiveSince: values["ActiveEnterTimestamp"], InactiveSince: values["InactiveEnterTimestamp"], FragmentPath: values["FragmentPath"], CanStart: values["CanStart"] == "yes", CanStop: values["CanStop"] == "yes", CanReload: values["CanReload"] == "yes"}
	if n, err := strconv.ParseUint(values["MainPID"], 10, 64); err == nil {
		d.MainPID = &n
	}
	return d, nil
}

func (b *Backend) Action(ctx context.Context, name, action string, out io.Writer) (any, error) {
	if !validUnit(name) {
		return nil, core.Invalid("Invalid service unit name")
	}
	if !slices.Contains([]string{"start", "stop", "restart", "reload", "enable", "disable"}, action) {
		return nil, core.Invalid("Unsupported service action")
	}
	if b.systemctl == "" {
		return nil, core.Unavailable("systemctl is unavailable")
	}
	c := b.command(action, "--", name)
	c.Timeout = 20 * time.Minute
	if err := b.runner.Run(ctx, c, out); err != nil {
		return nil, fmt.Errorf("Service %s failed: %w", action, err)
	}
	return map[string]string{"scope": b.scope, "unit": name, "action": action}, nil
}

func (b *Backend) Logs(ctx context.Context, name string, tail int) (Logs, error) {
	if !validUnit(name) {
		return Logs{}, core.Invalid("Invalid service unit name")
	}
	if tail == 0 {
		tail = 200
	}
	if tail < 1 || tail > 2000 {
		return Logs{}, core.Invalid("Log lines must be between 1 and 2000")
	}
	if b.journalctl == "" {
		return Logs{}, core.Unavailable("journalctl is unavailable")
	}
	c := core.Command{Program: b.journalctl, Args: []string{"--" + b.scope, "--no-pager", "--quiet", "--output=short-iso", "--unit", name, "--lines", strconv.Itoa(tail)}, Env: []string{"SYSTEMD_COLORS=0", "SYSTEMD_URLIFY=0"}, Timeout: 10 * time.Second}
	out := core.NewTail(128 << 10)
	if err := b.runner.Run(ctx, c, out); err != nil {
		return Logs{}, core.Unavailable("Journal unavailable; check journal permissions and the configured scope")
	}
	text, truncated := out.Snapshot()
	return Logs{Text: text, Truncated: truncated}, nil
}
