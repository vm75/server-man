# Serverman

A lightweight Linux server manager inspired by Cockpit, with a responsive web interface and MCP access for AI agents. Built with Go, Svelte 5, TypeScript, and Vite.

Implementation follows the [tracked plan](docs/plans/implementation.md). The application provides Linux resource monitoring, Podman/Docker Compose management, LXD/classic LXC/KVM management, authenticated HTTP APIs, MCP over stdio/Streamable HTTP, a responsive Svelte UI, and cancellable management jobs with bounded output and audit records. Remaining management modules are under development; their UI entries are labeled as planned.

The overview has CPU/network histories, per-core CPU meters, memory/swap, local storage usage and I/O, uptime/load, optional temperatures, and a searchable/sortable process table. Monitoring uses one shared Linux sampler and native SVG charts. See [monitoring details](docs/design/monitoring.md) for units, limits, and missing-data behavior.

**Containers** discovers Compose projects, shows containers and associated Podman pods, reads bounded logs, and runs up/start/stop/restart/pull/down jobs. It preserves the project directories, environment-file order and disabled markers used by `~/vms/vms.sh`. See [Compose setup and behavior](docs/design/compose.md) for paths, provider requirements, and action details.

**Virtual machines** separates LXD, classic LXC and KVM/libvirt inventory and controls. Launch LXD containers/VMs, manage existing classic containers, and manage libvirt lifecycle/autostart or define an existing KVM XML file. See [machine setup and behavior](docs/design/machines.md) for backend contexts, supported actions and retained storage.

## Build and run

Requires Linux, Go 1.26 or newer, Node.js 22.12 or newer, npm, and Make. Node is needed to build the UI, and is not needed to run the resulting executable.

```sh
make setup
make check
make test
make mcp-test
make build
./bin/serverman
```

Open `http://127.0.0.1:8080`. Sign in with the access token from the private file created on first start. The startup log shows its path; the default is `~/.config/serverman/token` (or under `XDG_CONFIG_HOME`). The server binds to loopback. `GET /healthz` provides a small unauthenticated JSON health response.

Management writes are disabled by default. When enabled, jobs run one at a time and appear in **Activity**. Instances using the same state directory also serialize writes through a file lock. Cancelling a job stops its remaining work; it does not undo completed changes. Each process retains 64 recent jobs in memory and records audit metadata under `~/.local/state/serverman` (or `XDG_STATE_HOME`).

## Development

Run `make api` in one terminal and `make dev` in another. Open the Vite address (`http://127.0.0.1:5173`); Vite proxies API requests to Go. Rebuild with `make build` to embed updated frontend assets in the executable. A Go-only build without generated UI assets serves APIs and an explanatory 503 response for the UI.

Use `./bin/serverman -config /absolute/path/config.json` to override defaults:

```json
{
  "listen": "127.0.0.1:8080",
  "vmRoot": "/srv/vms",
  "allowWrites": false,
  "sampleIntervalSeconds": 2
}
```

`vmRoot` defaults to `~/vms`. Compose projects default to `vmRoot/config/podman/<project>/` and `vmRoot/config/docker/<project>/`. Optional `podmanRoot` and `dockerRoot` override those discovery roots; environment files and disabled markers remain under `vmRoot/data/`. All roots, optional `tokenFile`, and optional `stateDir` must be absolute paths. Unknown fields, relative roots, and non-loopback listen addresses are rejected. Serverman does not edit Compose or environment files. Enabled Compose actions change workloads through the installed provider.

`sampleIntervalSeconds` ranges from 1 to 10. History retains at most 60 samples; the default interval shows two minutes. Process and network data follow the server's visible Linux namespaces/permissions. The first sample has null rates until the next sample establishes a delta.

For remote browser/API access, use a TLS reverse proxy and set `publicOrigin` to its HTTPS origin, such as `https://server.example.com`. Forward the original Host header. HTTPS deployments use Secure session cookies. A complete deployment example is tracked in the plan.

## HTTP API

All `/api/` routes require authentication except token sign-in. Send `Authorization: Bearer <token>` for an API client, or use the browser session cookie. Tokens in URL query parameters are not supported.

| Endpoint | Behavior |
| --- | --- |
| `POST /api/session` | Sign in using a JSON object with a `token` field |
| `DELETE /api/session` | Sign out the current browser session |
| `GET /api/status` | Server identity and write policy |
| `GET /api/tools` | Module availability and tool schemas |
| `POST /api/tools/<name>` | Invoke a tool with a JSON arguments object |

Core tools: `system_status`, `system_capabilities`, `monitor_snapshot`, `monitor_processes`, `jobs_list`, `jobs_get`, and `jobs_cancel`. Compose adds `compose_projects`, `compose_status`, `compose_logs`, and six [lifecycle tools](docs/design/compose.md#actions). `monitor_processes` accepts `sort`, `filter`, and `limit`. Mutation tools return a `job`; use `jobs_get` with its `id` to read state/output. `jobs_cancel` is an immediate control operation. Foreign browser origins and unexpected Host headers are rejected.

Machine tools include `machines_list`, `machines_details`, and [backend-specific actions](docs/design/machines.md#actions). `GET /api/tools` and the Agent access page provide the complete current schemas.

## MCP

Open **Agent access** in the UI for discovery and connection details. The running server provides Streamable HTTP at `/mcp`, authenticated with the same server access token. Local agents can launch a separate stdio process:

```sh
/absolute/path/to/serverman -transport stdio -config /absolute/path/to/config.json
```

Use HTTP to share the web server's jobs and history. A local stdio process has its own history and cancels unfinished work when it exits. See [MCP connection details and Hermes/OpenClaw examples](docs/design/mcp.md) for setup, schemas, protocol limits, and the verification scope. `make mcp-test` checks both transports with the official SDK; Node and its SDK are only verification dependencies.

See [architecture](ARCHITECTURE.md) for shared operation boundaries, resource limits, and authentication, and [project guidance](AGENTS.md) for working commands.

The repository lives in this `serverman/` directory because the parent workspace's `.git` mount is read-only.
