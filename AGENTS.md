# Project guide

server-man is a Linux administration application built with Go, Svelte 5, TypeScript, and Vite. Web and MCP operations must share the same validated management functions.

## Repository map and commands

- `cmd/serverman/`: entry point and process lifecycle.
- `internal/config/`, `internal/httpapi/`, `internal/webui/`: configuration, HTTP routes, and embedded assets.
- `internal/core/`: shared schemas, registry, execution, jobs, and audit. `internal/auth/`: token/session authentication. `internal/app/`: module assembly.
- `internal/modules/monitor/`: Linux collector, sampler, and tools; see [monitoring design](docs/design/monitoring.md) for units and bounds. `web/src/pages/Overview.svelte` renders the dashboard.
- `internal/declarative/`: central YAML model/store, preflight, apply coordinator, container/VM/share adapters and shared lifecycle/edit tools; see [declarative design](docs/design/declarative.md). `Applications.svelte`, `Configuration.svelte` and `ConfiguredShares.svelte` render managed resources.
- `internal/modules/services/`: fixed-scope systemd operations; see [service design](docs/design/services.md). `web/src/pages/Services.svelte` renders service inventory/details/logs.
- `internal/modules/shares/`: owned Samba/NFS fragments and rollback; see [share design](docs/design/shares.md). `web/src/pages/ConfiguredShares.svelte` renders central share forms.
- `internal/modules/updates/`: fixed native package operations; see [update design](docs/design/updates.md). `web/src/pages/Updates.svelte` renders package candidates/jobs.
- `internal/mcp/`: MCP adapters; [MCP design](docs/design/mcp.md) describes connection/protocol rules. `tools/mcp/`: official SDK interoperability checks and an in-process HTTP bridge.
- `web/src/`: Svelte UI; `web/vite.config.ts` sets its Go asset output and development proxy.
- `make setup`: install Go and frontend dependencies from manifests/lockfile.
- `make api` and `make dev`: backend and frontend development servers, in separate terminals.
- `make check`: Go vet and frontend type checking. `make test`: Go race tests.
- `make mcp-test`: build and verify stdio/HTTP MCP using the official SDK; requires `make setup` dependencies. The helper disables writes and does not listen on TCP.
- `make build`: production UI and Go binary at `bin/serverman`.
- Generated frontend assets and dependencies are ignored; do not edit them.

## Scope and tracking

- Follow [the implementation plan](docs/plans/implementation.md); commit each verified step and its tracking update separately.
- Use KISS and YAGNI. Prefer standard libraries and small explicit interfaces.
- Search before reading broad files; keep context relevant and avoid generated/dependency directories.
- Preserve existing conventions unless the task requires a change.

## Boundaries

- Never pass user input to a shell. Execute fixed programs with argument arrays and validate resource identifiers.
- Do not copy secrets, environment file contents, or resolved Compose configuration into logs, fixtures, APIs, or Git.
- Verification may read host inventory and metrics. Use temporary directories and command fixtures for operations that change containers, VMs, packages, shares, or services.
- Do not modify `~/vms`; preserve its conventions through configurable discovery.
- Share mutations may replace only owned fragments. Preserve unmanaged configuration and shared directory data; verify rollback with temporary files and native-command fixtures.
- Package verification uses simulations/fixtures. Never refresh or install packages on this development host. Preserve native signature checks and locks; never request a distro migration or reboot implicitly.
- Keep monitoring, retained output, jobs, and process concurrency bounded.
- Authentication, write policy, and validation apply equally to HTTP and MCP.
- Register tools through core; keep transports out of domain modules. See [architecture](ARCHITECTURE.md) for execution/resource limits.

## Completion

- Validate behavior relevant to each change. Record what ran and its limits in the plan.
- Update only documentation whose truth changed: user commands/capabilities in README, implementation tracking in the plan, component boundaries in architecture documentation when it exists.
- Keep unrelated changes out of each commit. Never claim unfinished plan items are complete.
