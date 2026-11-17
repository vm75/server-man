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

## Corrected-build matrix and libvirt boot precondition

After explicit cleanup and empty native-container inventory checks, one new uninterrupted seven-target attempt used application/helper/installer build 8d354d2 and runner 267d0b1. Podman Quadlet, Podman Compose and Docker Compose each passed their full define/update-stopped, start/stop/restart, independent boot-policy and deferred-update case, including definition removal with retained external data. Provider versions remain those listed above; nftables is 1.1.3-1.

Libvirt stopped definition/update/enable and the first native start succeeded. Separately authenticated requests to the installed HTTP service reported valid configuration, zero readiness issues and the fixture running/boot-enabled with registered configuration. The SDK runner then stopped at its bounded serial-login precondition: the guest did not reach login within three minutes. Direct bounded serial observation showed repeated Debian bootloader messages. The domain remains running; QEMU reported three unsupported nested VMX feature warnings, while the bounded kernel-warning query showed no matching KVM/QEMU/fault report. These observations do not establish the cause or prove the guest OS healthy.

No lifecycle request was replayed after this terminal failure. The dedicated libvirt definition/disk remain for diagnosis; its boot policy is still enabled. Incus and both share cases were not reached. The shared data sentinel was rechecked and unchanged. This is three complete native container cases plus partial libvirt evidence, not seven-target Debian completion. Native VM boot diagnosis, remaining lifecycle cases, logs, boot/daemon restart, client access and broader failure-profile gates remain open. No workstation workload or package was changed.

## Libvirt BIOS video correction and explicit recovery

Networkless snapshot probes of the unchanged verified public image reproduced failed normal BIOS boot outside the application with host, VMX-hidden and minimal CPU models. Direct kernel/initrd KVM boots reached serial login with both diagnostic and original kernel parameters. A normal UEFI probe reached login too; this does not add UEFI support to server-man. The decisive BIOS comparison added only a standard VGA device and reached login. CPU warnings remained present in that successful case, so they were not established as the cause. All diagnostic VMs were bounded and terminated; private raw/image-inspection copies used read-only loop mappings that were detached. The managed VM/disk were untouched during diagnosis.

Release a9fca76 now includes the VGA model in generated libvirt XML and persistent/live hardware comparison, with no graphics listener. Full Go race/type and SDK checks passed, plus native XML schema validation. The guest-only packaged installer ran with packages skipped and preserved exact YAML/token bytes, Docker/user-manager PIDs and HTTP health.

The old disposable inner VM was explicitly powered off after ownership/UUID/disk checks because it was looping before OS boot. This was fixture recovery, not a force-stop fallback in application lifecycle operations. Its disk inode and YAML remained unchanged. The first reviewed apply then failed conservatively because the earlier build's derived XML differed from both current renderings. That specific owned artifact was verified and preserved privately; explicit shared apply retry published the corrected definition. No compatibility/import mechanism or relaxed ownership rule was introduced. The apply error was too generic to guide this repair, so actionable artifact-conflict diagnostics/repair remain an open product gap.

The corrected generated VM reached serial login through the SDK verifier, with unchanged UUID/MAC. Its actual QEMU launch included the VGA device and display output disabled. Shared graceful Stop, Disable and definition removal succeeded; server settings and all storage/binding/private-file references were preserved. The external disk remains. This is an explicit successful libvirt recovery case, not yet the full final-build seven-target run, daemon restart or broader guest-health acceptance. No workstation package, service or workload was changed.

## Four-target pass and Incus deferred-update failure (2026-11-17)

After explicit recovery cleanup and empty-inventory guards, one new uninterrupted attempt on a9fca76 passed all three container targets and the full libvirt case: stopped creation/update, current-run controls, independent boot policy, deferred running update, stable VM identity and data-preserving definition removal. This closes the earlier libvirt serial-boot failure for this BIOS/q35 profile.

Incus passed stopped creation/update, enable without start, the first start with serial login, disable without stop and enable while running. The runner then terminated at the next-start memory update from 1536 to 2048 MiB. The authoritative YAML retains the requested 2048 MiB and enabled policy; the native instance remains running at 1536 MiB. The adapter currently attempts native configuration edit while active. Read-only server inspection advertises no memory-hotplug API extension. Upstream documents [VM memory growth support introduced with Incus 6.12](https://discuss.linuxcontainers.org/t/incus-6-12-has-been-released/23556); this is consistent with a capability gap, but the native edit's discarded stderr does not prove its exact failure reason. Correct deferred application/capability behavior rather than pinning or upgrading around the test.

The failed operation was not replayed. The Incus instance, root data, failed apply journal and desired configuration remain for diagnosis; the shared-data sentinel was unchanged. Both share cases were not reached. The serial-console verifier's Incus branch has now executed successfully against the real VM. The native managed diagnostic reader, uninterrupted seven-target result, boot/daemon restart, client and broader recovery gates remain open. No workstation dependency or workload was changed.

## Explicit stopped recovery, native diagnostics and share clients

On a9fca76, first-read authenticated HTTP and native MCP readiness both exposed the incomplete apply while still reporting the Incus VM running. The opt-in Incus diagnostic reader succeeded over installed HTTP during the failure and over MCP after recovery; only source/byte-bound metadata was retained, not runtime log text. This establishes access to the scoped native instance diagnostics, not guest application logging or large-output truncation behavior.

An explicit shared graceful Stop preserved enabled boot policy. Apply retry after that verified stop registered the requested 2048 MiB without starting the VM. Disable then left it stopped/boot-disabled, with stable UUID/MAC and unchanged server/storage/binding/private-file references. The instance/root disk remain. This is stopped recovery of a failed deferred update, not a successful running next-start update; the release behavior for that case remains unresolved.

An explicit share-only continuation then passed Samba and NFS central define/update/publish/unpublish/remove sequences and observed unmanaged-inventory preservation, leaving the retained VM unchanged. Separate guest-only client checks installed smbclient and used temporary private SMB credentials, a conflict-checked veth/network namespace and separate mount namespaces. Both protocols passed read-only reads/write denial, writable data round-trips under the workload identity, changed user/client restrictions, restored access and fresh-client denial after unpublication. Samba also rejected wrong credentials and anonymous access. NFS requested v4.2 and verified root-squashed write denial. An additional Samba-only sequence explicitly required minimum SMB3, closing the distinction between a maximum-protocol option and a proven SMB3 connection.

Observed clients: smbclient 4.22.11-Debian-4.22.11+dfsg-0+deb13u1 and nfs-utils 2.8.3. Both central share definitions were removed. Cleanup verified no client mounts/processes or probe data remained, removed only the temporary network/SMB account/credentials/files and rechecked the unchanged shared sentinel. The Incus instance remains stopped/boot-disabled. This is an isolated same-kernel client inside the disposable guest, not a separate physical client or proof of all identity/ACL/network profiles. It does not close the failed uninterrupted matrix, daemon-restart or broader recovery gates. No workstation package, network or share was changed.

## Daemon restart and corrected native-autostart status (2026-11-17)

On a9fca76, restarting Incus left the boot-disabled stopped VM stopped, with a changed provider PID and preserved configuration/token and Docker/server-man PIDs. Enabling boot through shared tools kept it stopped. Restarting only server-man then kept the boot-enabled manually stopped VM stopped through a ten-second observation window, preserving Docker/Incus PIDs and configuration/token. A further Incus restart, with every server-man process absent, autonomously started the enabled VM; restoring the web service preserved the VM process, identity and unrelated daemon PIDs. However, the application falsely reported restart required because the old runtime marker survived stopped hardware edits.

Corrected release 42ac77e passed `make check test mcp-test` and a final focused uncached Incus race run. The actual guest installer used packages skipped and preserved exact YAML/token bytes and Docker/Incus/user-manager PIDs. New shared graceful Stop performed the bounded metadata-only tracking update without changing desired hardware or boot policy. The same native-autostart scenario was then repeated with a separate checkpoint after that repaired precondition: Incus started the VM while all server-man processes were absent, and the restored HTTP application correctly reported registered configuration with matching runtime tracking. The VM process/identity, configuration/token and unrelated PIDs were preserved.

Final shared graceful Stop and Disable succeeded. The retained instance/root data remain stopped/boot-disabled; desired hardware, UUID/MAC, server settings and storage/binding/private-file references were preserved. Native tracking and the unchanged shared sentinel were checked again. No verification process remains active. This closes the observed stopped-update/native-autostart status regression and these selected restart cases, not general external/live-field tracking, the running Incus hardware-edit decision, cold boot, the uninterrupted final matrix or the full goal. The user requested pausing after this subtask. No workstation packages or services were changed.
