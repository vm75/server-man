package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"time"
)

type Module struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

type Tool struct {
	Name        string                                               `json:"name"`
	Module      string                                               `json:"module"`
	Title       string                                               `json:"title"`
	Description string                                               `json:"description"`
	ReadOnly    bool                                                 `json:"readOnly"`
	Destructive bool                                                 `json:"destructive"`
	InputSchema map[string]any                                       `json:"inputSchema"`
	Fields      map[string]Field                                     `json:"-"`
	Run         func(context.Context, Input, io.Writer) (any, error) `json:"-"`
	Immediate   bool                                                 `json:"-"` // Control operations such as cancelling an existing job.
}

// Registry is populated at startup and immutable once requests are accepted.
type Registry struct {
	tools       map[string]Tool
	modules     map[string]Module
	jobs        *Jobs
	reads       chan struct{}
	AllowWrites bool
}

func NewRegistry(allowWrites bool, jobs *Jobs) *Registry {
	return &Registry{tools: map[string]Tool{}, modules: map[string]Module{}, jobs: jobs, reads: make(chan struct{}, 4), AllowWrites: allowWrites}
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func (r *Registry) AddModule(module Module, tools ...Tool) error {
	if !identifier.MatchString(module.ID) {
		return fmt.Errorf("invalid module ID %q", module.ID)
	}
	if _, exists := r.modules[module.ID]; exists {
		return fmt.Errorf("duplicate module %q", module.ID)
	}
	seen := make(map[string]bool)
	for _, tool := range tools {
		if !identifier.MatchString(tool.Name) || tool.Run == nil {
			return fmt.Errorf("invalid tool %q", tool.Name)
		}
		if _, exists := r.tools[tool.Name]; exists || seen[tool.Name] {
			return fmt.Errorf("duplicate tool %q", tool.Name)
		}
		if tool.ReadOnly && tool.Destructive {
			return fmt.Errorf("read-only tool %q cannot be destructive", tool.Name)
		}
		for name, field := range tool.Fields {
			if name == "" || !slices.Contains([]string{"string", "boolean", "integer", "number"}, field.Type) {
				return fmt.Errorf("invalid field %q in tool %q", name, tool.Name)
			}
			if len(field.Enum) > 0 && field.Type != "string" {
				return fmt.Errorf("enum field %q must be a string", name)
			}
		}
		seen[tool.Name] = true
	}
	r.modules[module.ID] = module
	for _, tool := range tools {
		tool.Module = module.ID
		tool.Fields = maps.Clone(tool.Fields)
		if tool.Fields == nil {
			tool.Fields = map[string]Field{}
		}
		if tool.Destructive {
			tool.Fields["confirm"] = Field{Type: "boolean", Required: true, Description: "Explicit acknowledgement of this destructive operation. Must be true."}
		}
		tool.InputSchema = schema(tool.Fields)
		r.tools[tool.Name] = tool
	}
	return nil
}

func (r *Registry) Modules() []Module {
	values := make([]Module, 0, len(r.modules))
	for _, value := range r.modules {
		values = append(values, value)
	}
	slices.SortFunc(values, func(a, b Module) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return values
}

func (r *Registry) Tools() []Tool {
	values := make([]Tool, 0, len(r.tools))
	for _, value := range r.tools {
		values = append(values, value)
	}
	slices.SortFunc(values, func(a, b Tool) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return values
}

func (r *Registry) Invoke(ctx context.Context, name string, raw json.RawMessage, source string) (result any, err error) {
	defer func() {
		if recover() != nil {
			result = nil
			err = fmt.Errorf("management operation panicked")
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tool, ok := r.tools[name]
	if !ok {
		return nil, &Error{Code: "not_found", Message: "unknown tool", Status: 404}
	}
	input, err := validate(raw, tool.Fields)
	if err != nil {
		return nil, err
	}
	module := r.modules[tool.Module]
	if !module.Available {
		return nil, Unavailable(module.Reason)
	}
	if tool.Destructive && !input.Bool("confirm") {
		return nil, Invalid("confirm must be true for this operation")
	}
	if !tool.ReadOnly {
		if !r.AllowWrites {
			return nil, &Error{Code: "writes_disabled", Message: "Management writes are disabled. Set allowWrites in the server configuration to enable them.", Status: 403}
		}
		if r.jobs == nil {
			return nil, Unavailable("job manager is not configured")
		}
		if !tool.Immediate {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			job, err := r.jobs.Start(tool.Name, source, func(ctx context.Context, out io.Writer) (any, error) { return tool.Run(ctx, input, out) })
			if err != nil {
				return nil, err
			}
			return map[string]any{"job": job}, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	select {
	case r.reads <- struct{}{}:
		defer func() { <-r.reads }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return tool.Run(ctx, input, io.Discard)
}
