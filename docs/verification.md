# Verification and completion audit

This audit maps the requested features to implementation and evidence. Fixture tests establish command construction, parsing, safety and failure behavior; they do not establish that privileged changes work on a particular live server.

| Requirement | Implementation and evidence | Remaining evidence gap |
| --- | --- | --- |
| Go server with Svelte 5, TypeScript and Vite; small runtime | `go.mod`, `web/package.json`, embedded UI, no external Go modules. `make check` and `make build` pass. Built binary measured 7,983,369 bytes; JS is 141.53 kB raw / 48.62 kB gzip and CSS 38.20 kB / 8.40 kB gzip. | Network/TLS listener startup is blocked by the current sandbox's socket policy. |
| Responsive btop inspired monitoring | `internal/modules/monitor`, `web/src/pages/Overview.svelte`. Firefox rendered 1440×1000 and 390×844, checked horizontal overflow on all seven routes, and observed advancing production-handler sample timestamps. Screenshots are generated as ignored local artifacts; CI uploads them. | The browser forwards requests through an in-process production handler because real sockets are prohibited here. |
| Podman and Docker Compose | `internal/modules/compose` and Containers view. Fixtures cover both engines, project/env ordering, disabled markers, lifecycle and logs. Read-only discovery found 22 host Podman projects. | No host workload mutation was performed; Podman runtime setup and Docker daemon access are unavailable in this sandbox. |
| LXD, classic LXC and KVM | `internal/modules/machines` and Virtual machines view. Race-tested fixtures cover backend contexts, inventory, lifecycle, destructive guards and XML safety. | Host utility/daemon access is unavailable or blocked; no guests/domains were changed. |
| systemd services | `internal/modules/services` and Services view. Fixture tests cover both scopes, query/action construction, logs, parsing and errors. | System bus access is denied in the sandbox; no service was changed. |
| Samba/NFS shares | `internal/modules/shares` and File shares view. Tests cover atomic owned fragments, validation, rollback and unmanaged config preservation. `testparm` validates temporary candidate configurations. | No live reload, NFS export or client mount was performed. `systemd-analyze verify` could not run because its user-lookup socket setup is denied by the sandbox. |
| System package updates | `internal/modules/updates` and Updates view. Fixtures cover APT, DNF, Pacman and Zypper, cancellation and shared serialization; an APT simulation was read-only. | No metadata refresh, installation, distribution migration or reboot was performed. |
| MCP for agents, shared with web operations | Both transports derive from the same registry. `make mcp-test` passes against the official TypeScript SDK: 48 tools over stdio and the production Streamable HTTP handler, schemas, errors, denied writes and HTTP/API parity. | Live MCP HTTP over TCP/TLS and external OpenClaw/Hermes clients were not launched in this network-restricted environment. |
| Extensibility and KISS | `docs/extensions.md`; modules register typed tools in the shared registry. Node/Playwright and MCP SDK are development-only. | A separately maintained third-party module has not been built against a stable external module API. |
| Build/install and operations | `make install DESTDIR="$PWD/.cache/install-root"` staged the binary, example config and system/user units with expected modes. `bin/serverman -version` printed the source revision. Config example is covered by a Go test. | Service unit activation and live reverse-proxy TLS were not exercised; host service state was left untouched. |

## Idle measurement

`python3 tools/measure-idle.py bin/serverman 12` measured one stdio process waiting for input, with the real two-second Linux sampler active and no MCP requests. It used a temporary config/state directory and opened no listener. On this host the 12-second sample used 0.06 CPU seconds (0.50% of one core), 14,528 KiB RSS at the end (11,948 KiB at the start), 11 threads, and 6 file descriptors. This measures this namespace and process shape only; it is not a long-running HTTP daemon or large-host benchmark.

## Network and CI limits

The current environment rejects AF_INET socket creation and bind/listen. Firefox itself can render, so `tools/ui/verify.mjs` routes browser requests over pipes to the production `httpapi.Handler`; the production server executable is built, but this does not prove kernel TCP, a real reverse proxy, or TLS deployment. Chromium startup remains blocked here. The GitHub Actions workflow is committed but cannot be run from this checkout; its equivalent Go/frontend/MCP/browser commands were run locally, with Firefox available.
