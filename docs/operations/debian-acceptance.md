# Debian disposable installation acceptance

Recorded 2026-11-16 UTC. This is a dedicated Debian 13 guest used for distribution acceptance, separate from the installed Ubuntu workstation and its preserved test guest. It is not a claim of full Debian workload, boot or share-client acceptance.

The official Debian trixie genericcloud amd64 build is 20261111-2659, image `debian-13-genericcloud-amd64-20261111-2659.qcow2`, 341901312 bytes. Publisher HTTPS SHA512SUMS from the [fixed release directory](https://cloud.debian.org/images/cloud/trixie/20261111-2659/) matched:

```text
3247873136ba09b060fab45e9c115239f81c1d401fce00b8fa9b872013f7135d6a40b12462ab5ac03b9fa4ed5955dd0e8d1c900109a939ad7a0a4dafe7dea47b
```

No detached-signature verification is claimed. The guest uses a private sparse 24 GiB overlay, two vCPUs/6 GiB RAM and nested KVM. It shares no host filesystem, existing workload or physical device. SSH uses a private generated key and pinned host key on loopback port 22473. Its fixture hostname is server-man-test and account manager is UID/GID 1000, with broad test-only preparation sudo access; this does not establish restricted-sudo-only acceptance. Keys/seed/logs are private ignored artifacts. The completed Ubuntu guest was verified idle and gracefully powered off; its disk and retained Incus data were preserved. The workstation was not rebooted.

## Initial release installation

The actual packaged release `install.sh --user manager --backends all --yes` ran on the fresh guest, including repository dependency installation, without `--skip-packages`. It completed on 2c15260. Native inspection verified:

- Active system server-man service running as UID 1000/manager, with docker, libvirt and incus-admin groups in the actual process.
- One private owner-0600 YAML and token, no JSON bootstrap.
- Root-owned 0755 helper and the exact no-argument manager helper rule, passing isolated visudo validation.
- Installed version 0.1.0-alpha.1+g2c15260a7542, HTTP health 200 and nested /dev/kvm available.

The five real shared backend prerequisite functions then ran as manager with its user bus. Podman Quadlet, Podman Compose, libvirt and Incus returned zero issues. Docker correctly reported a missing executable. Actual Debian repository/package status confirmed docker.io and docker-compose installed, docker-cli absent, and both packages only recommending docker-cli. The [native package relationship](https://packages.debian.org/trixie/docker.io) explains why no-recommends installation omitted it.

The shared Debian dependency preset now explicitly requests docker-cli for guided/source/release installation and Software dependency setup. Command fixtures and full race/type checks cover the correction. No workstation package was installed or refreshed by this Debian acceptance work. Incus pools/networks and managed workloads/shares have not been initialized in this guest.

## Corrected installer and provider access

The packaged 02ec1fe release install.sh was rerun with all backends and actual package installation, using the shared corrected preset rather than an ad hoc CLI repair. It installed docker-cli successfully. Exact YAML/token bytes were preserved, the existing Docker daemon PID was unchanged, installed version matched and HTTP health returned 200.

All five shared prerequisite functions then returned zero issues as manager with the real user bus: Podman Quadlet, Podman Compose, Docker Compose, system libvirt and Incus. Observed versions:

| Component | Version |
| --- | --- |
| server-man | 0.1.0-alpha.1+g02ec1fe8f334 |
| Podman | 5.4.2 |
| podman-compose | 1.3.0 |
| Docker CLI/server | 26.1.5+dfsg1 |
| Docker Compose | 2.26.1-4 |
| libvirt | 11.3.0 |
| Incus client/server | 6.0.4 |

This is a fresh-guest installation followed by an explicit corrected installer rerun, not a pristine corrected-build installation or an uninterrupted workload matrix. Global program/account/device prerequisites do not prove per-resource image/storage/network readiness. At this installer checkpoint, no VM pool/network, managed workload or share had been created. Full Debian lifecycle/boot/share-client and source-build installation checks remain open. Native Arch testing is deferred by user instruction and excluded from the current release gates. The installed Ubuntu workstation remains on its preceding verified release; this Debian package correction did not alter it.

## Current-build fixture preparation and Quadlet validation

After incorporating the independently developed managed-log/member/readiness changes, c24e5e1 passed local `make check test mcp-test ui-test`, including the full race suite, 12 workflow/console contracts, 56-tool transport parity and desktop/mobile browser checks. Its release was installed only in the existing Debian guest with packages skipped. Read-only baseline checks confirmed empty managed resource/reference inventories before fixture preparation.

Dedicated guest-only assets now include a standalone 8 GiB disk cloned from the verified public base image with explicit workload/QEMU access, a shared data sentinel, isolated libvirt/Incus networks, a dir pool and local Incus VM image. The same digest-pinned container fixture was preloaded into both engines. Storage/binding references were saved through typed revision-pinned operations, preserving the installer-created server section; Samba/NFS setup completed through shared privileged operations. No workstation package, workload, share or network was changed.

The first seven-target attempt stopped during read-only candidate planning for Quadlet, before creating a managed workload or share. A separate read-only diagnostic confirmed the empty inventories and unchanged central revision. Isolated generator dry-runs identified the unsupported `Memory=` key on packaged Podman 5.4.2; forwarding the same limit through `PodmanArgs=--memory=64m` generated the expected service and native memory flag successfully. The renderer now uses that one fixed typed-argument path rather than pinning/upgrading/downgrading the provider or weakening the fixture's resource limit. The local native-generator regression also verifies the requested memory flag. Full repaired-build lifecycle acceptance remains pending.

After installing 7d3536f with packages skipped, read-only planning passed Quadlet and exposed the next candidate rejection: podman-compose 1.3.0 does not support config --quiet. Fixed temporary fixtures confirmed config succeeds without that flag, with stdout discarded at the process boundary. Shared candidate validation now runs config with both output streams sent directly to discard rather than capturing resolved output. Provider failures still reject the candidate. No managed workloads/shares or central revision changed during these diagnostics.

Read-only session inspection then found dbus-user-session uninstalled, dbus.socket missing/inactive and no account bus socket. Podman info selected cgroupfs both with inherited SSH environment and an explicit user-bus address. The reachable systemd user manager alone had masked this missing prerequisite in earlier checks. Shared APT session dependencies now include the package; bootstrap explicitly reloads/starts the user's bus socket without restarting its manager. Both Podman adapters report unavailable/unknown bus state before activation; Docker does not inherit that runtime blocker.

The actual packaged 724e426 installer subsequently completed with the shared dependency preset. Exact YAML/token bytes, Docker's PID and the account user-manager PID were preserved; HTTP health returned 200. The bus socket exists and Podman selects systemd cgroups with both inherited SSH environment and an explicit bus address. All seven read-only candidate plans passed without changing the central revision.

The ensuing native lifecycle attempt failed at the first Quadlet start. The exact generated fixture unit's journal reports `netavark: nftables error: unable to execute nft: No such file or directory`. The unit was automatically retrying; observation confirmed no retained container and the disposable fixture unit was explicitly stopped to contain the loop. Its managed definition and boot policy remain for investigation; the harness was not replayed and no remaining target lifecycle is claimed. This exposes an additional installer/readiness dependency gap. No workstation packages, services or workloads were changed. Full Debian lifecycle, boot, client and recovery acceptance remain open.

## nft dependency correction and retained-fixture recovery

The packaged 8d354d2 installer completed in the same guest with its shared all-backend dependency preset. It installed nftables 1.1.3-1 as the only new package, with zero upgrades/removals. Exact YAML/token bytes and Docker/user-manager PIDs were preserved and HTTP health returned 200. The native nftables service remained disabled/inactive. A post-install observation script failed after these checks because non-login SSH omitted `/usr/sbin` from PATH; this was a diagnostic failure after a successful installer, not a reason to replay installation.

Read-only inspection confirmed nft at `/usr/sbin/nft` and the user manager's standard PATH containing that directory. The native SDK harness now supplies the standard system-tool PATH explicitly for its SSH stdio process, matching the installed service/user manager rather than inheriting Debian's narrower non-login SSH PATH. With that environment, freshly built shared prerequisites returned zero issues for all five backends. Before installation, both Podman adapters had reported exactly the new nft blocker; Docker/libvirt/Incus were already clear.

The retained Quadlet fixture then started successfully through shared management tools. Observation verified it running while still boot-enabled; explicit stop, disable and definition removal also succeeded. Server settings and all storage/binding/private-file references were preserved. External data was not removed. This is an explicit isolated recovery and cleanup, not a completed seven-target run, daemon-restart or full recovery-profile acceptance. No workstation package, service or workload was changed.
