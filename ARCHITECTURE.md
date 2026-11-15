# Architecture

server-man runs as one Linux process for the web UI, authenticated JSON APIs and MCP HTTP. An agent can also launch a standalone stdio process. Both transports call the same management registry.

The canonical identifier is `server-man`: Go module/imports, executables, service/configuration paths, browser event namespace, session cookie and owned share markers use that spelling. Environment variables use the shell-compatible `SERVER_MAN_` prefix. No legacy-name aliases or migration layer are provided.

## Components

| Path | Responsibility |
| --- | --- |
| `cmd/server-man/` | Configuration, module assembly, HTTP listener, shutdown |
| `internal/app/` | Register the application's modules and tools |
| `internal/auth/` | Private access token, browser sessions, host/origin restrictions |
| `internal/declarative/` | Portable typed YAML model, private revision-checked store/history, VM identities, file preflight, configuration tools and journaled apply coordinator (implementation in progress) |
| `internal/core/` | Tool schemas, validation, write policy, process execution, jobs, audit |
| `internal/modules/monitor/` | Shared Linux resource sampler, bounded history and read-only tools |
| `internal/modules/services/` | System/user systemd inventory, selected properties, bounded journal and fixed service actions |
| `internal/modules/shares/` | Owned Samba/NFS fragments, selected host inventory, revision checks, validated publication and runtime rollback |
| `internal/modules/updates/` | Native package-family detection, cached candidates, serialized refresh/install and reboot hints |
| `internal/httpapi/` | HTTP transport, request limits, authentication, JSON responses |
| `internal/mcp/` | MCP tools protocol, stdio and stateless Streamable HTTP adapters |
| `internal/webui/` | Embed and serve production assets |
| `web/src/` | Responsive Svelte UI; API client, login, job activity, monitoring, managed applications/configuration/shares and agent access |
| `tools/mcp/` | Test-only official SDK client and HTTP handler bridge |

Dependency direction is entry point → assembly/transports → core. Domain modules depend on core interfaces, and do not import HTTP or UI packages. Assembly receives configuration, jobs, the shared sampler and a command executor. Tests inject a command recorder. A module supplies a capability descriptor and named tools; the registry provides discovery to transports.

## Declarative configuration foundation

The application registers configuration read/validate/health and preview/save/apply-status/retry tools through the same core registry. The absolute bootstrap `manifestPath` selects the portable workload YAML, defaulting to `stateDir/server-man.yaml`. The store validates, preserves guest identities, performs private atomic revision-checked saves and retains bounded exact-byte history. File preflight never creates missing data and checks expected mount sources and DAC access. Separate reviewed provisioning jobs create only absent owned directories or blank raw/qcow2 disks, using held parent/file handles, plan revalidation and no-replace publication. Optional checksum-pinned initial image assets clone through held descriptors, native conversion and verified staging; creation-only source changes do not change runtime fingerprints or reinitialize existing disks. Provisioning does not activate consumers. Shared readiness/status operations add bounded cached backend probes and dependency blockers; Applications displays these diagnostics and managed lifecycle/boot controls. A shared read-only readiness operation drives the global alert on initial load and bounded refreshes across all pages, without native workload inventory or activation. These checks do not establish successful guest/application health. A file-based coordinator journals desired saves and per-resource publication/verification, with dependency ordering, drift inspection and refusal to blindly replay uncertain removals. Private history inspection and reviewed revert use the same coordinator. A prepared revert retains the superseded transition until desired-state commit, so retry can recover either side of that boundary; unresolved native removals cannot silently disappear from tracking. Initial Podman Quadlet and Docker/Podman Compose adapters publish owned definitions and provide shared application lifecycle/boot-policy operations. Compose uses explicit user-scope boot-only launcher units and currently disables engine crash/boot restart policies; it does not run competing supervisors. It verifies installed-generator output and distinguishes source and active-container fingerprints. An initial existing-disk libvirt adapter generates ownership-marked XML, registers persistent definitions, distinguishes deferred hardware from live state, and uses graceful shutdown/start and native autostart. An initial local Incus adapter generates profile-independent VM settings, preserves native volatile state, creates stopped guests and updates explicit autostart through native API operations. Incus removal is blocked pending data-retention exports. Centralized Samba/NFS adapters derive protocol sets from YAML and reuse the share transaction/rollback engine, with narrow helper-only validation/publication for protected native paths. Applications, Configuration and File shares offer shared typed create/edit/review forms and YAML recovery. Legacy Compose/machine modules and direct native share-edit APIs have been removed. Host services/packages and read-only native share inventory remain direct. Complete provisioning/backup, recovery/preflight, forms for remaining target features and native lifecycle/boot integration remain in progress. See [current foundation behavior](docs/design/declarative.md) and [remaining implementation work](docs/plans/declarative-management.md).

Portable backup planning currently inventories metadata and unresolved native/external requirements through the shared registry, tied to the current manifest revision. It does not copy data or mark backups complete. Service storage/private-file references are shared by fingerprints, startup diagnostics, provisioning and backup inventory. See [backup design](docs/design/backup.md) for bounds and unfinished collection/restore work.

Domain rules and workflows follow DRY, KISS and YAGNI: share validation and management functions across transports, keep interfaces explicit, and avoid speculative infrastructure.

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

Container and VM adapters consume the authoritative typed model; they do not discover external native definitions as desired configuration. Compose uses deterministic project names and private generated files, Quadlet uses owned user sources, libvirt uses stable UUID ownership metadata, and Incus uses explicit local project bindings. Existing host workloads outside this configuration are not imported. See [declarative design](docs/design/declarative.md) for adapter limits.

Service management accepts an explicit system/user scope, defaulted by configuration, merges installed unit files with loaded runtime state and reports partial reads explicitly. Only exact validated `.service` names reach lifecycle or journal commands. Details select an explicit property allowlist; enablement does not imply a runtime start/stop. Application-owned user units are identified from central configuration; their controls route through managed lifecycle/boot operations before privilege routing. Details identify the owning application. See [service design](docs/design/services.md) for parser compatibility, limits and permissions.

Share definitions live in central YAML. Generated fragments retain typed metadata comments for integrity checks and rollback, never as the authority for desired definitions. Ownership markers and deterministic rendering reject foreign/externally edited files. Mutations require a current revision, validate a temporary candidate, check for concurrent edits, then rename/sync the owned file. Samba validates an include-substituted shadow with testparm and requests reload; NFS changes only the selected export pairs through targeted exportfs commands. Failure restores the prior file and attempts native rollback. Cleanup has an independent 30-second deadline so cancellation can restore state; job cancellation preserves rollback diagnostics. See [share design](docs/design/shares.md) for exact guarantees and limits.

Updates choose the native package family from os-release plus installed executables, with an explicit configuration override. Reads never refresh repositories. Package mutations use fixed commands under the shared job lock; install combines refresh and normal updates in one job. Pacman refresh uses checkupdates' isolated database, while installation performs a full sync/system upgrade. Exit-code exceptions are specific to their command; separate capture diagnostics distinguish an empty pacman query from a database failure. See [update design](docs/design/updates.md) for repository, deferred-package and reboot limits.

## Resource bounds and failure handling

- One management job runs at a time across modules and processes sharing a state directory. A file lock is held until completion/cancellation and its audit record. A competing write receives a `busy` error. No queue or database is needed.
- Jobs have a 30 minute deadline and are cancelled on server shutdown. Commands default to a 20 minute timeout and reads captured through the runner default to 15 seconds.
- Child commands get their own process group. Default cancellation kills the group and bounds inherited output pipes to two seconds. Package mutations first send SIGINT, allow 30 seconds for native cleanup, then kill the group if necessary; their pipe wait includes that grace period.
- Command capture separates stdout from diagnostics and rejects output above 1 MiB. Job output retains the newest 128 KiB.
- Keep 64 recent jobs in memory; restarting the process clears job history. Job metadata can be listed separately from an individual job's output/result.
- Record start, cancellation request, and terminal state in a private JSONL audit log. Arguments, results, output, and credentials are excluded. Rotate at approximately 4 MiB and keep one previous file.
- Failure to record a job's start prevents execution. A failure to record completion is reported explicitly, even if the host operation already ran.
- Job cancellation stops remaining work; it does not undo changes already made by a backend. Each domain module must handle its own transaction/rollback requirements.

## Authentication and privileges

The server creates a 32 byte random access token in an owner-only file, and keeps its SHA-256 digest for comparison. The token file can be configured; it cannot be a symlink or group/world readable. Browser sign-in exchanges the token for a random HttpOnly, SameSite Strict cookie with an eight hour lifetime. Up to 32 browser sessions are kept in memory; logout removes a session and restart expires them all. The UI does not store the access token in browser storage.

HTTP clients can send a bearer token instead of a cookie. API bodies are limited to 1 MiB, login bodies to 4 KiB, and management requests use JSON. Reject unexpected Host headers and foreign Origin headers, including on requests with bearer tokens. A configured HTTPS `publicOrigin` permits a reverse proxy's external hostname; otherwise the HTTP listener and allowed hosts are local. The health endpoint reveals only whether the process responds.

The main process has the permissions of its operating-system account. With
`privilegedOperations` enabled, a registry execution wrapper routes package/share
operations and system-service operations through the fixed root-owned
`server-man-admin` helper using noninteractive sudo. User services execute as the
main account. The existing registry still owns schema validation, write policy,
jobs and audit for both HTTP and MCP. The helper revalidates schemas and domain
inputs, restricts operation names, rejects user-service scope, fixes privileged
paths, clears inherited environment, and locks across invocations before running
complete domain transactions. It has no transport listener. Root commands retain
bounded execution/output; signals cancel their process groups with package grace
periods. Helper output is bounded and returned when the operation finishes.
Missing helpers, sudo policy, and denied permissions fail explicitly. Future
modules must not add arbitrary privileged command execution. Rootless Podman belongs to the invoking account; running as root does not access another user's rootless inventory.

MCP HTTP reuses this authentication/origin boundary. Stdio is accessed by launching a process under the workload's operating-system account; it does not require a browser token. Each process owns its recent in-memory jobs. See [MCP design and connection examples](docs/design/mcp.md) for lifecycle, result shapes, cancellation, shared-state locking and protocol compatibility.

## Verification

`make check` runs Go vet and frontend type checking. `make test` runs race tests for configuration, authentication, HTTP/MCP policy, tool validation, job bounds/cancellation/locking, output capture, and process timeouts. `make mcp-test` verifies the built stdio server and HTTP handler with the official SDK. Building frontend assets first also enables embedded production asset checks. Command-changing domain integration tests must use fixtures or a command recorder; development verification must preserve the host's actual workloads.

The migrated headless host verifies loopback TCP HTTP, official SDK MCP HTTP, native read-only inventory and headless Firefox over real sockets. The sandbox launcher itself is unavailable, so verification uses the approved host execution path. Privileged mutations remain fixture-verified. See the [verification audit](docs/verification.md) and [operations ledger](docs/operations/host-setup.md) for exact evidence and remaining TLS/provider/CI limits.
