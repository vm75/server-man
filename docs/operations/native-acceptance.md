# Native deployment acceptance

Recorded 2026-11-16 UTC. This record covers a dedicated disposable Ubuntu guest; workstation application deployment is recorded [separately](ubuntu-first-deployment.md). It is evidence for the selected operations and versions, not distribution-wide, reboot or remote-client certification.

## Environment

The guest uses the official Ubuntu 26.10 amd64 cloud image, release 20261105, with publisher HTTPS SHA256SUMS verification (`9ef2243a4319e43164ab11d61e324efa0dd19ed850979d10c3a6e44feb5684c5`). It has two vCPUs, 6 GiB RAM and a sparse 24 GiB overlay. KVM/nested virtualization is available. The workstation shares no filesystem, existing workloads or physical devices with it. SSH is forwarded to loopback with a pinned host key. Private credentials and logs are retained outside Git. The guest account's test-only preparation sudo access is broad; this does not establish restricted-sudo-only security acceptance.

Native versions:

| Provider | Version |
| --- | --- |
| Podman | 5.8.6 |
| podman-compose | 1.6.0 |
| Docker | 29.7.2 |
| Docker Compose | 2.40.3+ds2-0ubuntu2 |
| libvirt | 12.6.0 |
| QEMU | 11.0.3 |
| QEMU SPICE module package | 1:11.0.3+ds-2ubuntu2 |
| Incus client/server | 7.0.1 |
| Samba | 4.24.7 |
| NFS server package | 1:2.9.1-1ubuntu1 |
| passt | 0.0~git20260728.f8df3f1-1 |
| smbclient (client follow-up) | 4.24.7-Ubuntu-4.24.7+dfsg-1ubuntu1 |
| mount.nfs (client follow-up) | nfs-utils 2.9.1 |

Containers use a preloaded BusyBox image pinned to `sha256:73aaf090f3d85aa34ee199857f03fa3a95c8ede2ffd4cc2cdb5b94e566b11662`. VM fixtures use a prepared standalone BIOS disk and an explicit local Incus VM image/pool/network. All test resources live under the dedicated fixture namespace and data root. Guest package/backend preparation is isolated from workstation workloads; separately approved workstation prerequisite installations are recorded in the deployment document.

## Observed lifecycle results

The official SDK runner uses the same typed revision-pinned configuration edits and job-based lifecycle operations as HTTP/UI. The [repeat procedure](disposable-acceptance.md) defines its guards and limits.

| Target | Verified result |
| --- | --- |
| Podman Quadlet | Full lifecycle sequence passed on 2088bc4 |
| Podman Compose | Full lifecycle sequence passed on 2088bc4 |
| Docker Compose | Full lifecycle sequence passed on 2088bc4 |
| libvirt | Full lifecycle sequence passed on 449e3bd, with serial boot readiness verified before shutdown |
| Incus | Stopped creation/update/enable passed on 7744ccf; reviewed lifecycle continuation passed on a5916d5; retained stopped/boot-disabled |
| Samba | Central publication lifecycle passed on a5916d5; isolated client access/denial/changed policy/unpublication passed on 18020d4 |
| NFS | Central publication lifecycle passed on a5916d5; isolated client mounts/access/root squash/changed policy/unpublication passed on 18020d4 |

The full lifecycle sequence comprises stopped definition/update, enable without start, disable without stop, current start/stop/restart, stop while enabled, a deferred runtime update followed by shutdown/start, stable VM identity where applicable, and removal retaining external data. Incus is deliberately retained stopped and boot-disabled after its sequence because destructive removal/export is deferred. Container removal and the unchanged data sentinel were verified before subsequent cases.

## Corrections found by native tests

Native verification exposed and corrected unsupported podman-compose project-directory flags, incorrect raw Quadlet scalar quoting, missing pasta/passt prerequisites, un-linkable Compose launcher source placement, implicit switching to Docker's Compose plugin, and false libvirt runtime drift from a daemon-resolved network bridge. Shared package/provider/unit-value/readiness rules cover both applicable adapters. Ordinary type/race/SDK checks and targeted native probes accompany the fixes in the [release tracker](../plans/single-server-release.md).

Failures stop the runner. Retained fixture recovery is explicit and reviewed, preserves data, and uses central apply/recovery wherever possible. One stopped Compose removal required checking/deleting only its known generated files after the wrong provider prevented native down; central recovery then verified removal without replay. No failed run is counted as an all-target pass.

The last Incus start initially failed before QEMU startup because the SPICE module was omitted by no-recommends package installation. The corrected preset was installed only in the guest, and the revised native readiness check returned zero issues. An explicit inspected-fixture continuation then verified start/disable/enable/deferred memory edit/stop/start/restart/stop/disable, stable UUID/MAC, both share protocols and unchanged server/storage/binding/private-file references. Serial login was checked before VM shutdown requests. Native inventory and final configuration health passed; the shared data sentinel remained byte-identical. No existing Incus instance or root storage was deleted. The two share cases preserved their observed unmanaged inventories; this is not a remote client test or proof of preservation across every unmanaged configuration.

All seven targets now have the selected operation evidence above across the recorded builds and reviewed continuations. There has not been one uninterrupted all-target harness pass on a fresh guest with the final build.

## Outstanding evidence

An uninterrupted final-build matrix, daemon restart, broader guest OS/application health, Debian/Arch fresh installations and broader supported profiles remain separate gates. Actual guest power cycles, including a normal-environment enabled/disabled pair after the network-ordering correction, and isolated share clients are recorded below. These do not prove reliability across all profiles or separate physical client hosts. The earlier Netavark failure's cause is not proven; a subsequent passing run is not causal proof. Archival/restore acceptance is deferred by user instruction.


## Initial cold-boot observation

Five enabled workloads remained stopped before a deliberate graceful shutdown/power-on of the disposable guest. Its boot ID changed. Raw VM process observations before provider queries proved autonomous libvirt/Incus startup, and the application/user manager/provider services were active. Four workloads reported running/matching state; Podman Compose's success-returning inactive oneshot terminated its detached container's cgroup. Its command matched the fixture exactly and exited 0. A temporary native unit proved that RemainAfterExit=yes preserves the same container after main-command completion, and explicit unit stop terminates it. The launcher correction and safe owned-unit retirement have full fixture/race/type evidence; a corrected cold boot and disabled-running boot cycle are still required. Recovery stopped/disabled only the known boot fixtures and preserved the retained Incus definition/disks and shared data.

## DNS and corrected cold-boot follow-up

On `31109c5`, both Podman adapters passed an actual two-container service-name DNS/HTTP check: the caller fetched the target's sentinel by service name and its SHA256 matched. Both test definitions were stopped and removed, preserving the original Incus resource and shared data. Compose's retained failed definition was retried explicitly after the linked-fragment correction; no automatic replay was used.

A second graceful cold power cycle used five enabled/stopped fixtures on this build. Raw observation again proved autonomous libvirt/Incus startup before provider queries. Four workloads ran, but Podman Compose failed with exit 255 and left its container created without starting. Journal process metadata identifies Netavark as the process reporting a Rust stdout `Broken pipe` panic. No kernel OOM/segfault indicators were found, and the launcher has no memory limit. The cause is unresolved; concurrency is only a hypothesis. One explicit manual start of the exact failed generated unit succeeded and left it active/exited with its container running. This does not count as autonomous startup success.

At this point the five boot fixtures remained enabled/running after that manual retry, without advancing the verification checkpoint. The following diagnostic attempt and cleanup supersede that intermediate state.

## Diagnostic enabled boot and disabled-running cycle

The five exact fixtures were stopped through shared management operations, preserving enabled policy and the central revision. A temporary owner-only environment.d file enabled Rust backtraces in the disposable account. A third graceful cold power cycle on 31109c5 changed the boot ID; raw observations before provider queries again showed both VMs running autonomously. All five application rows were running, matching and enabled without any manual start. Podman Compose's launcher was active/exited with Result=success and status 0. The earlier failure did not reproduce, so no useful backtrace was produced; this diagnostic pass does not resolve its cause or establish normal-environment reliability.

Disable then left all five workloads running with matching definitions. The temporary backtrace file and manager environment setting were removed. A further graceful cold power cycle changed boot ID; raw observations before provider queries showed neither VM running. All five application rows were stopped, matching and disabled. Thus the actual disabled-after-running policy check passed across all five backends on this guest/build.

Cleanup stopped the known Quadlet network helper, removed four temporary definitions through central plans, verified no network consumers and removed only that known unused network. The original Incus definition is again stopped/disabled, with its disks and external references retained. The shared sentinel SHA256 is unchanged. No workstation reboot or existing-workload mutation occurred.

## Samba/NFS client acceptance

The guest was upgraded through the shared installer to 18020d4 with packages skipped. Only inside that guest, smbclient was installed and a temporary Samba credential for the manager account was generated through stdin and private runtime files; the account had no Samba entry beforehand. A new network namespace used an isolated veth pair, server 198.18.31.1 and client 198.18.31.2, without a default route or workstation interface. Each client probe used a private mount namespace. Thus requests crossed a real network interface and protocol boundary, while sharing the disposable guest's kernel and client binaries; this is not a second physical host or a different client OS.

All definitions, access-policy edits and publication/removal operations used revision-pinned shared tools/jobs. Each protocol used the existing fixture storage, initially mode 0755 owned by UID/GID 1000, without changing its permissions. The client suite verified:

| Protocol | Observed client behavior |
| --- | --- |
| Samba/SMB3 | Authenticated sentinel read matched SHA256. Read-only upload was denied. Writable update allowed upload/download/delete owned by UID 1000. Wrong-password and anonymous access were denied. Restricting valid users excluded manager; fresh access failed. Restoring manager restored writable access. Unpublication denied a fresh tree connection. |
| NFSv4.2/sec=sys | Real mounts read the sentinel with matching SHA256. Read-only export denied UID 1000 writes. Writable update permitted UID 1000 create/delete, retaining that ownership, while root-squashed UID 0 writes were denied. Changing the allowed client address denied fresh mounts from 198.18.31.2. Restoring the address restored writable access. Unpublication denied a fresh mount. |

Both protocols passed readonly, writable, restricted, writable-again and unpublished cases (ten checks). Denials required specific protocol/permission errors; a generic failed command did not count. Configuration status matched publication throughout. Removal preserved observed unmanaged native share definitions and all server/service/storage/binding/private-file references. The original Incus VM remained stopped/disabled. All client-created files were removed, and the sentinel SHA256 remained unchanged.

Cleanup verified no client namespace processes or client mounts, removed the exact veth/namespace, deleted only the newly added Samba credential entry and erased private generated credential/runtime files. No workstation package, share, mount, account or network was changed. Existing Samba-session revocation, other UID mappings, Kerberos and other client kernels were not tested. See the [client test procedure](disposable-acceptance.md#isolated-share-client-follow-up) for the boundary and cases.

## Normal-environment boot with Podman network ordering

The guest was upgraded through the shared installer to eb8c4cb, which adds Quadlet-equivalent user network-online ordering to Podman Compose and shared boot prerequisites for its packaged wait helper. This correction addresses an observed definition gap; it does not establish the cause of the earlier panic. Historical journal retrieval for that earlier boot was unavailable.

Five fixtures were again defined/enabled while stopped through central plans. The temporary Rust backtrace file was absent and the manager had no RUST_BACKTRACE setting. After a graceful cold power cycle changed the boot ID, raw VM observations preceded provider queries and showed both VMs running autonomously. All five application rows were running/matching/enabled without manual retry. Podman Compose's owned unit was active/exited with success/status 0 and no unmanaged overrides. Native monotonic timestamps verified system network-online activation, then helper completion, then Compose command start, in that order.

Disable preserved all five running states and matching definitions. A further normal-environment graceful cold cycle changed boot ID; raw observations before provider queries showed neither VM running. All five application rows were then stopped/matching/disabled. Thus both selected enabled-from-stopped and disabled-after-running cases passed on eb8c4cb with normal environment, not just the earlier diagnostic build/setting. The separate share-client results remain on 18020d4; no uninterrupted all-target final-build run is claimed.

Cleanup removed the four temporary definitions through central plans, stopped the known Quadlet network helper and removed its unused network only after checking for consumers. The original Incus VM remains defined/stopped/disabled with its disks/references retained. The sentinel hash is unchanged. No workstation reboot or workload/share mutation occurred.

## Partial container-group recovery

On 2c15260, all three container adapters passed a separate two-member native case: Podman Quadlet, Podman Compose and Docker Compose. Definitions were created stopped/boot-disabled, then shared Start brought both members up. One exact member was deliberately stopped using its native unit (Quadlet) or validated engine container identity (Compose), simulating partial availability. Inventory reported partially_running, registered configuration and the warning “1 of 2 configured containers are running.” Shared Start restored both members, clearing the warning. Configuration revision and boot policy remained unchanged throughout the stop/recovery observation.

Shared Stop then left the group stopped, and reviewed central removal retained external data. The Quadlet helper was stopped and its unused known network removed after checking for consumers. All three cases preserved the sentinel hash and final central configuration exactly, including the original stopped/disabled Incus definition and its references. This verifies observed partial availability and Start recovery, not every concurrent failure mode or automatic crash-restart policy. Ordinary/browser/SDK tests for the change are recorded in the release tracker; no workstation workload was used for acceptance.
