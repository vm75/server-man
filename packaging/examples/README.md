# Disposable Compose examples

Copy the contents of `vms/` into an **absent or empty** `~/vms` after inspecting
these definitions. Do not overwrite existing workloads or environment files.
These examples contain no credentials and only mount their own static site.
No service starts automatically: `restart: "no"`, no systemd units, no boot hooks.

| Provider / project | Purpose | Default gate |
| --- | --- | --- |
| Podman / `demo-status` | Tiny static status page at `http://127.0.0.1:18080` | Ready to create explicitly |
| Podman / `demo-heartbeat` | UTC timestamps every 30 seconds; demonstrates logs and sorted env overrides | `data/demo-heartbeat/.disabled` |
| Docker / `demo-status-docker` | Equivalent static page at `http://127.0.0.1:18081` | `data/demo-status-docker/.disabled`; requires Docker |

Both status services run as UID/GID 65534, drop capabilities, disallow privilege
gain, use a read-only root, bind only loopback, and have CPU/memory/PID limits.
The heartbeat has no network or mounts. Images use an explicit public BusyBox
version. Review image provenance and updates as part of normal administration.
No images are pulled when these files are copied or discovered.

Environment arguments follow the app: `data/data_root.env`, `data/global.env`,
then sorted project `*.env` files. The global files contain only sample defaults
and comments. The heartbeat label demonstrates `10-defaults.env` followed by
`20-local.env`. A disabled marker prevents up/start/restart/pull in server-man;
stop and down remain available. Native provider commands bypass this app gate.

For rootless Podman, install Podman, a compatible Compose v2 provider, uidmap and a supported
rootless networking helper under the owning account. Ubuntu podman-compose 1.6.0
lacks the required project-directory and repeated env-file options; see
[provider requirements](../../docs/design/compose.md). Standalone Docker Compose
v2 can use the local rootless Podman socket without a Docker daemon. Check `podman info` and
`podman compose version` before enabling application writes. Server-man delegates
Compose parsing and status to the provider. Docker is an independent optional
backend; these examples do not install or start a Docker daemon.

With the application configured to use this `vmRoot`, inspect **Containers**.
Writes remain a separate `allowWrites` setting, disabled in the deployment here.
To try a workload, review the configuration, enable writes deliberately, then
use **Up** and confirm. **Start** requires a previously created container. Stop
with **Stop**, or remove its containers/networks with **Down**. No named volumes
or persistent guest data are used. Removing these example files does not stop
containers; stop/down first if you previously launched them.

LXD, classic LXC and libvirt use native inventories. This directory introduces
no guest XML, hypervisor layout, guest downloads, bridges, or storage pools.
