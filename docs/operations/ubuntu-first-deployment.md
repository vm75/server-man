# Ubuntu first deployment

Recorded 2026-11-15 on Ubuntu 26.10 (amd64). This is the first application deployment, not complete native lifecycle/boot acceptance or Debian/Arch verification.

server-man is installed under `/usr/local`, running as account `shasak` through `systemctl --user`. The service is enabled and the account already has lingering enabled. UI `/` and `/healthz` return HTTP 200 at <http://127.0.0.1:8080>.

Bootstrap configuration: `~/.config/server-man/config.json` (0600). Authoritative workload configuration: `~/.local/state/server-man/server-man.yaml`. At initial startup this YAML is absent; Configuration returns a valid `version: 1` starter and revision `missing` for first-save review. No workloads or shares were imported or created. Sign in using the private `~/.config/server-man/token`; do not copy it into logs or this document.

The installed user service uses `allowWrites: true` and `privilegedOperations: true`. The user explicitly approved the exact no-argument helper rule in `/etc/sudoers.d/server-man`; that file passes `visudo -cf`. The root-owned helper and parent directories are mode 0755. Full `visudo -c` reported a pre-existing `/etc/sudoers` mode of 0644 rather than 0440; that unrelated file was not changed.

The user explicitly exempted this deployment from AGENTS.md's development-host package-installation prohibition. Installed `docker.io docker-compose-v2 incus samba nfs-kernel-server` using existing package indexes: 48 new packages, no upgrades/removals; Compose v2 was already installed. Native packages may enable/start their daemons. No shares were exposed by server-man, and Incus storage/network initialization was not performed.

Read-only provider checks: Podman 5.8.6 responds; libvirt `qemu:///system` inventory succeeds. Docker client 29.7.2 and Compose 2.40.3 are present, but Docker socket access is denied to the workload account. Incus is installed but account socket access is denied and initialization remains required. Account access changes and concrete Incus pool/network settings remain pending.

Verification: `make check test mcp-test ui-test` passed, including zero frontend diagnostics, full Go race tests, 55-tool official SDK stdio/HTTP checks and Firefox desktop/mobile checks. `make install DESTDIR=/tmp/server-man-release-stage` passed. Installed user unit passes `systemd-analyze --user verify`. Authenticated readiness and configuration-read requests succeed and accurately report the missing initial manifest. No native container/VM lifecycle, host reboot, share-client access or restore acceptance was performed; `~/vms` was untouched.

Use `systemctl --user status server-man.service` and `journalctl --user -u server-man.service` for service diagnosis. Stop with `systemctl --user stop server-man.service`. Workload and share setup proceeds through the UI's reviewed central configuration; the [release checklist](../plans/single-server-release.md) retains outstanding acceptance. Backup/restore development remains deferred and preserved.

## Guided installer follow-up (2026-11-15)

The shared source/release installer was applied with `--user shasak --backends all --skip-packages --yes`, using the already approved/installed dependencies. It preserves bootstrap configuration, validates/reinstalls the helper rule, and adds Docker, Incus-admin and libvirt account memberships. Read-only Docker and Incus inventory queries now succeed under a fresh workload-account process. `/proc` confirms the running server-man process has Docker GID 103 and Incus-admin GID 975.

The installer replaces the earlier user-service deployment with `/etc/systemd/system/server-man.service`, running as `User=shasak`, UID/GID 1000, with the account's home and user bus. The prior user service is stopped/disabled; the desktop/user manager was not restarted. Use `sudo systemctl status server-man` and `sudo journalctl -u server-man` for the current deployment. HTTP health remains 200. Incus has no configured storage pool or managed network; no guest/image/share was created. Group access does not establish complete workload readiness.

The extracted final release installer was then rerun with the same backend/account selection and `--skip-packages`. Bootstrap configuration and access-token bytes were preserved exactly. The installed system unit passed verification (apart from warnings in unrelated installed xfs units), and `/healthz` returned 200. Source and release entry points both passed dry-run checks; the native amd64 bundle and SHA256 sidecar are in ignored `dist/`. No fresh-host Debian/Arch or alternate-architecture installation is claimed.

## Single-file configuration follow-up (2026-11-15)

The active configuration is now `/home/shasak/.config/server-man/server-man.yaml` (0600), containing `version: 1`, the `server` settings section and managed resource sections when defined. The installed system unit selects this file. There is no JSON bootstrap or manifest selector. All previous server settings were preserved; no prior resource YAML or adjacent apply/backup journals existed. After authenticated readiness/model checks confirmed configured/valid state, exact preservation of specified server values and no restart blocker, the obsolete active `config.json` was removed. A private temporary checkpoint remains outside the active configuration in `/tmp`; no migration/compatibility mechanism was added.

The access token and state directory are unchanged. UI health remains HTTP 200. Packaging examples and system/user units now use YAML. Server-setting changes require restarting server-man; the reviewed resource workflow preserves the server section and reports pending restart. This update created no workloads/shares or Incus storage/network configuration and installed no packages.
