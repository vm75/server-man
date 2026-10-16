# Architecture

Serverman runs as one Linux process for the web UI, authenticated JSON APIs and MCP HTTP. An agent can also launch a standalone stdio process. Both transports call the same management registry.

## Components

| Path | Responsibility |
| --- | --- |
| `cmd/serverman/` | Configuration, module assembly, HTTP listener, shutdown |
| `internal/app/` | Register the application's modules and tools |
| `internal/auth/` | Private access token, browser sessions, host/origin restrictions |
| `internal/core/` | Tool schemas, validation, write policy, process execution, jobs, audit |
| `internal/modules/monitor/` | Shared Linux resource sampler, bounded history and read-only tools |
| `internal/modules/compose/` | Project discovery, safe metadata parsing and fixed Compose/native-engine commands |
| `internal/modules/machines/` | Independent LXD/classic LXC/libvirt capabilities, whitelisted inventory, lifecycle and XML import |
| `internal/modules/services/` | System/user systemd inventory, selected properties, bounded journal and fixed service actions |
| `internal/httpapi/` | HTTP transport, request limits, authentication, JSON responses |
| `internal/mcp/` | MCP tools protocol, stdio and stateless Streamable HTTP adapters |
| `internal/webui/` | Embed and serve production assets |
| `web/src/` | Responsive Svelte UI; API client, login, job activity, monitoring, Compose and agent access |
| `tools/mcp/` | Test-only official SDK client and HTTP handler bridge |

Dependency direction is entry point → assembly/transports → core. Domain modules depend on core interfaces, and do not import HTTP or UI packages. Assembly receives configuration, jobs, the shared sampler and a command executor. Tests inject a command recorder. A module supplies a capability descriptor and named tools; the registry provides discovery to transports.

## Operation flow

```mermaid
flowchart LR
  Browser --> Auth[Host, origin, and authentication]
  MCPHTTP[MCP HTTP client] --> Auth
  Auth --> HTTP[JSON HTTP adapter]
  HTTP --> Registry[Validate arguments and enforce write policy]
  Auth --> MCP[MCP HTTP adapter]
  MCP --> Registry
  Stdio[MCP stdio client] --> Registry
  Registry --> Read[Read operation]
  Registry --> Jobs[Serialized management job]
  Jobs --> Runner[Fixed executable and argument array]
  Jobs --> Audit[Audit metadata]
```

Tools define their fields once. The registry derives an input JSON Schema and uses those same fields to reject unknown parameters, incorrect types, invalid enums, missing fields, and values outside declared limits. Domain modules must also validate resource names and file paths before using them. There is no shell execution API or arbitrary program API.

Reads run with a 20 second request deadline, at most four at once. Monitoring reads use the shared cached sampler rather than recollecting for every request. Management writes require `allowWrites: true` in server configuration. They return a job immediately and execute independently of the requesting connection. A destructive tool additionally requires `confirm: true`. The job cancellation control runs immediately through the same registry and write policy.

The monitoring module samples Linux every one to ten seconds, keeping up to 60 aggregate history points and one current process list. Native SVG charts and CSS meters render the dashboard. Missing data, initial/reset counters, stale snapshots, process limits and namespace scope are explicit. See [monitoring design](docs/design/monitoring.md) for source mappings and calculation rules.

Compose discovery reads file metadata without opening configuration or environment contents. Inventory runs at most one native container query per engine; selected project details and actions delegate project interpretation to its Compose provider. Only whitelisted container/pod metadata is returned. See [Compose design](docs/design/compose.md) for path checks, provider differences and command limits. The UI's shared `startJob` helper opens Activity; completion events refresh the selected project after its job finishes.

Machine management fixes backend context in configuration and uses separate executables for LXD and classic LXC. Libvirt mutations resolve the selected domain to a UUID before execution. Deletion checks stopped/inactive state; no disk-removal flags are exposed for libvirt. XML import checks a configured directory boundary and passes a private validated snapshot to `virsh define --validate`. See [machine design](docs/design/machines.md) for CLI mappings and verification limits.

Service management fixes system/user scope in configuration, merges installed unit files with loaded runtime state and reports partial reads explicitly. Only exact validated `.service` names reach lifecycle or journal commands. Details select an explicit property allowlist; enablement does not imply a runtime start/stop. See [service design](docs/design/services.md) for parser compatibility, limits and permissions.

## Resource bounds and failure handling

- One management job runs at a time across modules and processes sharing a state directory. A file lock is held until completion/cancellation and its audit record. A competing write receives a `busy` error. No queue or database is needed.
- Jobs have a 30 minute deadline and are cancelled on server shutdown. Commands default to a 20 minute timeout and reads captured through the runner default to 15 seconds.
- Child commands get their own process group. Cancellation kills the group; inherited output pipes have a two second wait limit.
- Command capture separates stdout from diagnostics and rejects output above 1 MiB. Job output retains the newest 128 KiB.
- Keep 64 recent jobs in memory; restarting the process clears job history. Job metadata can be listed separately from an individual job's output/result.
- Record start, cancellation request, and terminal state in a private JSONL audit log. Arguments, results, output, and credentials are excluded. Rotate at approximately 4 MiB and keep one previous file.
- Failure to record a job's start prevents execution. A failure to record completion is reported explicitly, even if the host operation already ran.
- Job cancellation stops remaining work; it does not undo changes already made by a backend. Each domain module must handle its own transaction/rollback requirements.

## Authentication and privileges

The server creates a 32 byte random access token in an owner-only file, and keeps its SHA-256 digest for comparison. The token file can be configured; it cannot be a symlink or group/world readable. Browser sign-in exchanges the token for a random HttpOnly, SameSite Strict cookie with an eight hour lifetime. Up to 32 browser sessions are kept in memory; logout removes a session and restart expires them all. The UI does not store the access token in browser storage.

HTTP clients can send a bearer token instead of a cookie. API bodies are limited to 1 MiB, login bodies to 4 KiB, and management requests use JSON. Reject unexpected Host headers and foreign Origin headers, including on requests with bearer tokens. A configured HTTPS `publicOrigin` permits a reverse proxy's external hostname; otherwise the HTTP listener and allowed hosts are local. The health endpoint reveals only whether the process responds.

The process has the permissions of its operating-system account. Read/write policy is an application gate, not privilege escalation. Future backend modules must report unavailable binaries and denied permissions rather than silently invoke `sudo`. Rootless Podman belongs to the invoking account; running as root does not access another user's rootless inventory.

MCP HTTP reuses this authentication/origin boundary. Stdio is accessed by launching a process under the workload's operating-system account; it does not require a browser token. Each process owns its recent in-memory jobs. See [MCP design and connection examples](docs/design/mcp.md) for lifecycle, result shapes, cancellation, shared-state locking and protocol compatibility.

## Verification

`make check` runs Go vet and frontend type checking. `make test` runs race tests for configuration, authentication, HTTP/MCP policy, tool validation, job bounds/cancellation/locking, output capture, and process timeouts. `make mcp-test` verifies the built stdio server and HTTP handler with the official SDK. Building frontend assets first also enables embedded production asset checks. Command-changing domain integration tests must use fixtures or a command recorder; development verification must preserve the host's actual workloads.

Live TCP serving and browser verification remain outstanding because this development sandbox denies local socket creation; Chromium also exits on a denied `setsockopt` call. See the plan's evidence log for exact verification and remaining limits.
