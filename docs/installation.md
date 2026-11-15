# Install and operate server-man

## Guided installation

There are two supported entry points, using the same installer. Both require an existing non-root workload account, systemd and configured native repositories on Ubuntu, Debian or Arch Linux. Build tools are needed only for source installation.

From source, as your normal account (Linux, Go 1.26+, Node.js 22.12+, npm and Make):

```sh
./install.sh --user YOUR_ACCOUNT --backends all
```

The script builds without root, then invokes sudo for host setup. To inspect without changing the host, add `--dry-run` (the source build still runs). Build prerequisites must already be installed; this does not download or replace Go/Node toolchains.

From a release, verify its checksum and extract the matching Linux architecture archive:

```sh
sha256sum -c server-man-VERSION-linux-ARCH.tar.gz.sha256
tar -xzf server-man-VERSION-linux-ARCH.tar.gz
cd server-man-VERSION-linux-ARCH
./install.sh --user YOUR_ACCOUNT --backends all --dry-run
./install.sh --user YOUR_ACCOUNT --backends all
```

The release includes static `server-man`, `server-man-admin` and `server-man-install` binaries plus `install.sh`; no Go, Node, npm or Make is needed. `make release` produces the native-architecture archive and checksum in `dist/`; it does not publish a release. Verify the checksum against a trusted distribution source; a checksum alone does not authenticate a publisher.

Choose `--backends podman,docker,virtualization,incus,samba,nfs` or a subset. `--skip-packages` skips repository refresh/install; required backend groups must already exist. `--yes` explicitly accepts the displayed plan without prompting. Without it, the tool asks before mutations. The plan includes packages, group grants, helper sudo policy, service and lingering. Installation may start native daemons. Arch package installation performs a full system upgrade to avoid partial upgrades. No repositories are added and package signature checks remain enabled. Missing repository packages fail explicitly.

The installer installs root-owned executables, validates the exact helper rule, adds selected Docker/Incus/libvirt groups, creates initial private unified YAML configuration and enables lingering. Existing configuration is preserved and validated, including listen address/write policy. A new configuration enables writes and privileged operations on loopback. Selected Docker, libvirt and Incus daemon/socket units are enabled; Samba/NFS exposure remains explicit. The helper and Docker/Incus groups provide broad host administration privileges.

The installed **system service runs as the selected workload account**, with its home and user bus, so rootless Podman still belongs to that account. Systemd obtains current supplementary groups at service start without restarting the account's desktop session. The installer stops/disables an existing server-man user service to avoid duplicate listeners. It never restarts the user manager or logs you out.

```sh
sudo systemctl status server-man.service
sudo journalctl -u server-man.service
```

Open `http://127.0.0.1:8080` for a new configuration and sign in using the private `~/.config/server-man/token`. The unified YAML defaults to `~/.config/server-man/server-man.yaml`; its `server` section configures the application and its other sections define managed resources. There is no separate bootstrap JSON file. No workloads, VM pools/networks, images or shares are created. Initialize Incus storage/network explicitly, then use the UI to review readiness and define resources. No reboot is requested. Rerunning preserves configuration and updates the application/service policy; a failed step reports an error without claiming rollback of completed package or account changes.

## Manual installation and packaging


server-man is a native Linux binary. Node is needed only to build or run the verification tools. Build with `make setup build`, then install the built binary and examples with `sudo make install`. Installation copies files beneath `/usr/local` (or `PREFIX`) and honors `DESTDIR`; it does not enable or start a service, create accounts, or replace an active configuration. For packaging checks, use a temporary staging root such as `make install DESTDIR="$PWD/.cache/install-root"`.

Copy `/usr/local/share/server-man/server-man.example.yaml` (or `packaging/server-man.example.yaml` from the source tree) to `/etc/server-man/server-man.yaml`, then set ownership to root and mode 0600. This same YAML contains `server` settings and all managed resource sections. Select its absolute path with `-config`; relative workload paths resolve against its directory. Create the token file as a random private secret, for example:

```sh
sudo install -d -m 0700 /etc/server-man
sudo sh -c 'umask 077; openssl rand -base64 32 > /etc/server-man/token'
sudo chmod 0600 /etc/server-man/token
```

## Run as your workload account

For manual installation, run the user unit as account X so rootless containers and user
services belong to that account. Install the application and helper once using
`sudo make install`. Both executables and their parent directories must be
root-owned and not writable by X. The helper is an ordinary executable, not a
setuid binary; sudo supplies elevation for explicit operations.

Create X's private `~/.config/server-man/server-man.yaml`:

```yaml
version: 1
server:
  allow_writes: true
  privileged_operations: true
```

Use `sudo visudo -f /etc/sudoers.d/server-man` to install this rule after replacing
X with the actual account name:

```sudoers
X ALL=(root) NOPASSWD: NOSETENV: /usr/local/libexec/server-man-admin ""
```

Validate with `sudo visudo -c`. The example is also installed under
`/usr/local/share/server-man/sudoers.example`; installation never activates it.
The helper accepts no command arguments. It validates bounded structured requests
on stdin, fixes native paths and executable discovery, and serializes operations
with a root-owned lock. It starts no listener and exits after each operation.

As X, enable the user service with
`systemctl --user enable --now server-man.service`. For operation without a login,
an administrator can enable lingering with `sudo loginctl enable-linger X`.
The user service uses X's normal runtime directory/session bus. The Services page
selects system or current-user units; HTTP/MCP service tools accept an optional
`scope` of `system` or `user`, defaulting to `server.systemd_scope`.

Keep `server.privileged_operations` false for a read-only/unprivileged deployment.
`server.allow_writes` remains the shared HTTP/MCP write gate. When privilege routing is
enabled, package and share operations and system service operations use the
helper; user services run directly as X. Privileged operations use automatic
native package-manager detection, `/etc/samba/smb.conf`, the owned Samba fragment
`/etc/samba/server-man.conf`, and the compatible NFS fragment
`/etc/exports.d/server-man.exports`. Conflicting app overrides are rejected.
Root-owned helper state is separate at `/var/lib/server-man-admin`.

The Updates page also offers module dependency installation for Podman/Compose,
Docker/Compose, Samba, NFS, x86 QEMU/libvirt and Incus (APT/pacman). These are native
repository packages; missing repository packages fail explicitly. Restart the
app after installing a provider to refresh discovery. Dependency installation
does not change group memberships or container ownership. Use the separate share
setup action to configure and enable Samba/NFS. Incus still requires explicit daemon initialization with the configured project, pool and network.

The helper requires the default `/usr/local` installation prefix. For custom
prefixes, update the compiled helper path and sudoers rule together. Do not enable
`NoNewPrivileges` or service sandbox options that imply it on this sudo-based
user service: that prevents elevation. Retain loopback/TLS and authentication
protections. X is trusted to administer the allowed capabilities, including
package scripts that run as root; the helper does not make package administration
low privilege.

The system unit remains available for deployments deliberately running
server-man as root. Root does not manage X's rootless Podman inventory or X's
user manager. Do not enable both deployments on the same listener.

## Product naming

Use `server-man`, `server-man.service`, the `server-man` configuration/state directories and `SERVER_MAN_` environment variables. No legacy-name aliases or migration layer are provided.

## TLS reverse proxy

Keep server-man on loopback and terminate TLS at a trusted reverse proxy. Example Caddy site:

```caddyfile
server.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Set `server.public_origin` to the exact browser origin, for example `https://server.example.com`, in the unified YAML, then restart server-man. Server settings are loaded at startup; resource changes are applied through the management workflow. This enables Secure session cookies and origin validation. Keep the proxy's upstream on loopback, restrict who can reach the public site, and configure proxy logs to omit `Authorization`, `Cookie`, and request bodies. Do not expose the unauthenticated management socket publicly. MCP HTTP uses the same authenticated endpoint; remote agents should use HTTPS and a bearer token. See [MCP configuration](design/mcp.md).

The example system unit expects `/usr/local/bin/server-man`. If installing under another prefix, adjust `ExecStart` before enabling it. Back up configuration and state before upgrading; package updates and service management remain explicit operations initiated through server-man.


## Direct LAN HTTP

For a headless host that should serve HTTP directly, set `server.listen` to
`0.0.0.0:8080` and `server.public_origin` to the host's non-loopback IP and same port,
for example `http://192.168.192.78:8080`. This explicit configuration enables
IPv4 remote access and restricts accepted Host and Origin values to that
address. The service does not configure firewall or router rules; permit port
8080 only on a trusted network. Browser session cookies are HttpOnly and
SameSite Strict, but are not Secure over HTTP. Management requests and browser session credentials travel over HTTP. For untrusted networks, use the TLS reverse proxy
setup above instead.

For historical host configuration and verification, see the
[access and verification record](operations/host-setup.md#direct-lan-http-follow-up-2026-10-16).

## First Ubuntu deployment

See the [current deployment record](operations/ubuntu-first-deployment.md) for application access, installed dependencies and remaining provider setup. Debian and Arch Linux remain support targets without native acceptance evidence.
