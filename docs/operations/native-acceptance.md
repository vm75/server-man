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
| Samba | Central definition/update/publication/unpublication/removal passed on a5916d5 |
| NFS | Central definition/update/publication/unpublication/removal passed on a5916d5 |

The full lifecycle sequence comprises stopped definition/update, enable without start, disable without stop, current start/stop/restart, stop while enabled, a deferred runtime update followed by shutdown/start, stable VM identity where applicable, and removal retaining external data. Incus is deliberately retained stopped and boot-disabled after its sequence because destructive removal/export is deferred. Container removal and the unchanged data sentinel were verified before subsequent cases.

## Corrections found by native tests

Native verification exposed and corrected unsupported podman-compose project-directory flags, incorrect raw Quadlet scalar quoting, missing pasta/passt prerequisites, un-linkable Compose launcher source placement, implicit switching to Docker's Compose plugin, and false libvirt runtime drift from a daemon-resolved network bridge. Shared package/provider/unit-value/readiness rules cover both applicable adapters. Ordinary type/race/SDK checks and targeted native probes accompany the fixes in the [release tracker](../plans/single-server-release.md).

Failures stop the runner. Retained fixture recovery is explicit and reviewed, preserves data, and uses central apply/recovery wherever possible. One stopped Compose removal required checking/deleting only its known generated files after the wrong provider prevented native down; central recovery then verified removal without replay. No failed run is counted as an all-target pass.

The last Incus start initially failed before QEMU startup because the SPICE module was omitted by no-recommends package installation. The corrected preset was installed only in the guest, and the revised native readiness check returned zero issues. An explicit inspected-fixture continuation then verified start/disable/enable/deferred memory edit/stop/start/restart/stop/disable, stable UUID/MAC, both share protocols and unchanged server/storage/binding/private-file references. Serial login was checked before VM shutdown requests. Native inventory and final configuration health passed; the shared data sentinel remained byte-identical. No existing Incus instance or root storage was deleted. The two share cases preserved their observed unmanaged inventories; this is not a remote client test or proof of preservation across every unmanaged configuration.

All seven targets now have the selected operation evidence above across the recorded builds and reviewed continuations. There has not been one uninterrupted all-target harness pass on a fresh guest with the final build.

## Outstanding evidence

An uninterrupted final-build matrix, normal-environment enabled-boot reliability after the unexplained Netavark failure, daemon restart, broader guest OS/application health, SMB/NFS clients, Debian/Arch fresh installations and broader supported profiles remain separate gates. Actual guest power cycles are recorded below; these do not prove reliability across all profiles. Archival/restore acceptance is deferred by user instruction.


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
