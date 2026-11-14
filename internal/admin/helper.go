// Package admin implements the one-shot privileged management boundary.
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"serverman/internal/config"
	"serverman/internal/core"
	"serverman/internal/modules/services"
	"serverman/internal/modules/shares"
	"serverman/internal/modules/updates"
	"strings"
)

type Request struct {
	Operation string          `json:"operation"`
	Arguments json.RawMessage `json:"arguments"`
}
type Response struct {
	Result any    `json:"result,omitempty"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
	Code   string `json:"code,omitempty"`
	Status int    `json:"status,omitempty"`
}

func allowed(name string) bool {
	switch name {
	case "shares_setup", "packages_install", "updates_status", "updates_refresh", "updates_install", "shares_list",
		"samba_share_save", "samba_share_remove", "nfs_share_save", "nfs_share_remove",
		"services_list", "services_details", "services_logs", "services_start",
		"services_stop", "services_restart", "services_reload", "services_enable", "services_disable":
		return true
	}
	return false
}

// Serve accepts exactly one bounded request. Configuration is supplied by the
// privileged entry point, never by the unprivileged request.
func Serve(ctx context.Context, cfg config.Config, runner core.Executor, in io.Reader, out io.Writer) error {
	data, err := io.ReadAll(io.LimitReader(in, (1<<20)+1))
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
	if err = shares.New(cfg, runner).Register(registry); err != nil {
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
	text, _ := output.Snapshot()
	response := Response{Result: result, Output: text}
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
