# Adding a management module

server-man modules register typed operations with the shared Go registry. The HTTP API and both MCP transports derive their tool names, argument validation, write policy, and errors from that registry. A module does not add its own transport or execute shell text.

## Add a module

Create a package under `internal/modules/<name>`. Give it a constructor that accepts only the configuration and `core.Executor` inputs it needs, then a `Register(*core.Registry) error` method. Register one capability descriptor and focused tools. Read-only tools set `ReadOnly: true`; changing tools return `core.Job` values via `registry.StartJob`, validate every argument, and mark irreversible operations `Destructive: true` with a required `confirm` field.

The executor takes a fixed program and argument slice. Keep resource limits and cancellation in the executor; do not call a shell or put secrets in job output. Return structured values with bounded fields. Do not expose environment variables, credentials, or arbitrary file contents through inventory.

Example tool registration:

```go
func Register(registry *core.Registry, runner core.Executor) error {
    return registry.AddModule(core.Module{ID: "example", Title: "Example backend", Available: true},
        core.Tool{
            Name: "example_list", Title: "List examples",
            Description: "Read the backend inventory.", ReadOnly: true,
            Run: func(ctx context.Context, _ core.Input, _ io.Writer) (any, error) {
                output, err := runner.Capture(ctx, core.Command{Program: "examplectl", Args: []string{"list", "--json"}})
                if err != nil { return nil, err }
                return parseInventory(output)
            },
        },
    )
}
```

Add the module to `internal/app.Registry` so all transports receive it. Give it a page in `web/src/pages`, using `web/src/lib/api.ts` for requests and the existing shared jobs drawer for mutations. Keep loading, unavailable, empty, and failure states visible. Test command arguments with a recorder, temporary files, and fixtures; do not run management changes against the development host.

The MCP SDK parity verifier in `tools/mcp/verify.mjs` checks the actual production registry over stdio and Streamable HTTP. Extend it for each new tool, including its argument schema, annotations, invalid input, and denied-write behavior. The browser verifier in `tools/ui` checks rendered routes and responsive navigation.

## Keep the boundary small

Modules own domain parsing and validation. `core` owns execution, serialization, jobs, and output limits. `httpapi` and `mcp` own only transport concerns. Optional system utilities should produce an unavailable capability with a useful reason instead of preventing other modules from loading.
