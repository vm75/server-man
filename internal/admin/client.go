package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"server-man/internal/core"
	"strings"
	"time"
)

const HelperPath = "/usr/local/libexec/server-man-admin"

// Client invokes only the installed helper. Requests are never command arguments.
type Client struct{ Runner core.Executor }

func (c Client) Call(ctx context.Context, operation string, input core.Input, out io.Writer) (any, error) {
	if !allowed(operation) {
		return nil, core.Invalid("operation is not allowed")
	}
	arguments, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	request, err := json.Marshal(Request{Operation: operation, Arguments: arguments})
	if err != nil {
		return nil, err
	}
	runner := c.Runner
	if runner == nil {
		runner = core.Runner{}
	}
	response, err := runner.Capture(ctx, core.Command{
		Program: "/usr/bin/sudo", Args: []string{"-n", "--", HelperPath}, Stdin: bytes.NewReader(request),
		Timeout: 25 * time.Minute, CancelGrace: 45 * time.Second,
	})
	if err != nil {
		if operation == "shares_samba_password" {
			return nil, core.Unavailable("Could not set the Samba password; check privileged helper installation and access")
		}
		return nil, fmt.Errorf("privileged helper: %w", err)
	}
	var reply Response
	if err = json.Unmarshal([]byte(response), &reply); err != nil {
		return nil, core.Unavailable("invalid privileged helper response")
	}
	if _, err = io.WriteString(out, reply.Output); err != nil {
		return nil, err
	}
	if reply.Truncated {
		if _, err = io.WriteString(out, "\n[Earlier privileged output was discarded.]\n"); err != nil {
			return nil, err
		}
	}
	if reply.Error != "" {
		return nil, &core.Error{Code: reply.Code, Status: reply.Status, Message: reply.Error}
	}
	return reply.Result, nil
}

// Route redirects complete domain operations, including share file transactions.
func Route(r *core.Registry, c Client, defaultScope string) {
	r.Transform(func(tool core.Tool) func(context.Context, core.Input, io.Writer) (any, error) {
		if !allowed(tool.Name) {
			return tool.Run
		}
		return func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			if strings.HasPrefix(tool.Name, "services_") {
				scope := in.String("scope")
				if scope == "" {
					scope = defaultScope
				}
				if scope == "user" {
					return tool.Run(ctx, in, out)
				}
			}
			return c.Call(ctx, tool.Name, in, out)
		}
	})
}
