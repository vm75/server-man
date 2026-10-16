# Serverman implementation plan

Status: active. Updated: 2026-10-15. Check off a step only after its acceptance criteria have evidence. Commit each completed step separately, including its tracking update. This plan is the scope and progress authority.

## Goal

A lightweight Linux server manager inspired by Cockpit, using Go, Svelte 5, TypeScript, and Vite. The responsive web UI and MCP expose the same operations for Podman Compose, Docker Compose, LXC/LXD, KVM, systemd services, Samba/NFS shares, package updates, and live resource monitoring inspired by btop. Additional features fit a small Go module interface.

## Observed host conventions

Read-only inspection of `~/vms` on 2026-10-15 found:

- Projects live at `config/podman/<project>/compose.yml`; 22 projects, some with multiple services.
- The launcher sets the Compose project directory and passes `data/data_root.env`, `data/global.env`, then sorted `data/<project>/*.env` files.
- `data/<project>/.disabled` prevents starting a project; stopping remains possible.
- Compose files contain variable based bind mounts (`DATA_ROOT`, `MEDIA_ROOT`), SELinux mount options, devices, host networking, service dependencies, and Podman extensions. Delegate interpretation to Compose; do not rewrite these files or expose resolved environment secrets.
- Docker support should use the parallel `config/docker/<project>/` layout, with configurable roots.
- No KVM or LXC inventory convention was found in that directory. Use libvirt and LXD/classic LXC inventory rather than inventing a filesystem layout.

The workspace's root `.git` is a read-only sandbox mount. The working repository is the `serverman/` subdirectory, with normal Git metadata and commits.

## Design constraints

- A single Go process serves embedded production assets, JSON HTTP APIs, and MCP over stdio and Streamable HTTP. Node is a build/development dependency only.
- Use Go's HTTP server, native Linux `/proc` and `/sys` readings, and installed management CLIs. Use the official Go MCP SDK if available. Avoid a database, message broker, container daemon proxy, separate monitoring agent, or runtime plugin loader.
- A shared module/tool registry owns validation and operations. HTTP and MCP call the same functions. Optional tools are detected; unavailable backends report why without breaking the rest of the application.
- Execute fixed programs and argument arrays, never user supplied shell commands. Bound concurrency, execution time, retained output, and monitoring history. Preserve cancellation.
- Bind to loopback by default. Authenticate management APIs, require same-origin browser mutations, and support bearer tokens for MCP. Separate read access from explicitly enabled writes. Do not mutate this development host's workloads, services, packages, or shares during verification.
- Long operations return tracked jobs with bounded output and audit records. Destructive operations require an explicit parameter and a UI confirmation. Never log credentials or resolved Compose configuration.
- Keep configuration small and explicit. Respect rootless Podman ownership; running as root does not manage another user's rootless containers. Explain privilege requirements and remote TLS deployment accurately.
- Render loading, unavailable, empty, failure, and success states. Mobile controls must remain usable; charts should resize without horizontal page overflow. No chart framework is needed for a few SVG histories.

## Steps and acceptance criteria

### 0. Plan and repository

- [x] Inspect workspace, available toolchains, and existing VM/Compose layout.
- [x] Initialize a new repository and write this detailed tracked plan.
- [ ] Commit the plan and concise project guidance.

Acceptance: a real initial Git commit containing the plan and no copied secrets or host data.

### 1. Runnable application foundation

- [ ] Add Go entry point, typed configuration, graceful shutdown, and health API.
- [ ] Add Svelte 5 + TypeScript + Vite with reproducible dependency lockfile.
- [ ] Serve built assets from the Go binary; support Vite development proxy.
- [ ] Add concise build/development commands and ignore generated artifacts.
- [ ] Add responsive app navigation and explicit connection/error states.

Acceptance: frontend type checking/build and Go build pass; the built executable serves the UI and health endpoint without Node at runtime.

### 2. Shared management boundary

- [ ] Implement module descriptors, tool schemas, capability discovery, and structured errors.
- [ ] Implement authenticated JSON APIs and browser session login/logout.
- [ ] Implement bounded process execution, write policy, cancellable jobs, and audit output.
- [ ] Verify authentication, origin restrictions, validation, cancellation, output limits, and denied writes.

Acceptance: HTTP operations share a validated registry; unauthenticated/invalid/disabled writes cannot launch commands; jobs report terminal state and useful errors.

### 3. Live resource monitoring

- [ ] Collect host identity, uptime, CPU total/per-core, load, memory, swap, disk usage/I/O, network rates, and process activity from Linux.
- [ ] Sample at a bounded interval with a bounded history; share one sampler across clients.
- [ ] Build a btop inspired dashboard with CPU/network histories, memory meters, disks, and sortable processes.
- [ ] Verify counter resets, initial sample behavior, vanished processes, missing sensors, and mobile layout.

Acceptance: real host statistics update in the browser with readable desktop/mobile views; missing data is visible and sampling remains bounded.

### 4. MCP transport and parity

- [ ] Expose registry tools with JSON schemas and useful read/write/destructive annotations.
- [ ] Implement stdio mode without non-protocol stdout and authenticated Streamable HTTP.
- [ ] Document generic local and remote agent connection examples usable by Claw/Hermes where they support these transports.
- [ ] Verify initialize, list tools, invoke monitoring, invalid arguments, and denied writes using a real MCP client.

Acceptance: an MCP client can discover and invoke the same operations available to the UI; no unvalidated alternate command path exists.

### 5. Podman and Docker Compose

- [ ] Discover both roots and supported Compose filenames; handle missing roots and reject path escapes.
- [ ] Preserve project directory, environment file order, and disabled markers from `~/vms/vms.sh`.
- [ ] List project/container state and logs; support up, start, stop, restart, pull, and down as jobs. Do not remove volumes implicitly.
- [ ] Add useful project details and lifecycle controls in UI and MCP.
- [ ] Verify fixture projects modeled on the actual layout with a command recorder, and read-only live discovery when available.

Acceptance: both engines use correct commands and project context; disabled projects cannot start; configuration/env contents are never returned by inventory APIs.

### 6. LXC/LXD and KVM

- [ ] Detect LXD (`lxc`) separately from classic LXC utilities and expose inventory/state.
- [ ] Support LXD launch/start/stop/restart/delete and classic LXC start/stop with explicit capabilities.
- [ ] Use configurable libvirt URI for KVM inventory/details and start/shutdown/reboot/autostart; support defining an existing domain XML and undefining an inactive domain without deleting disks.
- [ ] Add corresponding responsive UI pages and MCP tools, including errors for missing permissions/backends.
- [ ] Verify commands and parsing with fixtures; use live read-only inventory only where available.

Acceptance: all three backends have independent capability states and typed actions; destructive actions require explicit acknowledgement; absent virtualization tools do not break the UI.

### 7. Services and logs

- [ ] List systemd services with state; inspect service details and bounded journal output.
- [ ] Support start/stop/restart/reload/enable/disable with validated unit names and jobs.
- [ ] Provide service search/filter/details in UI and equivalent MCP tools.
- [ ] Verify command construction, parser behavior, failed actions, and system/user scope configuration.

Acceptance: existing services can be inspected and controlled without an arbitrary command or unit-file editing endpoint.

### 8. Samba and NFS shares

- [ ] Read existing shares without rewriting unmanaged host configuration.
- [ ] Manage application owned Samba include/NFS exports fragments: create/edit/remove, validate, atomic write, and apply/reload with rollback on failure.
- [ ] Validate names, absolute paths, access lists, and supported options; document the one-time include/setup and filesystem/user permission requirements.
- [ ] Add share forms and equivalent MCP tools; show configured versus active state.
- [ ] Verify temporary-file workflows, validation failures, rollback, and preservation of unmanaged config. Do not change host shares during verification.

Acceptance: managed shares can be added, changed, removed and applied through both interfaces, and invalid configuration cannot replace working files.

### 9. System updates

- [ ] Detect supported package managers (apt, dnf, pacman, zypper) and expose explicit capabilities.
- [ ] List pending updates; refresh metadata and install updates as cancellable jobs with readable output and reboot-required status where detectable.
- [ ] Serialize package operations and never run distro upgrades or reboot implicitly.
- [ ] Add UI/MCP actions; verify each command and expected package-manager exit status without updating this host.

Acceptance: update operations are usable on supported distributions, report unsupported systems honestly, and cannot overlap or silently reboot.

### 10. Packaging, extension guide, and completion audit

- [ ] Provide example config, systemd unit, install/build instructions, privilege guidance, and a practical remote TLS/proxy example.
- [ ] Document how a new Go module adds tools/capabilities and a UI view without changing transports.
- [ ] Add CI for Go checks, frontend type checking/build, and required integration coverage.
- [ ] Run meaningful backend tests, MCP integration, production build, and browser checks at desktop/mobile sizes.
- [ ] Inspect artifact size and idle behavior; record measured values rather than claim a target without evidence.
- [ ] Audit every original requirement against current implementation and verification evidence. Record environment-limited checks explicitly; do not mark full completion while required evidence is missing.

Acceptance: reproducible production binary; documented operation/extension; functional web and MCP coverage for every requested subsystem; responsive UI; clean Git worktree; committed tracking and verification evidence.

## Verification strategy

Use narrow unit tests for parsing, policy, authentication, and file safety, and fake executable integration fixtures for commands that would change a host. Use the actual Linux host for read-only metrics and discovery. Verify MCP with the official client and the production UI in a browser at approximately 390px and 1440px widths. Build and type check every affected layer before committing a step. Do not substitute fixture success for a claim that privileged host integration was exercised.

## Evidence log

| Step | Evidence | Remaining limits |
| --- | --- | --- |
| 0 | Empty workspace confirmed; Go 1.26.5 and Node 24.18.0 installed; `~/vms/vms.sh` and Compose structure inspected without reading environment values; new repository initialized. | Initial commit pending. |

## Next action

Commit step 0, then implement the runnable foundation in step 1. Keep subsequent work aligned with the full scope above.
