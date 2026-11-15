# Single-server release

Status: active, 2026-11-15. This checklist supersedes the broader plan's release sequencing. Deliver a usable deployment first; preserve unfinished archival code without continuing it. DRY, KISS and YAGNI remain key tenets.

## Scope

First deployment: this Ubuntu 26.10 machine, as its workload account. Debian and Arch Linux remain support targets; neither has native acceptance evidence yet.

- One authoritative server-man YAML for managed containers, VMs and Samba/NFS shares. Native definitions are derived state.
- Podman Quadlet and Compose, Docker Compose, KVM/libvirt and Incus VMs. Configure/update, start/stop and independent boot enable/disable through the same validated web/MCP functions.
- Friendly typed editors, review before apply, useful status and alerts for missing programs/providers, permissions, files/directories/disks, mounts, pools and networks. Do not create empty data to conceal a failed prerequisite.
- Host system services and software remain direct operations outside workload YAML. Preserve rootless workload ownership and unmanaged share configuration/data.
- Existing supported VM profiles are the initial boundary: prepared existing disks for libvirt; local images and explicit initialized project/pool/network for Incus. Advanced guest profiles and destructive VM removal are not deployment prerequisites.
- Backup, archival, restore and their native acceptance are deferred. Preserve existing capture/recovery code and tests. Pending recovery journals continue to block unsafe mutations; never bypass them to enable deployment.
- Use the canonical `server-man` spelling throughout; no backward compatibility or migration layer.

## Deployment sequence

- [x] Preserve interrupted archival work separately: `c03b60e`; previous full `make check test` passed. No native archival acceptance.
- [x] Inspect target without mutation. No existing system/user server-man unit or bootstrap configuration found. Podman/libvirt executables present; Docker, Incus, Samba and NFS management executables absent. Presence does not establish working runtime access.
- [x] Simulate missing dependency installation: `apt-get --simulate install docker.io docker-compose-v2 incus samba nfs-kernel-server`. Cached repositories propose 48 new packages, zero removals/upgrades; Compose v2 package already installed. No refresh or installation performed.
- [x] Run `make check test mcp-test ui-test`, then stage `make install DESTDIR=...` and verify packaged paths/unit syntax. Fix actual deployment blockers before extending scope.
- [x] Install the verified application/helper and user service with private bootstrap configuration, loopback listener and explicit manifest path. Validate helper sudo policy and service health without exposing the token.
- [x] Obtain an explicit exception to AGENTS.md's development-host package prohibition before installing missing runtime/share dependencies. Package installation may start system daemons. Do not initialize Incus storage/network or expose shares without concrete reviewed settings.
- [ ] Inspect account access to Podman user systemd, Docker socket, libvirt and Incus. Report necessary access/setup explicitly; executable presence alone is insufficient. Do not alter `~/vms` or import its workloads.
- [ ] Confirm readiness diagnostics for absent prerequisites and fix blockers affecting the core UI/configure/apply workflow.
- [ ] Perform explicitly authorized live acceptance with disposable, clearly owned resources: each backend defines stopped, updates, starts/stops and changes boot policy independently; central shares preserve unrelated native configuration and data. Record actual versions and any unavailable backend. Boot and remote share-client checks require a suitable disposable environment; do not reboot this workstation implicitly.
- [ ] Record access/run instructions and remaining limits. Consolidate completed release decisions into architecture/design/operations docs; replace this checklist with a concise completion record once deployment acceptance is achieved. Keep deferred work tracked separately.

## Completion boundary

A running web service is an application deployment, not proof that all workloads are ready. Distinguish app health, provider availability, fixture coverage, live lifecycle, boot and client checks. Do not claim Debian/Arch support verified from Ubuntu checks. Release scope excludes backup/restore; the broader declarative phase remains incomplete until its retained future requirements pass.

Deployment evidence: [Ubuntu first deployment](../operations/ubuntu-first-deployment.md). Application is installed, enabled and serving HTTP 200. Authenticated readiness correctly reports no initial manifest. Docker/Incus account access and Incus initialization remain pending; no workload/share lifecycle acceptance is claimed. Explicit user approval allowed the helper rule; its isolated syntax check passed. Full sudoers validation found an unrelated existing file-mode issue, recorded in the operations note.
