# server-man host setup record

> **Anonymized historical record, not a machine-specific installation requirement.** Hostname, workload account, checkout directory, interface and LAN addresses are placeholders here. `WORKLOAD_USER` is a replaceable account name, `/path/to/server-man` is a replaceable checkout, and `192.0.2.10`/`192.0.2.0/24` are reserved documentation addresses that will not route to a real deployment. Substitute actual values before adapting any command; do not run this historical mutation ledger as an installer. See [guided installation](../installation.md) for current supported instructions.

Date: 2026-10-16. Host: `example-host`, Ubuntu 26.10, account `WORKLOAD_USER` (UID 1000).
Checkout: `/path/to/server-man`, real Git directory `.git`, starting commit
`a95fc91`. Existing untracked `HANDOFF.md` is preserved and excluded from commits.
The task attachment explicitly authorizes server preparation, dependency installation,
and creation of `~/vms`; this supersedes the development-only prohibitions in
AGENTS.md for this setup task. Verification of destructive module operations still
uses fixtures. No credential values belong in this record.

## Current access

Open **http://192.0.2.10:8080** from a machine on `192.0.2.0/24`, then
sign in with the existing private access token. The direct LAN HTTP follow-up
at the end records the current listener and supersedes the earlier HTTPS setup.

## Initial inventory

14 GiB RAM, about 97 GiB free on the root filesystem; `/dev/kvm` exists.
No `~/vms`, Go, Node, Docker, Podman, classic LXC utilities or virsh initially.
Existing npm 12.2.0 is managed by mise. Passwordless sudo works. The user systemd
manager is running. The system manager is degraded by pre-existing
`thermald.service`; it is left alone. Port 8080 was unused. No Git remote is set.
Existing externally bound port 22 is unchanged.

## Host mutation ledger

1. `timeout 12 lxc list local: --format json` invoked Ubuntu's `/usr/sbin/lxc`
   installer wrapper, unexpectedly triggering LXD Snap installation via the
   pre-existing `lxd-installer.socket`. The query timed out (124). The subsequent
   storage query also timed out. `snap changes` showed change 3 installing LXD
   from `6/stable/ubuntu-26.10`; `snap list` showed `lxd` 6.9-bf243da rev 40916
   and base core24 rev 2124. This was an inspection side effect, not guest
   provisioning. The installation completed; details are recorded below. No `lxd init` was issued.
2. Prerequisite command (simulation: 39 new packages, zero upgrades or
   removals): `sudo apt-get install -y --no-install-recommends golang-go nodejs
   podman podman-compose uidmap slirp4netns`. Uses the existing signed native
   repository cache; no metadata refresh, signature bypass, distro upgrade or
   reboot. Package outcomes and full version inventory are below.

The sandbox launcher fails before commands run (`bwrap` loopback RTM_NEWADDR:
operation not permitted). Commands are therefore run through the approved
outside-sandbox execution path. Failed sandbox attempts changed no files.

3. The prerequisite install succeeded (exit 0). No packages upgraded or removed;
   needrestart reported no services, containers, sessions or VMs needing restart.
   Go is 1.27.1, Node 24.19.0; existing mise npm 12.2.0 is retained. No global
   mise configuration or shell startup files changed. Full native package
   versions are recorded below.
4. `make setup` installs lockfile dependencies under `web/node_modules`,
   `tools/mcp/node_modules`, `tools/ui/node_modules` and project `.cache/npm`;
   it uses `--ignore-scripts` and Go caches under `.cache`. Setup succeeded.
   Build outputs live under ignored `bin/` and embedded frontend assets.
5. `podman info` / `podman ps` may initialize this account's empty rootless
   engine storage/runtime and network metadata. No pull/create/start was requested.
   Native LXD queries may create client config under `~/snap/lxd/common/config`.

### Native packages installed

```text
conmon 2.1.13+ds1-2
crun 1.21-1ubuntu3
golang-1.27-go 1.27.1-1
golang-1.27-src 1.27.1-1
golang-github-containers-common 0.67.1-3
golang-github-containers-image 5.39.2-3
golang-go:amd64 2:1.27~1
golang-src 2:1.27~1
libada-url0-3:amd64 3.4.4-1
libcares2:amd64 1.34.8-1
libgpgme45:amd64 2.1.2-1
libhdr-histogram6:amd64 0.11.8-4build1
libllhttp9.4:amd64 9.4.3+~cs12.11.9-2
libnode137:amd64 24.19.0+dfsg+~cs24.13.3-1ubuntu1
libsimdjson33:amd64 4.6.8-1
libsimdutf33:amd64 8.2.0-1
libslirp0:amd64 4.9.3-1ubuntu1
libsubid5:amd64 1:4.19.3-2ubuntu2
libuv1t64:amd64 1.52.1-4
libyajl2:amd64 2.1.0-7
netavark 1.17.2-2
node-acorn 8.17.0+ds+~cs28.27.25-1
node-balanced-match 2.0.0-1
node-brace-expansion 2.1.4+~1.1.2-1
node-corepack 0.35.0-2
node-debug 4.4.3+~4.1.13-1
node-llhttp 9.4.3+~cs12.11.9-2
node-lru-cache 11.2.7-7
node-minimatch 9.0.7-1
node-ms 2.1.3+~cs0.7.31-3
node-semver 7.7.4+~cs9.7.4-2
node-undici 8.9.0+dfsg+~cs3.2.0-1
node-xtend 4.0.2-3
nodejs 24.19.0+dfsg+~cs24.13.3-1ubuntu1
podman 5.8.6+ds1-2
podman-compose 1.6.0-1
python3-dotenv 1.2.1-2
slirp4netns 1.3.3-1
uidmap 1:4.19.3-2ubuntu2
```

### Provider and browser preparation

- LXD Snap change 3 completed. LXD 6.9-bf243da rev 40916 and core26 base rev 646
  were installed by the launcher. The daemon was started; activate service and
  daemon/user-daemon sockets are enabled. Native instance and storage inventories
  both return `[]`. LXD automatically created its database and empty default
  profile (zero instances); no guest, storage pool or managed bridge was created.
  No explicit `lxd init` was performed. The installation is retained as an available optional
  backend. This account's LXD client created its normal Snap config directory.
- Rootless Podman reports `true netavark`, existing subordinate UID/GID ranges
  `100000:65536` were reused unchanged. Containers and images both return `[]`.
  Podman units/socket/timer remain disabled at this point.
- A Python copy operation created `/home/WORKLOAD_USER/vms` only after asserting the
  destination did not exist, with root mode 0700, owned by `WORKLOAD_USER`. Copied
  `packaging/examples/vms` plus its README: three Compose definitions, two static
  pages, four harmless environment files and two disabled markers. Descendant
  directories are 0755 and files 0644 under the private root. No workload was
  pulled, created or started. Native provider validation attempts failed because
  Ubuntu podman-compose 1.6.0 rejects `--project-directory` and supports only one
  `--env-file`; these failed reads made no workload changes.
- `npm --prefix tools/ui exec -- playwright install firefox` downloaded Firefox
  build 1543 and FFmpeg build 1011 under `~/.cache/ms-playwright`. It reported
  missing native GTK/audio libraries. No browser service was installed.
- Next reviewed native install (simulation: 67 new packages, zero upgrades or
  removals): `sudo apt-get install -y --no-install-recommends docker-compose-v2
  libgtk-3-0t64 libasound2t64`. Compose v2 provides required project-directory,
  multiple env files and JSON status without a Docker daemon. GTK/audio dependencies
  allow the requested browser verification. Full versions and result are below.

### Compose v2 and browser packages installed

Install succeeded (exit 0); no upgrades, removals or service restarts.

```text
adwaita-icon-theme 51.0-1
at-spi2-common 2.62.0-1
bubblewrap 0.12.0-1
dconf-gsettings-backend:amd64 0.49.0-4
dconf-service 0.49.0-4
docker-compose-v2 2.40.3+ds2-0ubuntu2
fontconfig 2.17.1-5ubuntu1
fontconfig-config 2.17.1-5ubuntu1
fonts-dejavu-core 2.37-9
fonts-dejavu-mono 2.37-9
glycin-loaders 2.2.1+ds-1ubuntu2
glycin-thumbnailers 2.2.1+ds-1ubuntu2
gtk-update-icon-cache 4.24.1+ds-1
hicolor-icon-theme 0.18-2build1
libaom3:amd64 3.14.1-1
libasound2-data 1.2.16.1-1
libasound2t64:amd64 1.2.16.1-1
libatk-bridge2.0-0t64:amd64 2.62.0-1
libatk1.0-0t64:amd64 2.62.0-1
libatspi2.0-0t64:amd64 2.62.0-1
libavahi-client3:amd64 0.8-18ubuntu1.1
libavahi-common-data:amd64 0.8-18ubuntu1.1
libavahi-common3:amd64 0.8-18ubuntu1.1
libcairo-gobject2:amd64 1.18.4-3
libcairo2:amd64 1.18.4-3
libcolord2:amd64 1.4.8-3
libcups2t64:amd64 2.4.16-1ubuntu3
libdatrie1:amd64 0.2.14-2
libdconf1:amd64 0.49.0-4
libepoxy0:amd64 1.5.10-2build1
libfontconfig1:amd64 2.17.1-5ubuntu1
libfribidi0:amd64 1.0.16-5
libgdk-pixbuf-2.0-0:amd64 2.44.8+dfsg-1
libgdk-pixbuf2.0-common 2.44.8+dfsg-1
libglycin-2-0:amd64 2.2.1+ds-1ubuntu2
libgraphite2-3:amd64 1.3.15-2
libgtk-3-0t64:amd64 3.24.52-0ubuntu1
libgtk-3-common 3.24.52-0ubuntu1
libharfbuzz0b:amd64 12.3.2-2
libheif-plugin-aomdec:amd64 1.23.4-1
libheif1:amd64 1.23.4-1
libhwy1t64:amd64 1.3.0-5.1ubuntu1
libjxl0.11:amd64 0.11.2-5.1ubuntu1
liblcms2-2:amd64 2.19.1-1
libpango-1.0-0:amd64 1.58.0-1
libpangocairo-1.0-0:amd64 1.58.0-1
libpangoft2-1.0-0:amd64 1.58.0-1
libpixman-1-0:amd64 0.46.4-1
librsvg2-2:amd64 2.62.1+dfsg-1ubuntu1
libsharpyuv0:amd64 1.6.0-0.1
libthai-data 0.1.30-3
libthai0:amd64 0.1.30-3
libwayland-client0:amd64 1.26.0-1
libwayland-cursor0:amd64 1.26.0-1
libwayland-egl1:amd64 1.26.0-1
libxcb-render0:amd64 1.17.0-2ubuntu1
libxcb-shm0:amd64 1.17.0-2ubuntu1
libxcomposite1:amd64 1:0.4.6-1build1
libxcursor1:amd64 1:1.2.3-1build1
libxdamage1:amd64 1:1.1.7-1
libxfixes3:amd64 1:6.0.0-2build2
libxi6:amd64 2:1.8.2-2
libxinerama1:amd64 2:1.1.4-3build2
libxkbcommon0:amd64 1.13.1-1
libxrandr2:amd64 2:1.5.4-1build1
libxrender1:amd64 1:0.9.12-1build1
libxres1:amd64 2:1.2.1-2
```

### Additional Firefox runtime package

```text
libx11-xcb1:amd64 2:1.8.13-1
```

### Current verification and configuration outcomes

- `make setup` succeeded. npm reported advisories; investigation found two high
  transitive frontend advisories and five MCP development dependency findings
  (including one critical). These affect development tooling; Node is not used
  by the installed Go daemon. Updates were verified and committed separately below.
- Initial `make check test mcp-test` stopped at the embedded UI test because
  copied ignored assets still contained the old branding. `make build check
  test mcp-test` then passed: no frontend diagnostics, all Go race tests, and
  48 tools with official SDK parity on stdio and the HTTP handler. No generated
  assets were edited by hand.
- The first browser run stopped before launch with missing `libx11-xcb1`.
  Reviewed simulation: one new package, no upgrades/removals. Ran
  `sudo apt-get install -y --no-install-recommends libx11-xcb1`; installed version
  `2:1.8.13-1`. No service restart was required.
- Created `~/.config/containers` (0700) and `containers.conf` (0600) using
  exclusive creation. Its only setting is `[engine] compose_providers =
  ["/usr/libexec/docker/cli-plugins/docker-compose"]`. No prior config existed.
- `systemctl --user enable --now podman.socket` created the normal user
  `sockets.target.wants` symlink and rootless Unix socket. Compose status queries
  activate the user Podman API service on demand; no TCP Podman API was enabled.
  The pre-existing `/run/podman/nv-proxy.sock` was left untouched.
- All three examples pass Compose v2 `config --quiet` using the app's ordered
  environment arguments. Podman `compose ... ps --all --format json` succeeds
  for the status example. Native images and containers remain empty. Docker
  syntax is validated with Compose v2; no Docker engine or daemon is installed.

### Deployment preparation

- Created `~/.config/server-man` (0700) and `config.json` (0600), both owned by
  `WORKLOAD_USER`, refusing to replace an existing file. Config selects loopback
  `127.0.0.1:8080`, `/home/WORKLOAD_USER/vms`, a two-second sampler and `allowWrites:false`.
  Default private token/state paths and system service inventory scope are kept.
  The HTTP server generates its own token on first start; it is never printed
  or copied into this record, command arguments or Git.
- The verified branding step is commit `296e14c`. No Go module, executable,
  config/state directory, systemd unit, browser event, cookie, tool/schema or
  share ownership marker was renamed.
- `npm --prefix web audit fix --ignore-scripts` updated two compatible transitive
  dependencies, with zero findings afterward. The development MCP SDK was
  updated separately to the patched exact release `1.32.1`. No lifecycle scripts
  were enabled. Final lockfile/audit/verification results are below.

- Dependency security updates are commit `c1d2918`: SDK 1.32.1, devalue 5.9.4,
  source-map-js 1.2.2, fast-uri 3.1.8, hono 4.13.13, ip-address 10.7.4 and
  proxy-addr 2.0.8. Commands: `npm --prefix tools/mcp install --save-dev
  --save-exact @modelcontextprotocol/sdk@1.32.1 --ignore-scripts`, followed by
  `npm --prefix tools/mcp audit fix --ignore-scripts`. All three package trees
  now report zero audit findings. `make check mcp-test ui-test` passes with the
  final lockfiles, including Firefox 1440×1000, 390×844 and 320×740. Management
  browser flows remain fixtures with writes rejected by the production handler.
- `make install DESTDIR="$PWD/.cache/install-root"` produced a 0755 executable
  and 0644 config example/system/user unit files. It did not change host service
  state. The staged executable ran `-version` successfully.
- Example source files are retained in `packaging/examples` for review/reuse.
  Image existence/pulls and live workload lifecycle have not been verified;
  all examples remain definitions only. Two `.disabled` markers are present.

### Headless service installation

This machine is headless. No desktop session, display manager or graphical login
is needed. Firefox/GTK/X11 libraries above support headless verification only;
Node, Firefox and these libraries are not application runtime dependencies.
The daemon is a native Go process. Browser access is through loopback/SSH.

Pre-install checks found no existing server-man unit/binary/config and no listener
on 8080. `make build` from clean revision `e4424dd` produced `bin/server-man`
reporting `e4424dd`, SHA-256
`26cc7c4632ce0b9180a36627fc86a1e26e2f9a41c5e0f2ae34e79faea49c1fb5`.

Commands applied successfully:

```sh
sudo make install
systemctl --user daemon-reload
systemd-analyze verify /usr/local/lib/systemd/system/server-man.service
systemd-analyze --user verify /usr/local/lib/systemd/user/server-man.service
sudo loginctl enable-linger WORKLOAD_USER
systemctl --user enable --now server-man.service
```

Installation writes `/usr/local/bin/server-man` (0755),
`/usr/local/share/server-man/config.example.json` (0644), and system/user unit files
under `/usr/local/lib/systemd` (0644), owned by root. Only the **user** server-man
unit is enabled and started, under the existing `WORKLOAD_USER` account; no account or
group is created. User lingering changes from no to yes so the manager survives
logout and starts at boot on this headless host. The system/root unit stays
inactive/disabled. No TLS proxy, firewall, public listener, host hostname or
host network changes were applied. Final service and verification results are below.


### Deployment outcome and live evidence

- Install/start commands all succeeded. Both unit files pass `systemd-analyze`
  verification. The system checker also emitted pre-existing XFS units' removed
  `CPUAccounting` option warnings; no server-man unit error occurred. These
  unrelated vendor unit files were left unchanged.
- User `server-man.service` is enabled and active, running the installed revision
  `e4424dd`, with zero restarts. System `server-man.service` is disabled/inactive.
  Installed and built binary hashes match. User `podman.socket` is enabled/active
  at `/run/user/1000/podman/podman.sock`; no root Podman service was enabled.
  `loginctl show-user WORKLOAD_USER -p Linger` reports `yes`.
- First app start created `~/.config/server-man/token` (0600),
  `~/.local/state/server-man` (0700), and its `audit.jsonl` (0600), owned by
  `WORKLOAD_USER`. Config and token directory are 0700. Podman's account storage is
  `~/.local/share/containers/storage` (0700). These contain generated private
  runtime state and are excluded from Git and this document's content.
- `ss -ltn '( sport = :8080 )'` shows only `127.0.0.1:8080`. `/healthz` returns
  200. Authenticated status reports revision `e4424dd` and `allowWrites:false`.
  LXD's `core.https_address` is empty. Its only listed networks are existing,
  unmanaged physical interfaces; its default profile has zero instances.
- `node .cache/live-verification.mjs` uses the token file internally without
  printing it. Real TCP HTTP checks pass health, unauthenticated API/MCP 401,
  authenticated foreign-origin 403, 48 tool schemas, all three example projects,
  exact global/project env filename ordering and disabled flags. Both Podman
  projects return `not_created` and empty containers from the actual provider.
  Compose Up is rejected with `writes_disabled` and no job is created.
- Native application inventories succeed for empty LXD and 296 system services
  with no service warnings. APT 3.3.3 cached status is valid with zero candidates;
  it was not refreshed. Classic LXC, libvirt, Samba and NFS report absent tools.
- The official SDK connects to the real `/mcp` endpoint over TCP, checks
  `server-man` identity, all 48 schemas against HTTP discovery, native project
  discovery, live monitoring, denied writes and ping. Headless Firefox connects
  directly over TCP at 1440×1000 and 390×844, signs in/out, sees both provider
  tabs and all three real examples, and passes branding/overflow/error checks.
  Screenshots and a credential-free summary are under ignored `.cache/`.
- The first local probe incorrectly expected HTTP jobs to use MCP's `items`
  wrapper; another browser assertion looked for Docker content in the Podman
  tab. Only the scratch verifier was corrected; the final run passes all checks.
  These failed read probes did not launch jobs or workloads.
- Final native `podman ps --all --format json` and `podman images --format json`
  both return `[]`. No guest/container/image/persistent workload data was
  created, downloaded, started, stopped or removed. No existing workloads,
  shares, host package updates, host network/firewall or existing failed services
  were changed. No accounts/groups were created; existing subordinate IDs were
  reused. No host changes were reverted.
- Native totals: 107 newly installed APT packages, zero upgraded/removed, plus
  LXD and its core26 Snap prerequisite. Package-owned configuration, binaries,
  caches, triggers and font/icon caches were created by their installers. Existing
  core24 and other Snaps were retained. No APT metadata refresh was requested.
  Root `thermald.service` remains in its pre-existing failed state.
- Repository/build mutations: the four commits for branding, dependency patches,
  examples/preparation and deployment evidence; `make setup/build/check/test/
  mcp-test/ui-test`, staging installation, Go/npm caches and ignored generated
  assets/artifacts. Browser fixtures use temporary state and clean it up.
  Live probes create temporary in-memory browser sessions; successful checks
  log out. Existing `HANDOFF.md` remains untracked and unchanged.
- Local logs: `/tmp/server-man-*.log` and audit summaries
  `/tmp/server-man-*-audit*.json`; no credential values or resolved Compose
  configuration were written there. The copied `~/vms/README.md` was refreshed
  from the reviewed example README after documenting the compatible provider.

## Operating this headless host

```sh
systemctl --user status server-man.service
journalctl --user -u server-man.service
systemctl --user restart server-man.service
```

From another computer on the LAN, open `https://192.0.2.10` after trusting
the exported public CA. Read the token directly from the private
`~/.config/server-man/token` file in your SSH session; never put it in a URL or
commit it. No SSH tunnel or desktop session is needed. See the direct LAN
follow-up below for certificate transfer and configuration details.

The default deployment is read-only. To manage workloads, deliberately change
`allowWrites` in `~/.config/server-man/config.json` and restart the user service;
this enables the shared management API under this account's native permissions.
Remove only the chosen example's `.disabled` marker when ready to use it.
**Up** creates a workload; **Start** requires an existing container. Both status
examples publish loopback-only ports; heartbeat has no networking. Images have
not been pulled. Review definitions and provider access before enabling writes.

To undo this deployment without deleting data: disable/stop the user server-man
unit and Podman socket, stop the socket-activated user Podman API service, and
remove the installed files/config only after checking for subsequent use. Disable
user lingering only if no other user services need it. Preserve tokens/state and
`~/vms` unless deliberately retiring them. No undo command was executed here.
Optional package/Snap removal requires separate impact review; their state may
be used by later work.

Remaining integration limits: no real workload lifecycle, Docker daemon,
classic LXC/libvirt guests, share reload/mount, package-update job, reboot,
external TLS proxy, reboot persistence or hosted CI was exercised. These are
recorded limits of verification; the headless loopback deployment and examples
are ready. The installed binary reports `4191209-dirty`; later commits update
deployment and verification documentation only.

## Direct LAN access follow-up (2026-10-16)

Historical record: this HTTPS configuration was subsequently retired by the
user's request; the current plain HTTP arrangement is recorded below.

The user explicitly requested access from another machine. This supersedes the
loopback-only access arrangement above. Inspection found LAN address
`192.0.2.10/24` on `LAN_INTERFACE`, no proxy installed, ports 80/443 unused, and no
ufw/nft/iptables utility installed. No firewall, router, DNS or hostname changes
are planned. The backend stays on `127.0.0.1:8080`, with token auth and writes
disabled. A Caddy proxy will bind only `192.0.2.10:443` and accept requests
only from `192.0.2.0/24`. No plaintext listener or HTTP/3 UDP port is enabled.

Reviewed simulation: `sudo apt-get install -y --no-install-recommends caddy` adds
only caddy 2.11.4-1, libnspr4 2:4.39-1ubuntu1, libnss3 2:3.127-1 and
libnss3-tools 2:3.127-1, with zero upgrades/removals. The package was downloaded
with `apt-get download caddy` under ignored `.cache/caddy-package` and its service
and install scripts inspected. Its default config would start a wildcard HTTP
server, so the reviewed `packaging/Caddyfile.lan.example` is installed first and
`--force-confold` preserves it during package installation. Caddy's package
creates a system caddy account/group, adds it to www-data and creates
`/var/lib/caddy` and `/var/log/caddy`. Its system unit is enabled and started.

The proxy uses Caddy's local CA (`tls internal`) and disables automatic trust
installation. Client machines must trust the exported public root certificate;
private CA/leaf keys remain in Caddy-owned state and are never copied to the repo
or logs. Access logs are not enabled. Caddy's normal admin endpoint stays
loopback-only. Its certificate and configuration lifecycle follows the native
service; no desktop environment is required.

Commands applied and exact outcomes follow below.


### LAN setup commands and outcomes

All of the following completed successfully:

```sh
dpkg-deb --extract .cache/caddy-package/caddy_*.deb .cache/caddy-package/root
.cache/caddy-package/root/usr/bin/caddy adapt --config packaging/Caddyfile.lan.example --adapter caddyfile
sudo install -d -m 0755 /etc/caddy
sudo install -m 0644 packaging/Caddyfile.lan.example /etc/caddy/Caddyfile
sudo apt-get -o Dpkg::Options::=--force-confold install -y --no-install-recommends caddy
systemctl --user restart server-man.service
sudo install -o WORKLOAD_USER -g WORKLOAD_USER -m 0644 /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt /home/WORKLOAD_USER/.config/server-man/lan-ca.crt
sudo -u caddy env HOME=/var/lib/caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
```

The proxy package and adapter output are cached under ignored `.cache`; install
and validation logs are `/tmp/server-man-caddy-*.log`. Before restarting the app,
a read-only authenticated check confirmed no management jobs existed. A Python
file operation saved its prior config as `~/.config/server-man/config.before-lan.json`
(0600) using exclusive creation, then atomically added
`publicOrigin: "https://192.0.2.10"` to `config.json` (0600).
The token and write policy were preserved. Restart expires prior browser sessions.

Newly installed packages (four; zero upgrades/removals; cumulative APT total 111):

```text
caddy 2.11.4-1
libnspr4:amd64 2:4.39-1ubuntu1
libnss3:amd64 2:3.127-1
libnss3-tools 2:3.127-1
```

Caddy's package created account `caddy` (UID 980), group `caddy` (GID 982), and
membership in existing `www-data`. `/etc/caddy` is root:root 0755, its reviewed
Caddyfile 0644; the unused vendor example is retained as `Caddyfile.dpkg-dist`.
`/var/lib/caddy` is caddy:caddy 0750, `/var/log/caddy` caddy:caddy 0755.
Its normal private CA/leaf keys and certs are under
`/var/lib/caddy/.local/share/caddy` (root key 0600, caddy:caddy). Caddy's runtime
state/autosaved config is under its own home. The public CA was exported 0644,
WORKLOAD_USER:WORKLOAD_USER, inside the existing private `~/.config/server-man` directory.
No private key was exported; automatic machine trust installation was disabled.

The native Caddy unit is enabled/active with zero restarts. Its `caddy-api` unit
remains disabled/inactive. Package installation required no other service restart.
The app was deliberately restarted once to load the origin configuration. Final
listeners are LAN `192.0.2.10:443`, local backend `127.0.0.1:8080`, and Caddy's
local admin `127.0.0.1:2019`. There is no port 80 or UDP 443 listener. The app
remains write-disabled, and its token permissions remain unchanged.

### LAN verification

- Native `caddy adapt` and `caddy validate` accept the deployed config.
- `curl --cacert ~/.config/server-man/lan-ca.crt https://192.0.2.10/healthz`
  verifies the certificate chain and IP identity and returns 200/ok. A connection
  bound to source `127.0.0.1` receives 403 from the proxy's LAN source restriction.
- `NODE_EXTRA_CA_CERTS=/home/WORKLOAD_USER/.config/server-man/lan-ca.crt node
  .cache/lan-verification.mjs` passes with certificate verification enabled:
  health, unauthenticated API/MCP 401, authenticated foreign-origin 403,
  authenticated API discovery, secure/HttpOnly/SameSite Strict cookie login and
  logout, denied Compose writes, 48 official SDK schemas, native three-project
  discovery and MCP ping over HTTPS.
- Headless Firefox imports the public CA into an isolated temporary NSS profile
  using `certutil -N` and `certutil -A`. It loads the LAN HTTPS URL **without**
  bypassing certificate checks, signs in, sees advancing live monitoring and the
  real Podman examples, verifies no page errors/overflow, and signs out. The
  temporary profile is removed. Screenshot: ignored `.cache/lan-containers.png`.
- These checks connected from this server using its LAN address. Another physical
  client has not been tested; its browser trust store and network reachability
  are client-side prerequisites. No host firewall, router forwarding or DNS was
  changed. No guest/container or application management job was launched.

### Historical certificate setup (retired)

Copy the **public certificate** over your authenticated SSH connection:

```sh
scp WORKLOAD_USER@192.0.2.10:.config/server-man/lan-ca.crt ./server-man-lan-ca.crt
openssl x509 -in server-man-lan-ca.crt -noout -fingerprint -sha256
```

Expected SHA-256 fingerprint:
`5F:96:F8:CE:82:75:80:32:FB:C1:8E:16:0E:B1:20:EF:0B:F0:A5:12:2B:EE:49:36:D1:18:F1:0D:AB:84:93:75`.
Import that certificate as a trusted certificate authority in your client OS or
browser, then open **https://192.0.2.10** and sign in with the existing access
token. [Caddy's local HTTPS documentation](https://caddyserver.com/docs/automatic-https#local-https)
explains client trust for its internal CA. An HTTP URL is not served.

The current address comes from DHCP. If it changes, update the proxy bind/site
and the app's publicOrigin together, validate, then restart both services. A DHCP
reservation can keep this address stable; none was changed by this task.

At the time of this HTTPS setup, this was the documented rollback procedure.
It was superseded by the direct HTTP follow-up below; the Caddy service, package,
CA state, exported certificate and config backup have since been removed.

## Direct LAN HTTP follow-up (2026-10-16)

The user requested plain HTTP directly from the application, bound to
`0.0.0.0`. The live config now sets `listen` to `0.0.0.0:8080` and
`publicOrigin` to `http://192.0.2.10:8080`; `allowWrites:false` and the
existing token file are preserved. Config remains mode 0600 in the private
server-man config directory. The application validates this explicit remote
listener against a non-loopback IP origin and matching port, and uses
non-Secure session cookies for HTTP.

Caddy was stopped and disabled before restarting the app. Direct checks then
confirmed the app owns wildcard TCP 8080, health returns 200 through both
loopback and the LAN address, unauthenticated API access returns 401, a foreign
Origin returns 403, login/logout work with HttpOnly and SameSite Strict cookies
without Secure, and the jobs tool reports no active jobs. There is no listener
on TCP 80 or 443. The Caddy package and its three dependencies
(`libnspr4`, `libnss3`, `libnss3-tools`) were purged, removing exactly four
packages and freeing 55.0 MB, with zero upgrades or installs. Its system
service, account, configuration and CA state were removed. The exported public
CA and the private pre-HTTPS config backup created during the previous follow-up
were deleted. No firewall, router, DHCP, or DNS settings changed. Other physical
clients have not been tested; this HTTP deployment is intended for a trusted
network only.

The app was rebuilt and installed at `/usr/local/bin/server-man`; the running
user service uses the new binary. HTTP health, authentication, cookie behavior,
Host and Origin rejection, API jobs state, unauthenticated MCP denial, and an
authenticated official SDK MCP session (48 tools and ping) were checked after
the change. Go test suites were not rerun.

## Compose authoring deployment follow-up (2026-10-16)

The requested project editor was built from commit `d016f2c` and installed at
`/usr/local/bin/server-man` (SHA-256
`9e06ec36c2275091bab965a3e42a77f34306875fa76779e6b789913357db033d`). The
existing user service was restarted and remains active. Its configuration was
left untouched; at deployment it had `allowWrites:true`,
`listen: "0.0.0.0:8080"`, and
`publicOrigin: "http://192.0.2.10:8080"`. The token file was read only for an authenticated
MCP discovery check and was not displayed or changed.

The installed executable reports version `d016f2c`; its hash matches the
workspace build. Local HTTP health returns 200, and an authenticated official
SDK session to the configured LAN origin lists 54 tools, including project
create, Compose read/save, env read/save, and data-directory creation. No
project files, containers, firewall rules, or network settings were changed.
Plain HTTP carries env text and is intended for the trusted LAN only.
