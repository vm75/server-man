package declarative

import (
	"context"
	"io"
	"os"

	"serverman/internal/core"
)

// RegisterInspection adds read/validate operations independently of backend
// availability, allowing a broken ported configuration to remain inspectable.
func RegisterInspection(r *core.Registry, store Store) error {
	return r.AddModule(core.Module{ID: "managed", Title: "Managed configuration", Available: true},
		core.Tool{Name: "managed_config_read", Title: "Inspect workload configuration", Description: "Read private desired configuration for editing; disabled with management writes. Secret file contents are never read.", ReadOnly: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			if !r.AllowWrites {
				return nil, &core.Error{Code: "writes_disabled", Message: "Configuration editing is disabled with management writes.", Status: 403}
			}
			data, rev, err := store.ReadRaw()
			if os.IsNotExist(err) {
				return map[string]any{"revision": "missing", "content": "version: 1\n", "valid": true}, nil
			}
			if err != nil {
				return nil, core.Unavailable("Configuration cannot be read; check its path, type and private permissions")
			}
			_, parseErr := Parse(data)
			message := ""
			if parseErr != nil {
				message = parseErr.Error()
			}
			return map[string]any{"revision": rev, "content": string(data), "valid": parseErr == nil, "error": message}, nil
		}},
		core.Tool{Name: "managed_config_validate", Title: "Validate workload configuration", Description: "Validate a proposed YAML workload configuration without saving it or activating resources.", ReadOnly: true, Fields: map[string]core.Field{"content": core.StringField("Workload YAML", true, MaxConfigBytes)}, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			c, err := Parse([]byte(in.String("content")))
			if err != nil {
				return nil, core.Invalid("%s", err)
			}
			return map[string]any{"valid": true, "files": store.CheckFiles(c), "runtimeChecked": false}, nil
		}},
		core.Tool{Name: "managed_config_health", Title: "Check workload configuration", Description: "Check configuration validity and required files without changing the host. Runtime readiness checks are reported separately.", ReadOnly: true, Run: func(ctx context.Context, in core.Input, out io.Writer) (any, error) {
			data, _, err := store.ReadRaw()
			if os.IsNotExist(err) {
				return map[string]any{"configured": false, "runtimeChecked": false}, nil
			}
			if err != nil {
				return nil, core.Unavailable("Configuration cannot be read; check its path, type and private permissions")
			}
			c, err := Parse(data)
			if err != nil {
				return map[string]any{"configured": true, "valid": false, "error": err.Error(), "runtimeChecked": false}, nil
			}
			return map[string]any{"configured": true, "valid": true, "files": store.CheckFiles(c), "runtimeChecked": false}, nil
		}})
}
