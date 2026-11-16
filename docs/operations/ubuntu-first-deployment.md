# Ubuntu deployment

Current deployment: Ubuntu 26.10 amd64, workload account `shasak`. Server-man is installed under `/usr/local` and runs through the system unit `/etc/systemd/system/server-man.service` as UID/GID 1000. The prior user unit is disabled. Lingering and the account's user bus are configured; the desktop manager was not restarted.

The single active configuration is `/home/shasak/.config/server-man/server-man.yaml` (0600), including `server` settings and managed resources. No JSON bootstrap or manifest selector exists. The private token remains `~/.config/server-man/token`. The listener is loopback at <http://127.0.0.1:8080>; UI and `/healthz` return HTTP 200. See [installation and remote access](../installation.md) for binding settings and source/release installation.

Use `sudo systemctl status server-man`, `sudo journalctl -u server-man`, and `sudo systemctl restart server-man`. Server-setting saves require a restart; resource edits use reviewed central apply. No workstation workloads or shares were imported or created, and `~/vms` is untouched. Archival/restore development remains deferred and preserved.

Writes and privileged operations are enabled by explicit user approval. The exact no-argument helper rule in `/etc/sudoers.d/server-man` passes isolated `visudo` validation. The root-owned helper and parents are 0755. Full sudoers validation found an unrelated preexisting `/etc/sudoers` mode of 0644 instead of 0440, left unchanged. User-approved Docker, Incus-admin and libvirt account grants are active in the application process; native read-only Docker/libvirt/Incus inventory succeeds. No permanent direct KVM group grant was made. Host Incus pool/network initialization remains pending the user's concrete settings choice.

The user explicitly exempted the earlier `docker.io docker-compose-v2 incus samba nfs-kernel-server` installation from the development-host package prohibition: 48 new packages, no upgrades/removals. Subsequent installer reruns use `--skip-packages`. They do not initialize Incus or expose shares. Native acceptance uses a separate disposable guest, never the workstation's existing workloads; [the release tracker](../plans/single-server-release.md) records its progress and remaining gates. Debian/Arch fresh-install and full boot/client acceptance remain unproven.

The dated evidence below records earlier deployment stages; the current settings and commands above supersede historical bootstrap/user-service references.

## Guided installer follow-up (2026-11-15)

The shared source/release installer was applied with `--user shasak --backends all --skip-packages --yes`, using the already approved/installed dependencies. It preserves bootstrap configuration, validates/reinstalls the helper rule, and adds Docker, Incus-admin and libvirt account memberships. Read-only Docker and Incus inventory queries now succeed under a fresh workload-account process. `/proc` confirms the running server-man process has Docker GID 103 and Incus-admin GID 975.

The installer replaces the earlier user-service deployment with `/etc/systemd/system/server-man.service`, running as `User=shasak`, UID/GID 1000, with the account's home and user bus. The prior user service is stopped/disabled; the desktop/user manager was not restarted. Use `sudo systemctl status server-man` and `sudo journalctl -u server-man` for the current deployment. HTTP health remains 200. Incus has no configured storage pool or managed network; no guest/image/share was created. Group access does not establish complete workload readiness.

The extracted final release installer was then rerun with the same backend/account selection and `--skip-packages`. Bootstrap configuration and access-token bytes were preserved exactly. The installed system unit passed verification (apart from warnings in unrelated installed xfs units), and `/healthz` returned 200. Source and release entry points both passed dry-run checks; the native amd64 bundle and SHA256 sidecar are in ignored `dist/`. No fresh-host Debian/Arch or alternate-architecture installation is claimed.

## Single-file configuration follow-up (2026-11-15)

The active configuration is now `/home/shasak/.config/server-man/server-man.yaml` (0600), containing `version: 1`, the `server` settings section and managed resource sections when defined. The installed system unit selects this file. There is no JSON bootstrap or manifest selector. All previous server settings were preserved; no prior resource YAML or adjacent apply/backup journals existed. After authenticated readiness/model checks confirmed configured/valid state, exact preservation of specified server values and no restart blocker, the obsolete active `config.json` was removed. A private temporary checkpoint remains outside the active configuration in `/tmp`; no migration/compatibility mechanism was added.

The access token and state directory are unchanged. UI health remains HTTP 200. Packaging examples and system/user units now use YAML. Server-setting changes require restarting server-man; the reviewed resource workflow preserves the server section and reports pending restart. This update created no workloads/shares or Incus storage/network configuration and installed no packages.

## First-use UI and readiness follow-up (2026-11-15)

Deployed `b6c33d3` through the shared installer with `--skip-packages`; configuration, backend grants and the workload account are preserved. The refreshed native amd64 release archive includes the same changes. A real authenticated Firefox session at 390x844 confirmed that the running server displays “No managed applications yet,” keeps Define application enabled, and has no horizontal overflow or page errors. This browser check made no workload/configuration mutations. An initial ad hoc attempt omitted opening the mobile navigation and was cancelled; the corrected check passed.

The shared readiness fix is verified with temporary fixtures: pending-server-restart issues appear once at configuration/service/share scopes, reach application inventory, survive native cache reuse, and clear after the running settings match. No live server-setting change or native workload/VM/share mutation was used to test that condition. Incus pool/network creation remains pending the user's settings choice; it has not been inferred from elapsed time.

## Docker boot prerequisite follow-up (2026-11-15)

Deployed `598b55d` using the shared installer with existing packages. The reviewed host-unit list explicitly includes Docker's socket before its daemon. This host's socket was already persistently enabled; native ordering shows it before sockets.target, sockets.target before basic.target, and the workload account's user manager after basic.target. The installer rerun preserved the Docker daemon PID and exact unified configuration bytes. HTTP health and the refreshed release checksum passed. No Docker daemon restart, host reboot, workload mutation or Incus initialization was performed.

Temporary command fixtures prove that Docker Compose boot readiness distinguishes persistent enablement, disabled/runtime-only/missing socket states and unreadable/malformed probes. Autostart blockers prevent boot enablement while leaving current-run readiness available when the engine is reachable. This verifies prerequisite reporting, not the full real reboot/daemon-restart matrix.

## Delegated libvirt readiness follow-up (2026-11-15)

Deployed `79859c0`, preserving the exact unified configuration and existing groups. The prior readiness code tried to open `/dev/kvm` as the app account, which incorrectly rejected this host's working system connection. The device is visible outside the sandbox, but is inaccessible directly to shasak. Native system libvirt nevertheless reports the supported KVM profile through its daemon; the session connection does not.

Direct execution of the revised server-man prerequisite function under UID 1000 returned zero issues for qemu:///system and the expected KVM blocker for qemu:///session. Fixed-query, locale, timeout, native XML shape/profile and sanitized failure handling are covered by fixtures and the full Go race suite. Native inspection created no VM and granted no direct KVM permission.

The installer update completed and the service remained active. An immediate ad hoc HTTP probe raced process startup; rechecking the same service confirmed running state and HTTP 200 without reinstalling/restarting it. The refreshed release archive checksum passed. Native guest lifecycle/reboot acceptance remains open; no disposable guest was provisioned by this increment.

## Native prerequisite and Compose fixes (2026-11-16)

Build `a5916d5` is installed and packaged in the native amd64 release. It includes the libvirt resolved-network observation fix and supported stdin-based Incus mutation commands, with absence-only failed-definition cleanup and VM dependency checks. It includes corrected Quadlet raw scalar encoding, missing configured rootless networking-helper checks, Compose launcher sources outside systemd's unit hierarchy, and consistent podman-compose selection across readiness/validation/runtime/boot. The shared dependency preset explicitly includes passt; no additional workstation package was installed.

Release checksum/extraction and the shared installer with `--skip-packages` passed. Exact unified YAML/token bytes and the Docker daemon PID were preserved; installed version and HTTP 200 were verified. `make check test` passed with zero frontend diagnostics/full Go race suite; `make mcp-test` passed all seven acceptance contracts and official SDK stdio/HTTP 55-tool parity/authentication checks. Native guest evidence is tracked separately and does not establish reboot or share-client acceptance.

## Native results and approved VM dependencies (2026-11-16)

All seven backends now have selected native operation evidence in [the dedicated guest record](native-acceptance.md), across recorded builds and explicit reviewed continuation. Containers/libvirt passed lifecycle and independent boot-policy/deferred-update cases; Incus also passed its retained-instance continuation with stable UUID/MAC; Samba/NFS passed central publication/update/unpublication/removal. The shared data sentinel remained unchanged, and the Incus VM remains stopped/boot-disabled in the disposable guest. A final uninterrupted all-target run, actual reboot, remote share clients and fresh Debian/Arch checks remain open.

The user explicitly approved the additional workstation firmware/SPICE dependencies after review of cached APT simulation. `sudo apt-get --assume-yes --no-remove --no-install-recommends install ovmf qemu-system-modules-spice` completed: 25 new packages, zero upgrades/removals, no repository refresh or reboot. A direct read-only probe confirms SPICE option declarations now load. No workstation Incus pool/network, VM, container or share definition was created. Initial sandbox-only service-bus checks were denied by isolation; host service/readiness checks use the real account context outside that isolation.


A complete read-only check initially found missing pasta and, after the DNS audit, aardvark-dns. The user approved both packages together. `sudo apt-get --assume-yes --no-remove --no-install-recommends install passt aardvark-dns` completed with exactly two new packages, zero upgrades/removals, no repository refresh and no reboot. All five provider prerequisite functions subsequently returned zero issues as shasak with refreshed groups and the real user bus: Podman Quadlet, Podman Compose, Docker Compose, libvirt and Incus. These checks establish program/access prerequisites, not configured Incus storage or networking.

Build `31109c5` is installed and packaged. The shared installer rerun with `--skip-packages` preserved exact YAML/token bytes and the Docker daemon PID; installed version and HTTP health 200 passed. Host Incus pool/network initialization remains pending; no dependency repair implies resource creation. Guest cold-boot and share-client acceptance remain open as described in the native record.
