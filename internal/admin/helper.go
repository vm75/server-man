// Package admin implements the one-shot privileged management boundary.
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"server-man/internal/config"
	"server-man/internal/core"
	"server-man/internal/firecrackerhost"
	"server-man/internal/modules/services"
	"server-man/internal/modules/shares"
	"server-man/internal/modules/updates"
	"strings"
)

type Request struct {
	Operation string          `json:"operation"`
	Arguments json.RawMessage `json:"arguments"`
}
type Response struct {
	Result    any    `json:"result,omitempty"`
	Output    string `json:"output,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
	Code      string `json:"code,omitempty"`
	Status    int    `json:"status,omitempty"`
}

func allowed(name string) bool {
	switch name {
	case "shares_definitions_validate", "shares_definitions_inspect", "shares_definitions_apply", "dependencies_install", "shares_setup", "packages_install", "updates_status", "updates_refresh", "updates_install", "shares_list", "shares_samba_password",
		"services_list", "services_details", "services_logs", "services_start",
		"services_stop", "services_restart", "services_reload", "services_enable", "services_disable",
		"firecracker_publish", "firecracker_inspect", "firecracker_action", "firecracker_remove", "firecracker_logs":
		return true
	}
	return false
}

// Serve accepts exactly one bounded request. Configuration is supplied by the
// privileged entry point, never by the unprivileged request.
func Serve(ctx context.Context, cfg config.Config, runner core.Executor, in io.Reader, out io.Writer) error {
	account, _ := shares.CurrentAccount()
	return ServeForAccount(ctx, cfg, runner, in, out, account)
}

// ServeForAccount receives an identity resolved by the trusted entry point.
func ServeForAccount(ctx context.Context, cfg config.Config, runner core.Executor, in io.Reader, out io.Writer, account string) error {
	// One read goroutine is bounded by the one-shot process lifetime. Selecting
	// cancellation ensures a stalled caller cannot hold the privileged lock after
	// the helper's deadline. The entry point exits after Serve returns.
	type readResult struct {
		data []byte
		err  error
	}
	reads := make(chan readResult, 1)
	go func() { data, err := io.ReadAll(io.LimitReader(in, (1<<20)+1)); reads <- readResult{data, err} }()
	var data []byte
	var err error
	select {
	case read := <-reads:
		data, err = read.data, read.err
	case <-ctx.Done():
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return errors.New("request exceeds 1 MiB")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var req Request
	if err = dec.Decode(&req); err != nil {
		return errors.New("invalid helper request")
	}
	if dec.Decode(new(any)) != io.EOF {
		return errors.New("expected one helper request")
	}
	if !allowed(req.Operation) {
		return errors.New("operation is not allowed")
	}
	if strings.HasPrefix(req.Operation, "firecracker_") {
		result, runErr := firecrackerhost.Handle(ctx, account, req.Operation, req.Arguments)
		reply := Response{Result: result}
		if runErr != nil {
			reply.Result = nil
			reply.Error = runErr.Error()
			reply.Code = "operation_failed"
			reply.Status = 500
		}
		return json.NewEncoder(out).Encode(reply)
	}
	if strings.HasPrefix(req.Operation, "services_") {
		var args core.Input
		if err := json.Unmarshal(req.Arguments, &args); err == nil && args.String("scope") != "" && args.String("scope") != "system" {
			return errors.New("helper manages system services only")
		}
	}
	registry := core.NewRegistry(true, nil)
	if err = services.New("system", runner).Register(registry); err != nil {
		return err
	}
	shareBackend := shares.NewForAccount(cfg, runner, account)
	if err = shareBackend.Register(registry); err != nil {
		return err
	}
	if err = shareBackend.RegisterDefinitions(registry); err != nil {
		return err
	}
	if err = updates.New(cfg, runner).Register(registry); err != nil {
		return err
	}
	if err = shares.RegisterSetup(registry, cfg, runner); err != nil {
		return err
	}
	output := core.NewTail(128 << 10)
	result, runErr := registry.Execute(ctx, req.Operation, req.Arguments, output)
	text, truncated := output.Snapshot()
	response := Response{Result: result, Output: text, Truncated: truncated}
	if runErr != nil {
		response.Result = nil
		response.Error = runErr.Error()
		response.Code = "operation_failed"
		response.Status = 500
		var typed *core.Error
		if errors.As(runErr, &typed) {
			response.Code = typed.Code
			response.Status = typed.Status
		}
	}
	return json.NewEncoder(out).Encode(response)
}
