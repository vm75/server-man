# server-man host setup record

Date: 2026-10-16. Host: `nuc`, Ubuntu 26.10, account `shasak` (UID 1000).
Checkout: `/home/shasak/ws/server-man`, real Git directory `.git`, starting commit
`a95fc91`. Existing untracked `HANDOFF.md` is preserved and excluded from commits.
The task attachment explicitly authorizes server preparation, dependency installation,
and creation of `~/vms`; this supersedes the development-only prohibitions in
AGENTS.md for this setup task. Verification of destructive module operations still
uses fixtures. No credential values belong in this record.

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
   provisioning. Final outcome will be recorded below. No `lxd init` was issued.
2. Planned prerequisite command (simulation: 39 new packages, zero upgrades or
   removals): `sudo apt-get install -y --no-install-recommends golang-go nodejs
   podman podman-compose uidmap slirp4netns`. Uses the existing signed native
   repository cache; no metadata refresh, signature bypass, distro upgrade or
   reboot. Package outcomes and full version inventory follow after execution.

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
   it uses `--ignore-scripts` and Go caches under `.cache`. Result pending.
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
  both return `[]`. No guest, profile, pool, bridge or daemon initialization was
  created by this task. The installation is retained as an available optional
  backend. This account's LXD client created its normal Snap config directory.
- Rootless Podman reports `true netavark`, existing subordinate UID/GID ranges
  `100000:65536` were reused unchanged. Containers and images both return `[]`.
  Podman units/socket/timer remain disabled at this point.
- A Python copy operation created `/home/shasak/vms` only after asserting the
  destination did not exist, with root mode 0700, owned by `shasak`. Copied
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
  allow the requested browser verification. Full versions and result follow.

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

### Current verification and configuration outcomes

- `make setup` succeeded. npm reported advisories; investigation found two high
  transitive frontend advisories and five MCP development dependency findings
  (including one critical). These affect development tooling; Node is not used
  by the installed Go daemon. Updates will be recorded as a separate step.
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

- Created `~/.config/serverman` (0700) and `config.json` (0600), both owned by
  `shasak`, refusing to replace an existing file. Config selects loopback
  `127.0.0.1:8080`, `/home/shasak/vms`, a two-second sampler and `allowWrites:false`.
  Default private token/state paths and system service inventory scope are kept.
  The HTTP server generates its own token on first start; it is never printed
  or copied into this record, command arguments or Git.
- The verified branding step is commit `296e14c`. No Go module, executable,
  config/state directory, systemd unit, browser event, cookie, tool/schema or
  share ownership marker was renamed.
- `npm --prefix web audit fix --ignore-scripts` updated two compatible transitive
  dependencies, with zero findings afterward. The development MCP SDK is being
  updated separately to the patched exact release `1.32.1`. No lifecycle scripts
  are enabled. Final lockfile/audit/verification results follow.

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
