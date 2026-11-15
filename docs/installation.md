# Install and operate server-man

server-man is a native Linux binary. Node is needed only to build or run the verification tools. Build with `make setup build`, then install the built binary and examples with `sudo make install`. Installation copies files beneath `/usr/local` (or `PREFIX`) and honors `DESTDIR`; it does not enable or start a service, create accounts, or replace an active configuration. For packaging checks, use a temporary staging root such as `make install DESTDIR="$PWD/.cache/install-root"`.

Copy `/usr/local/share/server-man/config.example.json` (or `packaging/server-man.example.json` from the source tree) to `/etc/server-man/config.json`, then set ownership to root and mode 0600. Set `manifestPath` to the absolute location of your authoritative workload YAML. Relative workload paths resolve against its directory. Create the token file as a random private secret, for example:

```sh
sudo install -d -m 0700 /etc/server-man
sudo sh -c 'umask 077; openssl rand -base64 32 > /etc/server-man/token'
sudo chmod 0600 /etc/server-man/token
```

## Run as your workload account

Recommended: run the user unit as account X so rootless containers and user
services belong to that account. Install the application and helper once using
`sudo make install`. Both executables and their parent directories must be
root-owned and not writable by X. The helper is an ordinary executable, not a
setuid binary; sudo supplies elevation for explicit operations.

Create X's private `~/.config/server-man/config.json`:

```json
{
  "allowWrites": true,
  "privilegedOperations": true
}
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
`scope` of `system` or `user`, defaulting to `systemdScope`.

Keep `privilegedOperations` false for a read-only/unprivileged deployment.
`allowWrites` remains the shared HTTP/MCP write gate. When privilege routing is
enabled, package and share operations and system service operations use the
helper; user services run directly as X. Privileged operations use automatic
native package-manager detection, `/etc/samba/smb.conf`, the owned Samba fragment
`/etc/samba/server-man.conf`, and the compatible NFS fragment
`/etc/exports.d/serverman.exports`. Conflicting app overrides are rejected.
Root-owned helper state is separate at `/var/lib/server-man-admin`.

The Updates page also offers module dependency installation for Podman/Compose,
Docker/Compose, Samba, NFS, x86 QEMU/libvirt only. These are native
repository packages; missing repository packages fail explicitly. Restart the
app after installing a provider to refresh discovery. Dependency installation
does not change group memberships or container ownership. Use the separate share
setup action to configure and enable Samba/NFS. Incus requires a separately installed and initialized local daemon with the configured project, pool and network; automatic Incus package setup is not yet provided.

The helper requires the default `/usr/local` installation prefix. For custom
prefixes, update the compiled helper path and sudoers rule together. Do not enable
`NoNewPrivileges` or service sandbox options that imply it on this sudo-based
user service: that prevents elevation. Retain loopback/TLS and authentication
protections. X is trusted to administer the allowed capabilities, including
package scripts that run as root; the helper does not make package administration
low privilege.

The legacy system unit remains available for deployments deliberately running
server-man as root. Root does not manage X's rootless Podman inventory or X's
user manager. Do not enable both deployments on the same listener.

## Naming compatibility

The product is named **server-man**. Keep the installed executable and unit as `server-man` and `server-man.service`, and keep `/etc/server-man`, `/var/lib/server-man` and the user XDG `server-man` directories. This preserves existing scripts, service enablement, tokens and audit history. Packaging descriptions and MCP identity use `server-man`. Share format markers and filenames remain compatible with existing owned fragments. No second service or automatic path migration is installed.

## TLS reverse proxy

Keep server-man on loopback and terminate TLS at a trusted reverse proxy. Example Caddy site:

```caddyfile
server.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Set `publicOrigin` to the exact browser origin, for example `https://server.example.com`, in the server-man config. This enables Secure session cookies and origin validation. Keep the proxy's upstream on loopback, restrict who can reach the public site, and configure proxy logs to omit `Authorization`, `Cookie`, and request bodies. Do not expose the unauthenticated management socket publicly. MCP HTTP uses the same authenticated endpoint; remote agents should use HTTPS and a bearer token. See [MCP configuration](design/mcp.md).

The example system unit expects `/usr/local/bin/server-man`. If installing under another prefix, adjust `ExecStart` before enabling it. Back up configuration and state before upgrading; package updates and service management remain explicit operations initiated through server-man.


## Direct LAN HTTP

For a headless host that should serve HTTP directly, set `listen` to
`0.0.0.0:8080` and `publicOrigin` to the host's non-loopback IP and same port,
for example `http://192.168.192.78:8080`. This explicit configuration enables
IPv4 remote access and restricts accepted Host and Origin values to that
address. The service does not configure firewall or router rules; permit port
8080 only on a trusted network. Browser session cookies are HttpOnly and
SameSite Strict, but are not Secure over HTTP. The project editor can return
private Compose/env file contents to authenticated web and MCP clients, and env
values travel over HTTP. For untrusted networks, use the TLS reverse proxy
setup above instead.

The current headless host uses this configuration with writes disabled. See its
[access and verification record](operations/host-setup.md#direct-lan-http-follow-up-2026-10-16).
