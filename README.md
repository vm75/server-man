# server-man

A lightweight Linux server manager inspired by Cockpit, with a responsive web interface and MCP access for AI agents. Built with Go, Svelte 5, TypeScript, and Vite.

The immediate goal is a [single-server deployment](docs/plans/single-server-release.md), first on Ubuntu, with Debian and Arch Linux as support targets. One portable YAML configuration owns managed containers, VMs, storage references and file shares. Applications supports Podman Quadlet (default), Podman Compose, Docker Compose, existing-disk KVM/libvirt and local Incus VMs. Configuration and File shares provide typed editors and reviewed changes. Start/Stop controls current execution; Enable/Disable controls boot startup independently. Native lifecycle and boot acceptance remains incomplete; see [current behavior and limits](docs/design/declarative.md).

Backup/restore is deferred and is not required for the initial deployment. Partial capture and interrupted-capture recovery remain available, but captures are not restorable backups. Existing recovery journals still require explicit resolution before managed mutations. The [broader declarative plan](docs/plans/declarative-management.md) retains future work.

The overview provides bounded Linux resource histories and process inventory. Services manages host system/user systemd units and bounded journal output. Updates manages native packages directly on the host. These host operations remain outside workload configuration. Managed application units shown in Services route lifecycle and startup changes through the application's configuration and coordinator.

File shares derives owned Samba/NFS fragments from central definitions, validates candidates and attempts rollback on native publication failure, preserving unmanaged configuration and shared data. Host daemon/package setup remains explicit. Missing dependencies and file/mount prerequisites appear in a global configuration alert and per-resource issues. Checks run when the UI opens and can be refreshed explicitly; complete post-port validation remains in development.

HTTP and MCP share authentication/write policy, validated operations, serialized jobs and bounded output/audit. See [installation](docs/installation.md), [architecture](ARCHITECTURE.md), and [monitoring](docs/design/monitoring.md).

## Install

From source, run as your normal account:

```sh
./install.sh --user YOUR_ACCOUNT --backends all
```

Or extract a release archive and run the same command; releases include binaries and do not need build tools. Use `--dry-run` to review changes, or choose a subset with `--backends podman,docker,virtualization,incus,samba,nfs`. The installer configures native dependencies, the privileged helper, backend groups and a system service running as your workload account. Existing configuration is preserved. Incus storage/network and workloads/shares remain explicit setup. See [installation](docs/installation.md). Build release bundles with `make release`.

## Build and run

Requires Linux, Go 1.26 or newer, Node.js 22.12 or newer, npm, and Make. Node is needed to build the UI, and is not needed to run the resulting executable.

```sh
make setup
make check
make test
make mcp-test
make ui-test
make build
./bin/server-man
```

Application versions use the committed [`VERSION`](VERSION) file and pre-1.0 prerelease tags while the implementation plan is unfinished. Untagged builds include a Git commit suffix; a clean build at an exact matching `v<VERSION>` tag reports the release version. `./bin/server-man -version`, `/api/status` and the UI footer expose it. `SERVER_MAN_VERSION` remains available as an explicit build override. See the [versioning and CI policy](docs/versioning.md) for tagging and release steps. The Go module, executables, service units, UI and native ownership markers use `server-man`.

Open `http://127.0.0.1:8080`. Sign in with the access token from the private file created on first start. The startup log shows its path; the default is `~/.config/server-man/token` (or under `XDG_CONFIG_HOME`). The server binds to loopback. `GET /healthz` provides a small unauthenticated JSON health response.

Management writes are disabled by default. When enabled, jobs run one at a time and appear in **Activity**. Instances using the same state directory also serialize writes through a file lock. Cancelling a job stops its remaining work; it does not undo completed changes. Package commands first receive an interrupt and have up to 30 seconds before forced termination; interrupted package transactions can require native recovery. Each process retains 64 recent jobs in memory and records audit metadata under `~/.local/state/server-man` (or `XDG_STATE_HOME`).

## Development

Run `make api` in one terminal and `make dev` in another. Open the Vite address (`http://127.0.0.1:5173`); Vite proxies API requests to Go. Rebuild with `make build` to embed updated frontend assets in the executable. A Go-only build without generated UI assets serves APIs and an explanatory 503 response for the UI.

server-man uses one private YAML configuration: `~/.config/server-man/server-man.yaml` (under `XDG_CONFIG_HOME` when set). Select another absolute path with `./bin/server-man -config /absolute/path/server-man.yaml`.

```yaml
version: 1
server:
  listen: 127.0.0.1:8080
  allow_writes: false
  sample_interval_seconds: 2
services: {}
storage: {}
shares: {}
bindings: {}
secrets: {}
```

The `server` section contains application settings; the other sections define managed resources. UI/MCP edits preserve the server section in the same revision-checked file. There is no JSON/TOML configuration or `manifestPath`. Relative workload paths resolve against this YAML's directory. Referenced private files and runtime state remain separate data, rather than additional configuration authorities.

Resource changes follow the reviewed native apply workflow. Server-setting changes require a process restart and must be saved separately from resource changes. The UI review and global alert identify a pending restart; managed operations are blocked until the running settings match the file. Restart an installed deployment with `sudo systemctl restart server-man`.

`server.sample_interval_seconds` ranges from 1 to 10. Monitoring history retains at most 60 samples. For trusted LAN access, set `server.listen: 0.0.0.0:8080` and `server.public_origin: http://192.168.1.50:8080`, replacing the IP with the server's address. For other networks use an HTTPS reverse proxy; see [installation and remote access](docs/installation.md).

The declarative configuration foundation exposes `managed_config_read`, `managed_config_validate`, and `managed_config_health` through the shared API/MCP registry. The CLI `-config` option selects the same unified YAML used at startup. Private editor reads require `server.allow_writes`. Preview/save/apply-status/retry operations also use the shared registry and serialized jobs. Desired saves are journaled; initial Podman Quadlet, Docker/Podman Compose, existing-disk libvirt and local Incus adapters and `managed_service_start/stop/restart/enable/disable` tools are connected. The Applications page shows status, startup policy, configuration issues and lifecycle controls. `managed_config_check` refreshes prerequisites and mount/file checks. Compose currently uses boot-only user units with no container crash restart policy. Applications and File shares provide typed definition/edit/review forms; Configuration manages storage, bindings, private-file references, YAML recovery and private history inspection/reviewed revert. Storage references offer a separate reviewed new-directory/disk operation, including checksum-pinned local image cloning; existing/restored data is never replaced. Complete VM provisioning and full runtime verification remain in development. `managed_shares` and `managed_share_publish` inspect and change central share publication through shared native validation/rollback. Legacy Compose discovery/editing, LXD/classic LXC tools and direct native share-edit APIs have been removed. See [current behavior and limits](docs/design/declarative.md).

Native lifecycle acceptance is separate from normal checks. `make mcp-vm-test` requires an explicitly acknowledged disposable guest and prepared backend fixtures; see [guest acceptance](docs/operations/disposable-acceptance.md). It never runs automatically as part of `make mcp-test`.

## HTTP API

All `/api/` routes require authentication except token sign-in. Send `Authorization: Bearer <token>` for an API client, or use the browser session cookie. Tokens in URL query parameters are not supported.

| Endpoint | Behavior |
| --- | --- |
| `POST /api/session` | Sign in using a JSON object with a `token` field |
| `DELETE /api/session` | Sign out the current browser session |
| `GET /api/status` | Server identity and write policy |
| `GET /api/tools` | Module availability and tool schemas |
| `POST /api/tools/<name>` | Invoke a tool with a JSON arguments object |

Core tools include `system_status`, `system_capabilities`, `monitor_snapshot`, `monitor_processes`, `jobs_list`, `jobs_get`, and `jobs_cancel`. `monitor_processes` accepts `sort`, `filter`, and `limit`. Mutation tools return a `job`; use `jobs_get` with its `id` to read state/output. `jobs_cancel` is an immediate control operation.

Managed workloads use `managed_config_*`, `managed_resource_plan/save`, `managed_apply_status/retry`, `managed_services`, and `managed_service_start/stop/restart/enable/disable`. Shares use `managed_shares` and `managed_share_publish`; `shares_list` remains read-only native inspection. Native host services use `services_list/details/logs` and six management actions. Updates exposes `updates_status/refresh/install`, `packages_install`, and `dependencies_install`; `shares_setup` explicitly prepares native share services. `GET /api/tools` and Agent access provide complete current schemas. Foreign browser origins and unexpected Host headers are rejected.

## MCP

Open **Agent access** in the UI for discovery and connection details. The running server provides Streamable HTTP at `/mcp`, authenticated with the same server access token. Local agents can launch a separate stdio process:

```sh
/absolute/path/to/server-man -transport stdio -config /absolute/path/to/server-man.yaml
```

Use HTTP to share the web server's jobs and history. A local stdio process has its own history and cancels unfinished work when it exits. See [MCP connection details and Hermes/OpenClaw examples](docs/design/mcp.md) for setup, schemas, protocol limits, and the verification scope. `make mcp-test` checks both transports with the official SDK; Node and its SDK are only verification dependencies.

See [architecture](ARCHITECTURE.md) for shared operation boundaries, resource limits, and authentication, and [project guidance](AGENTS.md) for working commands.

This checkout is rooted at `/home/shasak/ws/server-man` with its own writable `.git` directory. See the [host setup record](docs/operations/host-setup.md) for this server’s deployment and example workloads.

This headless host is available on the LAN at **http://192.168.192.78:8080**, with token authentication and writes disabled. See the [host setup record](docs/operations/host-setup.md#direct-lan-http-follow-up-2026-10-16).

Run the application as your workload account with `privilegedOperations: true`
and the restricted sudo helper to administer packages, system services, and
Samba/NFS while retaining your own user services and rootless containers. See
[installation](docs/installation.md#run-as-your-workload-account). The Updates page
also installs named repository packages (`packages_install`), and Shares offers
explicit native server setup (`shares_setup`); both require confirmation and
`allowWrites`.

Updates also offers **Install dependencies** (`dependencies_install`) for
Podman/Compose, Docker/Compose, Samba, NFS, x86 QEMU/libvirt using
native repositories. Restart the app afterward to refresh provider discovery.
