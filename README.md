# Serverman

A lightweight Linux server manager inspired by Cockpit, with a responsive web interface and MCP access for AI agents. Built with Go, Svelte 5, TypeScript, and Vite.

Implementation follows the [tracked plan](docs/plans/implementation.md). The current foundation provides a Go HTTP server with embedded frontend assets, a health endpoint, and a responsive Svelte application. Management modules are under development; their UI entries are labeled as planned.

## Build and run

Requires Linux, Go 1.26 or newer, Node.js 22.12 or newer, npm, and Make. Node is needed to build the UI, and is not needed to run the resulting executable.

```sh
make setup
make check
make test
make build
./bin/serverman
```

Open `http://127.0.0.1:8080`. The server binds only to loopback while authentication is under development. `GET /healthz` provides a small JSON health response.

## Development

Run `make api` in one terminal and `make dev` in another. Open the Vite address (`http://127.0.0.1:5173`); Vite proxies API requests to Go. Rebuild with `make build` to embed updated frontend assets in the executable. A Go-only build without generated UI assets serves APIs and an explanatory 503 response for the UI.

Use `./bin/serverman -config /absolute/path/config.json` to override defaults:

```json
{
  "listen": "127.0.0.1:8080",
  "vmRoot": "/srv/vms"
}
```

`vmRoot` defaults to `~/vms`. Unknown fields, relative roots, and non-loopback listen addresses are rejected. The application does not currently modify that directory. See [project guidance](AGENTS.md) for development boundaries and the plan for the remaining scope.

The repository lives in this `serverman/` directory because the parent workspace's `.git` mount is read-only.
