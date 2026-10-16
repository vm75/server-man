# Serverman

A lightweight Linux server manager inspired by Cockpit, with a responsive web interface and MCP access for AI agents. Built with Go, Svelte 5, TypeScript, and Vite.

Implementation follows the [tracked plan](docs/plans/implementation.md). The application provides authenticated HTTP APIs, a responsive Svelte UI, a shared tool registry, and cancellable management jobs with bounded output and audit records. Backend management modules and MCP transports are under development; their UI entries are labeled as planned.

## Build and run

Requires Linux, Go 1.26 or newer, Node.js 22.12 or newer, npm, and Make. Node is needed to build the UI, and is not needed to run the resulting executable.

```sh
make setup
make check
make test
make build
./bin/serverman
```

Open `http://127.0.0.1:8080`. Sign in with the access token from the private file created on first start. The startup log shows its path; the default is `~/.config/serverman/token` (or under `XDG_CONFIG_HOME`). The server binds to loopback. `GET /healthz` provides a small unauthenticated JSON health response.

Management writes are disabled by default. When enabled, jobs run one at a time and appear in **Activity**. Cancelling a job stops its remaining work; it does not undo completed changes. The server retains 64 recent jobs in memory and records audit metadata under `~/.local/state/serverman` (or `XDG_STATE_HOME`).

## Development

Run `make api` in one terminal and `make dev` in another. Open the Vite address (`http://127.0.0.1:5173`); Vite proxies API requests to Go. Rebuild with `make build` to embed updated frontend assets in the executable. A Go-only build without generated UI assets serves APIs and an explanatory 503 response for the UI.

Use `./bin/serverman -config /absolute/path/config.json` to override defaults:

```json
{
  "listen": "127.0.0.1:8080",
  "vmRoot": "/srv/vms",
  "allowWrites": false
}
```

`vmRoot` defaults to `~/vms`. Optional `tokenFile` and `stateDir` must be absolute paths. Unknown fields, relative roots, and non-loopback listen addresses are rejected. The application does not currently modify the VM root.

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

Current tools: `system_status`, `jobs_list`, `jobs_get`, and `jobs_cancel`. Mutation tools return a `job`; use `jobs_get` with its `id` to read state/output. `jobs_cancel` is an immediate control operation. Foreign browser origins and unexpected Host headers are rejected.

See [architecture](ARCHITECTURE.md) for shared operation boundaries, resource limits, and authentication, and [project guidance](AGENTS.md) for working commands.

The repository lives in this `serverman/` directory because the parent workspace's `.git` mount is read-only.
