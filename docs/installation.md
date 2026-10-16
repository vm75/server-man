# Install and operate server-man

server-man is a native Linux binary. Node is needed only to build or run the verification tools. Build with `make setup build`, then install the built binary and examples with `sudo make install`. Installation copies files beneath `/usr/local` (or `PREFIX`) and honors `DESTDIR`; it does not enable or start a service, create accounts, or replace an active configuration. For packaging checks, use a temporary staging root such as `make install DESTDIR="$PWD/.cache/install-root"`.

Copy `/usr/local/share/serverman/config.example.json` (or `packaging/serverman.example.json` from the source tree) to `/etc/serverman/config.json`, then set ownership to root and mode 0600. Adjust `vmRoot` to the directory containing your VM/Compose conventions. Create the token file as a random private secret, for example:

```sh
sudo install -d -m 0700 /etc/serverman
sudo sh -c 'umask 077; openssl rand -base64 32 > /etc/serverman/token'
sudo chmod 0600 /etc/serverman/token
```

The system unit runs as root because system package updates, system services, host shares, and some virtualization operations need host administration rights. That grants the authenticated manager broad host authority. Restrict access to the token and network endpoint. Root does not manage another user's rootless Podman engine. For rootless Podman, use the user unit under the account that owns those containers, with a separate config, state directory, and token. User-scope systemd management controls that user's units; it does not gain system administration rights. Create `~/.config/serverman/config.json` with private permissions; `{"allowWrites":false}` uses that user's default `~/vms`, token, and state paths.

Install the unit file with `sudo make install`, review `/usr/local/lib/systemd/system/serverman.service`, then enable it using your normal systemd administration process. The user unit is installed under `/usr/local/lib/systemd/user`; user lingering may be needed if it must run without an active login. The config defaults to loopback and writes disabled. Set `allowWrites` to true only after reviewing the configured account and network boundary.

## Naming compatibility

The product is named **server-man**. Keep the installed executable and unit as `serverman` and `serverman.service`, and keep `/etc/serverman`, `/var/lib/serverman` and the user XDG `serverman` directories. This preserves existing scripts, service enablement, tokens and audit history. Packaging descriptions and MCP identity use `server-man`. Share format markers and filenames remain compatible with existing owned fragments. No second service or automatic path migration is installed.

## TLS reverse proxy

Keep server-man on loopback and terminate TLS at a trusted reverse proxy. Example Caddy site:

```caddyfile
server.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Set `publicOrigin` to the exact browser origin, for example `https://server.example.com`, in the server-man config. This enables Secure session cookies and origin validation. Keep the proxy's upstream on loopback, restrict who can reach the public site, and configure proxy logs to omit `Authorization`, `Cookie`, and request bodies. Do not expose the unauthenticated management socket publicly. MCP HTTP uses the same authenticated endpoint; remote agents should use HTTPS and a bearer token. See [MCP configuration](design/mcp.md).

The example system unit expects `/usr/local/bin/serverman`. If installing under another prefix, adjust `ExecStart` before enabling it. Back up configuration and state before upgrading; package updates and service management remain explicit operations initiated through server-man.


## Direct LAN HTTP

For a headless host that should serve HTTP directly, set `listen` to
`0.0.0.0:8080` and `publicOrigin` to the host's non-loopback IP and same port,
for example `http://192.168.192.78:8080`. This explicit configuration enables
IPv4 remote access and restricts accepted Host and Origin values to that
address. The service does not configure firewall or router rules; permit port
8080 only on a trusted network. Browser session cookies are HttpOnly and
SameSite Strict, but are not Secure over HTTP. For untrusted networks, use the
TLS reverse proxy setup above instead.

The current headless host uses this configuration with writes disabled. See its
[access and verification record](operations/host-setup.md#direct-lan-http-follow-up-2026-10-16).
