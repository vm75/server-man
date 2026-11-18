# server-man implementation plan

Status: active. Updated: 2026-10-16. Check off a step only after its acceptance criteria have evidence. Commit each completed step separately, including its tracking update. The [single-server release plan](single-server-release.md) is the current deployment scope authority; historical tracking below remains evidence.

## Current deployment priority (2026-11-15)

Prioritize a single-server Ubuntu reference deployment, retaining Debian and Arch Linux as support targets. The product must not depend on a specific workstation, account, or filesystem layout. Focus on container/VM configuration and lifecycle, central Samba/NFS shares, and actionable prerequisite alerts. Backup/restore is deferred; preserve partial implementation. Follow the [single-server release plan](single-server-release.md). Full declarative-phase completion is separate from this initial deployment.

Updated 2026-11-16: defer native Arch testing per user instruction. Keep Arch implementation/package mappings, mark native support unverified, and exclude its test-host/install/workload/boot acceptance from current release gates. Continue active Ubuntu/Debian work; do not resume native Arch testing until requested.

## WS-Discovery setup repair (complete, 2026-11-17)

- [x] Share Samba dependency presets between guided installation and share setup; request `wsdd-server` on APT systems and enable/start `wsdd-server.service`, retaining `wsdd`/`wsdd.service` for other supported managers.
- [x] Report discovery package and enable/start failures instead of silently succeeding with Samba alone. Keep NetBIOS service setup optional.
- [x] Carry discovery server-unit availability through native/helper share inspection into the UI, so installing only the `wsdd` executable cannot hide the missing server component. Bound service discovery to three seconds and describe unavailable discovery service state accurately.

Verification: package/service fixtures cover native package mappings, missing discovery package errors without fallback installation, enable/start failures, both discovery unit names, and helper-to-UI status propagation. `make check`, `make build`, and `make test` (Go race tests) pass. A read-only APT simulation confirmed that `wsdd-server` selects both `wsdd` and `wsdd-server` without package removals. An initial test run overlapped frontend asset regeneration and failed the embedded-asset compilation; the full race suite was rerun after the build completed. No host packages, services, shares or firewall settings changed; native daemon startup and Windows discovery remain unverified.

Runtime follow-up: the development API was current but the installed admin helper differed from the rebuilt binary, so privileged setup still ran the previous Samba-only path. Replaced only the installed helper with the verified binary and confirmed identical SHA256 hashes. Its read-only Samba inspection succeeds and reports `available: true`, `discoveryAvailable: false`, matching the absent discovery service. Documented that Air/build do not update the installed helper and that `make dev-admin` is required after privileged code changes. No native package installation or service changes ran; the user's next setup attempt remains the native mutation check.

## VS Code development launcher (complete, 2026-11-17)

- [x] Add a compound launcher for Vite and the Go API in integrated terminals, with shared stop behavior and no additional editor extension requirement.
- [x] Retain Vite HMR and add `make api-watch` using pinned Air v1.67.4, installed under the ignored cache on first use. Watch application Go sources and module files, exclude frontend/generated files and tests, and interrupt the old process before replacement.
- [x] Document launcher use, first-run download, compile-error recovery, and the distinction between automatic Go restarts and breakpoint debugging.
- [x] Support headless remote development through the existing unified YAML listener/public origin. Both API development commands enable `-dev-ui`, proxying UI assets and the HMR WebSocket to loopback Vite while preserving native API/MCP routing and Host/Origin policy.

Verification: launcher JSON and Air TOML parse; compound references and shutdown settings validate; `make -n` confirms existing and new development commands; `go build -o .cache/dev-api-verify ./cmd/server-man` and `git diff --check` pass. An isolated temporary Go fixture using the actual Make target and Air configuration verified initial startup, graceful source-change restart, compile-error shutdown, recovery after correction, module-file rebuilds, exclusion of frontend/test edits, and interrupt cleanup without surviving child processes. The fixture disabled VCS stamping because its temporary module was outside a Git repository. No host management operations ran. VS Code GUI launch/stop and browser HMR were not exercised; Vite's existing development configuration is unchanged.

Headless follow-up verification: `make check`, `make test` (Go race tests), and an application build pass. Added regression coverage for native API/MCP authentication, health routing, and foreign UI/API/WebSocket Host/Origin rejection in development mode. A real TCP fixture loaded its listener and public origin from a temporary default XDG YAML, proxied temporary frontend files through Go to the existing local Vite server, signed in and read authenticated tool discovery, then received a real Vite HMR file-update message over a WebSocket through Go. Temporary configuration/state/frontend files were removed; existing Vite and host management state were preserved. The test used a documentation Host/Origin with loopback TCP, so routing/firewall access from a second physical machine and VS Code GUI behavior remain unverified. Vite's configuration remains unchanged.

## Next phase: declarative management (in progress, 2026-11-14)

The [declarative management implementation plan](declarative-management.md) is the scope and progress authority for the next phase. It supersedes this document's legacy workload discovery, Compose authoring, LXD/classic LXC, and fragment-owned share configuration requirements for that work. No compatibility or migration layer is required. Existing evidence below describes the earlier implementation, not completion of the new plan. The new phase follows DRY, KISS, and YAGNI and ends with architecture updates and consolidation of obsolete planning material. Partial implementation and verification are recorded in that plan; native acceptance remains open.

## Versioning and CI policy (2026-11-14)

- [x] Add a committed prerelease `VERSION` authority and retain embedded `-version`, API and UI version reporting. Clean exact matching tags identify manual prereleases; untagged builds include a commit identity.
- [x] Limit GitHub CI triggers to PRs targeting `main` (including new commits), pushes to `main`, and manual dispatch. Neither feature-branch nor tag pushes start CI.
- [x] Document manual prerelease tagging, the absence of automatic publication, and the requirement not to equate releases with completion of the active declarative plan.
- [ ] Verify the new PR CI and an eventual post-merge CI run on GitHub; no tag, release or deployment is part of this change.

Verification scope: `sh -n` passed for the revised build script; an isolated Git repository with a stub `go` command verified untagged commit metadata, exact annotated-tag naming, dirty-tree identification, override handling and invalid-`VERSION` rejection. No full repository build, hosted workflow or native integration test has been run for this change. See [versioning and CI policy](../versioning.md).

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

Historical acceptance used a writable Git checkout. No repository checkout location is a server-man runtime requirement. Earlier records describe a separate test environment and do not prescribe the current host layout.

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
- [x] Commit the plan and concise project guidance.

Acceptance: a real initial Git commit containing the plan and no copied secrets or host data.

### 1. Runnable application foundation

- [x] Add Go entry point, typed configuration, graceful shutdown, and health API.
- [x] Add Svelte 5 + TypeScript + Vite with reproducible dependency lockfile.
- [x] Serve built assets from the Go binary; support Vite development proxy.
- [x] Add concise build/development commands and ignore generated artifacts.
- [x] Add responsive app navigation and explicit connection/error states.

Acceptance: frontend type checking/build and Go build pass; the built executable serves the UI and health endpoint without Node at runtime.

### 2. Shared management boundary

- [x] Implement module descriptors, tool schemas, capability discovery, and structured errors.
- [x] Implement authenticated JSON APIs and browser session login/logout.
- [x] Implement bounded process execution, write policy, cancellable jobs, and audit output.
- [x] Verify authentication, origin restrictions, validation, cancellation, output limits, and denied writes.

Acceptance: HTTP operations share a validated registry; unauthenticated/invalid/disabled writes cannot launch commands; jobs report terminal state and useful errors.

### 3. Live resource monitoring

- [x] Collect host identity, uptime, CPU total/per-core, load, memory, swap, disk usage/I/O, network rates, and process activity from Linux.
- [x] Sample at a bounded interval with a bounded history; share one sampler across clients.
- [x] Build a btop inspired dashboard with CPU/network histories, memory meters, disks, and sortable processes.
- [x] Verify counter resets, initial sample behavior, vanished processes, and missing sensors.
- [x] Verify the rendered desktop/mobile layout and live browser updates. Firefox screenshots at 1440×1000 and 390×844 show the production UI through the production handler bridge; browser polls observed advancing Linux samples and all seven routes passed overflow checks.

Acceptance: real host statistics update in the browser with readable desktop/mobile views; missing data is visible and sampling remains bounded.

### 4. MCP transport and parity

- [x] Expose registry tools with JSON schemas and useful read/write/destructive annotations.
- [x] Implement stdio mode without non-protocol stdout and authenticated Streamable HTTP.
- [x] Document local and remote agent connection examples, including documented OpenClaw/Hermes configuration surfaces.
- [x] Verify initialize, list tools, invoke monitoring, invalid arguments, and denied writes using the official SDK client.

Acceptance: an MCP client can discover and invoke the same operations available to the UI; no unvalidated alternate command path exists.

### 5. Podman and Docker Compose

- [x] Discover both roots and supported Compose filenames; handle missing roots and reject path escapes.
- [x] Preserve project directory, environment file order, and disabled markers from `~/vms/vms.sh`.
- [x] List project/container state and logs; support up, start, stop, restart, pull, and down as jobs. Do not remove volumes implicitly.
- [x] Add useful project details and lifecycle controls in UI and MCP.
- [x] Verify fixture projects modeled on the actual layout with a command recorder, and read-only live discovery when available.

Acceptance: both engines use correct commands and project context; disabled projects cannot start; configuration/env contents are never returned by inventory APIs.

### 6. LXC/LXD and KVM

- [x] Detect LXD (`lxc`) separately from classic LXC utilities and expose inventory/state.
- [x] Support LXD launch/start/stop/restart/delete and classic LXC start/stop with explicit capabilities.
- [x] Use configurable libvirt URI for KVM inventory/details and start/shutdown/reboot/autostart; support defining an existing domain XML and undefining an inactive domain without deleting disks.
- [x] Add corresponding responsive UI pages and MCP tools, including errors for missing permissions/backends.
- [x] Verify commands and parsing with fixtures; use live read-only inventory only where available.

Acceptance: all three backends have independent capability states and typed actions; destructive actions require explicit acknowledgement; absent virtualization tools do not break the UI.

### 7. Services and logs

- [x] List systemd services with state; inspect service details and bounded journal output.
- [x] Support start/stop/restart/reload/enable/disable with validated unit names and jobs.
- [x] Provide service search/filter/details in UI and equivalent MCP tools.
- [x] Verify command construction, parser behavior, failed actions, and system/user scope configuration.

Acceptance: existing services can be inspected and controlled without an arbitrary command or unit-file editing endpoint.

### 8. Samba and NFS shares

- [x] Read existing shares without rewriting unmanaged host configuration.
- [x] Manage application owned Samba include/NFS exports fragments: create/edit/remove, validate, atomic write, and apply/reload with rollback on failure.
- [x] Validate names, absolute paths, access lists, and supported options; document the one-time include/setup and filesystem/user permission requirements.
- [x] Add share forms and equivalent MCP tools; show configured versus active state.
- [x] Verify temporary-file workflows, validation failures, rollback, and preservation of unmanaged config. Do not change host shares during verification.

Acceptance: managed shares can be added, changed, removed and applied through both interfaces, and invalid configuration cannot replace working files.

### 9. System updates

- [x] Detect supported package managers (apt, dnf, pacman, zypper) and expose explicit capabilities.
- [x] List pending updates; refresh metadata and install updates as cancellable jobs with readable output and reboot-required status where detectable.
- [x] Serialize package operations and never run distro upgrades or reboot implicitly.
- [x] Add UI/MCP actions; verify each command and expected package-manager exit status without updating this host.

Acceptance: update operations are usable on supported distributions, report unsupported systems honestly, and cannot overlap or silently reboot.

### 10. Packaging, extension guide, and completion audit

- [x] Provide example config, systemd units, install/build instructions, privilege guidance, and a practical remote TLS/proxy example.
- [x] Document how a new Go module adds tools/capabilities and a UI view without changing transports.
- [x] Add CI for Go checks, frontend type checking/build, and required integration coverage (workflow committed; hosted execution remains unverified).
- [x] Run meaningful backend tests, MCP integration, production build, and rendered browser checks at desktop/mobile sizes.
- [x] Inspect artifact size and idle behavior; measured values and measurement limits are recorded.
- [x] Audit every original requirement against implementation and verification evidence; remaining live-integration gaps are explicit.

Acceptance: reproducible production binary; documented operation/extension; functional web and MCP coverage for every requested subsystem; responsive UI; clean Git worktree; committed tracking and verification evidence.

### 11. Compose project authoring

- [x] Create projects and edit Compose plus project-specific `.env` files through the shared authenticated registry.
- [x] Create nested directories under each project's data root and restrict editor operations to the matching config/data directories.
- [x] Validate resolved Compose configuration before saves and lifecycle operations; reject host paths, privileges, devices, and other host access outside the project policy.
- [x] Keep secret file reads gated by `allowWrites`, document plain HTTP exposure, and verify UI/MCP write-disabled behavior.

Acceptance: the web UI and MCP share project file operations; stale edits, symlink/traversal escapes, and unsafe host access are rejected; file modes are private; no env contents enter logs or job output.

## Verification strategy

Use narrow unit tests for parsing, policy, authentication, and file safety, and fake executable integration fixtures for commands that would change a host. Use the actual Linux host for read-only metrics and discovery. Verify MCP with the official client and the production UI in a browser at approximately 390px and 1440px widths. Build and type check every affected layer before committing a step. Do not substitute fixture success for a claim that privileged host integration was exercised.

## Evidence log

| Step | Evidence | Remaining limits |
| --- | --- | --- |
| 0 | Initial commit `a20a0ef`; Go 1.26.5 and Node 24.18.0 installed; `~/vms/vms.sh` and Compose structure inspected without reading environment values. | Root `.git` is read-only; nested repository is fully writable. |
| 1 | `make check` passes (Go vet, Svelte/TypeScript: zero errors/warnings); `make build` passes; `make test` passes with race detection, including embedded index/JS responses, asset boundaries, health, and strict config. Initial stripped binary: 6.2 MiB; UI JS: 42.33 kB (16.84 kB gzip). | TCP listen is blocked by sandbox (`socket: operation not permitted`), so live executable/browser acceptance remains outstanding. Registry DNS is unavailable; dependencies were installed from existing local cache using standard lockfile metadata. npm lifecycle scripts are disabled because the supplied toolchain packages build without them; the esbuild install script's synchronous spawn is blocked here. |
| 2 | Shared registry and bounded runner/jobs implemented; private token/session login/logout and authenticated HTTP tool invocation are wired into the UI. Go race tests cover schema/policy, destructive acknowledgement, sessions/origins, job serialization/cancellation/retention, audit failure, separate stdout/stderr, capture limits and real helper-process timeouts. `make check test build` passes (zero frontend diagnostics). | Live listener/browser verification remains blocked by the sandbox. MCP parity evidence is recorded in step 4. No actual host management changes were executed. |
| 3 | Linux collector, bounded shared sampler and dashboard implemented. Race tests verify real advancing Linux samples, concurrent readers, authenticated HTTP monitoring, counter/PID resets, missing data and history/query bounds. Firefox rendered desktop and mobile views via production handler pipe bridge; all seven routes passed horizontal overflow checks and browser polls observed advancing sample timestamps. Current build assets are 141.53 kB JS / 48.62 kB gzip and 38.20 kB CSS / 8.40 kB gzip. | Real TCP/TLS is blocked by socket policy; Chromium remains blocked. Idle measurements are recorded under step 10 and represent a quiet stdio process in this namespace. |
| 4 | `make check build test mcp-test` passes; official TypeScript SDK 1.30.0 initializes the actual stdio binary and Streamable HTTP handler, discovers 7 tools, reads real Linux metrics, and verifies schemas/error results/denied writes. Tool/schema parity with HTTP API and origin/authentication rejection pass. Go race tests cover malformed/oversized messages, lifecycle/cancellation, version negotiation and shared jobs/destructive acknowledgements. Agent access page and source-linked OpenClaw/Hermes examples added; processes sharing state serialize mutations with flock. | Go SDK download is blocked by DNS socket policy, so isolated standard-library adapters implement the documented tools-only protocol. MCP HTTP is verified in process through its real handler; live TCP/TLS and browser checks remain outstanding. OpenClaw/Hermes themselves were not launched. |
| 5 | `make check build test mcp-test` passes: zero frontend diagnostics, Go race tests, production assets/binary, and official SDK discovery/parity for 16 tools. SDK checks also exercise Compose discovery, invalid identifiers and denied writes. Command fixtures cover both engines, all six actions, project/env context, markers, path escapes, custom project labels, JSON variants, pod association and bounded logs. Read-only discovery finds the existing 22 Podman projects without reading env values. Final Activity completion tracking fix passes `make check build`; completion now refreshes projects even when the drawer is closed or another job is selected. | No real container mutations were executed. Podman runtime setup fails on the sandbox's read-only runtime directory; Docker Compose version detection succeeds, but live runtime inventory and provider execution remain unverified. Desktop/mobile browser and live network checks remain outstanding. |
| 6 | `make check build test mcp-test` passes: zero frontend diagnostics, Go race tests, production build, and official SDK/schema parity for 31 tools. Fixtures verify explicit LXD/project, classic path and libvirt contexts; every action; missing/partial tools; runtime/parser failures; whitelisted metadata; numeric identity mismatch; UUID mutation targeting; stopped/inactive removal guards; XML size/directive/symlink/FIFO rejection and snapshot cleanup. Machine inventory, detail/action controls, LXD launch, KVM XML definition and autostart are wired into the UI. | Classic LXC utilities and virsh are absent on this host. LXD's installed Snap client cannot create its runtime/home directories in the restricted filesystem. Real daemon inventory/mutations and browser rendering remain unverified. No actual guest/container/domain/storage changes were executed. |
| 7 | `make check build test mcp-test` passes with zero frontend diagnostics and official SDK/schema parity for 40 tools. Services UI provides name/state filtering, selected details, bounded journal and six management jobs. Race-tested fixtures verify both scopes, all action arguments, enablement without `--now`, installed/runtime merging, JSON and legacy columns, partial failures, missing utilities, property privacy, identifier validation, journal bounds and failed commands. SDK checks service inventory, invalid patterns and denied writes. Final mobile tab CSS adjustment passes frontend checks/build; added canonical-alias fixture passes the race suite. | Installed systemd is 259.5; read-only live queries cannot connect to the system bus due to sandbox policy. Real system/user manager control, journal permissions and browser rendering remain unverified. No actual host service/startup changes were executed. |
| 8 | Shares UI and five shared tools provide inventory, current revisions, owned CRUD/reapply and review dialogs. Fixtures verify candidate validation before publication, atomic files, stale/manual/symlink/FIFO rejection, ACL/path validation, whitelisted metadata, exact targeted NFS commands, unmanaged file/data/runtime preservation, partial native failures, first-create cleanup, cancellation recovery and explicit rollback failures. Installed testparm validates temporary guest/authenticated shadow configurations and inherited global defaults without daemon reload. Official SDK parity now covers 45 tools, share inventory/invalid protocol/annotations and denied writes. Full `make check build test mcp-test` passes with zero frontend diagnostics; focused race tests cover additional overlap/runtime mismatch and cancellation diagnostics. | No live Samba reload, NFS export/client mount or host permission changes were performed. NFS utilities are absent here. Samba connection counts and NFS registration have explicit runtime limits; neither proves client usability. Rendered mobile/desktop and live network checks remain outstanding. |
| 9 | Updates UI and three shared tools implement native-family detection, cached candidates/deferred entries, refresh/install jobs and reboot evidence. Fixtures cover all four families and DNF4/5 fallback, exact commands, phase failure, command-specific exit codes, diagnostic-sensitive empty results, shared mutation locking, acknowledgement and cancellation. Real helper processes verify SIGINT and grace-period group cleanup. Read-only APT 3.2.0 simulation exposed native dependency-state suffixes; regression fixtures and the live parser test now pass. `make check build test mcp-test` passes with zero frontend diagnostics and official SDK/schema parity for 48 tools, including update status, invalid arguments and denied refresh/install. | No host metadata refresh, package installation, migration or reboot was executed. DNF/Pacman/Zypper mutation behavior is fixture-verified; repository policies and client effects require native host integration. Browser and network checks remain outstanding. |
| 10 | Added staged native install, system/user units, config and Caddy examples, versioned build, extension guide, CI, browser verification, and requirement matrix. Local `make check test mcp-test ui-test` passes; Firefox verified layouts at 1440px, 390px, and 320px, live timestamps, sign-in/out, overflow, Compose restart, LXD launch, service stop, Samba share creation and package installation confirmation flows. A Samba create/review flow also passed at 390px. Browser assertions verified arguments and that the actual write-disabled handler rejected each fixture mutation. A staged install produced a 7,983,369-byte binary plus the example config and both units. Quiet stdio sampler measurement (12 seconds): 0.06 CPU seconds / 0.50% one core, 14,528 KiB end RSS, 11 threads, 6 FDs. | GitHub Actions itself cannot be run from this checkout. Socket creation/bind/listen and live TCP/TLS verification remain unavailable; systemd unit semantic verification is blocked by sandbox credential-socket setup. Provider responses for browser interaction flows are fixtures. See [verification audit](../verification.md) for the complete requirement matrix and boundaries. |
| 11 | `make check`, `make test` (race), and `make mcp-test` pass; official SDK discovers 54 tools over stdio and production-handler Streamable HTTP with parity/auth/policy checks. Compose fixtures verify revisions, private modes, folder boundaries, symlink/traversal rejection, and unsafe mount/privilege rejection. Firefox rendered 1440px, 390px, and 320px layouts and exercised project creation, Compose/env saves and data folder creation; all fixture mutations were rejected by the real write-disabled handler. The UI verifier printed its success result but stalled during process cleanup and was interrupted. Built commit `d016f2c` is installed; the user service is active on `0.0.0.0:8080`, health returns 200, and authenticated LAN MCP lists all 54 tools including the six authoring tools. | The live configuration has `allowWrites:true`; no project files or containers were changed. Real-provider mutations remain untested. Plain HTTP env content is visible to authenticated clients on the trusted LAN. |

## Next action

The planned implementation steps have code and local evidence. The host migration follow-up below is complete: headless loopback service, HTTP/MCP over real TCP, native read-only inventories and discoverable examples are verified. Broader external TLS, hosted CI and privileged subsystem integration gates remain open; see the [verification audit](../verification.md). Future integration work should use disposable guests/workloads and a deliberately configured TLS boundary.

## Host migration follow-up (2026-10-16)

Historical record: the compatibility-preserving naming policy below was superseded by the clean rebrand in the [declarative management plan](declarative-management.md#product-naming). Current changes use `server-man` and `SERVER_MAN_` without legacy aliases or migration support. This section records earlier work, not instructions to deploy or migrate this checkout.

Requested end state: canonical `server-man` display name with compatibility
preserved; buildable checkout; prepared local service; safe discoverable examples
under this account's `~/vms`; complete host change record.

- [x] Migrate visible branding, MCP identity and package labels; document stable identifiers and verify parity/UI.
- [x] Install missing prerequisites and prepare three stopped Compose examples, preserving env ordering and disabled markers.
- [x] Build and verify the current host over loopback HTTP/MCP and install a private user service.
- [x] Record every host change and verification limit in [host setup](../operations/host-setup.md).

Existing live privileged subsystem and hosted CI gaps remain separate acceptance
limits; this follow-up does not mark them complete.

Branding evidence: `make build check test mcp-test` passes after rebuilding copied
assets. Official SDK checks assert `server-man` name/title on both transports
and parity for 48 unchanged tools. `npm --prefix tools/ui run verify` passes
Firefox at 1440×1000, 390×844 and 320×740, including login branding/title, all
routes, live samples, fixture confirmation flows and denied production writes.
The first runs exposed stale assets and missing browser libraries; both were
resolved before acceptance. Share ownership bytes and compatibility identifiers
were preserved.

Dependency follow-up: setup reported security advisories. Updated the exact
verification SDK 1.30.0 → 1.32.1 and compatible transitive packages (`devalue`
5.9.4, `source-map-js` 1.2.2, `fast-uri` 3.1.8, `hono` 4.13.13,
`ip-address` 10.7.4, `proxy-addr` 2.0.8). `make check mcp-test ui-test`
passes with the new lockfiles, including official SDK parity and all three
Firefox sizes. All three `npm audit --json` reports have zero findings.
Dependencies remain development/build-only; Go has no external modules.

Host preparation evidence: installed Go 1.27.1, Node 24.19.0, rootless Podman
5.8.6 and standalone Compose v2 2.40.3 using signed Ubuntu packages. All three
example definitions pass native Compose `config --quiet` with ordered env files.
The rootless provider returns empty successful status. Files were copied only
into an absent `~/vms`, root mode 0700. Images/containers remain empty; Docker
engine, classic LXC, libvirt, LXD pools and guests were not provisioned. The
[operations ledger](../operations/host-setup.md) lists all packages and changes,
including the Ubuntu LXD launcher installation side effect.

Deployment evidence: `/usr/local/bin/server-man -version` is `e4424dd`; its hash
matches the built binary. User unit enabled/active, zero restarts, private token
0600 and state 0700, linger enabled, root unit disabled/inactive. Both systemd
units verify. Real TCP `/healthz`, authenticated HTTP inventory, 401/403 policy,
all three projects and both Podman not-created statuses pass. Official SDK checks
48 schemas, identity, native discovery, monitoring and denied writes over real
TCP MCP. Headless Firefox directly tests desktop/mobile login, both provider
tabs, examples and logout with no page errors/overflow. No image, container,
guest, host share or application update job was created. Full package/config/
service/permission/command outcomes, including failed inspection/test attempts
and LXD install side effects, are in the operations ledger. Later commits are
documentation-only; the deployed code is unchanged from `e4424dd`.

Migration task status: complete. Earlier implementation-wide privileged/TLS/CI
acceptance limits remain explicitly open and were not claimed as completed.

## Direct LAN access follow-up (2026-10-16)

Historical: the HTTPS deployment below was retired per the user's later request;
the replacement plain HTTP listener is tracked in the follow-up after it.

The user requested access from another machine without an SSH tunnel.

- [x] Configure HTTPS at `https://192.0.2.10` using a LAN-bound Caddy proxy, preserving loopback backend and token/origin policy.
- [x] Verify TLS, browser login/session behavior, HTTP/MCP parity and denied writes through the proxy; record client trust requirements and every host change.

Evidence: reviewed/validated Caddy configuration installed before package startup;
LAN-only TCP 443, no plaintext/HTTP3 listener, source restriction rejects a
non-LAN source. Four native packages and Caddy account/service/state are recorded
in the operations ledger. App publicOrigin updated atomically with a private
backup; existing token/write policy preserved. Real HTTPS curl, Node fetch and
official SDK pass trusted chain/IP, 401/403 policy, Secure session lifecycle,
48 schemas and native discovery. Headless Firefox imports the CA into a
temporary profile and passes login/live samples/project rendering/logout without
TLS bypass. Another physical client is untested and must import the exported
public CA. No Go/frontend code changed, so relevant verification is the actual
proxy/service/auth/browser path. Direct LAN follow-up complete.

## Direct LAN HTTP follow-up (2026-10-16)

- [x] Serve the app directly over HTTP on `0.0.0.0:8080` with a pinned LAN
  `publicOrigin`, preserving token authentication and disabled writes.
- [x] Stop and remove the HTTPS proxy and its CA/client-trust artifacts.

Evidence: rebuilt and installed the app; the user service reports
`0.0.0.0:8080`, and `ss` shows no TCP 80 or 443 listener. Loopback and LAN
health checks return 200; unauthenticated API returns 401; foreign Origin is
rejected; browser login/session/logout pass with HttpOnly, SameSite Strict, and
no Secure cookie flag. The jobs tool reports no active jobs and `allowWrites`
remains false. Official SDK MCP over HTTP lists 48 tools and passes ping.
Purging Caddy and its three dependencies removed exactly four packages. Other
physical clients are untested. No Go test suites were run for this
deployment-only change. See the [HTTP host ledger](../operations/host-setup.md#direct-lan-http-follow-up-2026-10-16).

## Run as X with explicit administrative operations

The active follow-up and its verification evidence are tracked in
[privileged helper implementation](privileged-helper.md). This supersedes the
original single-account privilege assumption without replacing earlier evidence.
