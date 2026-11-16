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
| Incus client/server | 7.0.1 |
| Samba | 4.24.7 |
| NFS server package | 1:2.9.1-1ubuntu1 |
| passt | 0.0~git20260728.f8df3f1-1 |

Containers use a preloaded BusyBox image pinned to `sha256:73aaf090f3d85aa34ee199857f03fa3a95c8ede2ffd4cc2cdb5b94e566b11662`. VM fixtures use a prepared standalone BIOS disk and an explicit local Incus VM image/pool/network. All test resources live under the dedicated fixture namespace and data root. Package/backend preparation happened only inside this guest; no new workstation packages were installed.

## Observed lifecycle results

The official SDK runner uses the same typed revision-pinned configuration edits and job-based lifecycle operations as HTTP/UI. The [repeat procedure](disposable-acceptance.md) defines its guards and limits.

| Target | Verified result |
| --- | --- |
| Podman Quadlet | Full lifecycle sequence passed on 2088bc4 |
| Podman Compose | Full lifecycle sequence passed on 2088bc4 |
| Docker Compose | Full lifecycle sequence passed on 2088bc4 |
| libvirt | Full lifecycle sequence passed on 449e3bd, with serial boot readiness verified before shutdown |
| Incus | Stopped creation failed on an unsupported query mutation flag; retained for correction |
| Samba | Native setup passed; central publication sequence pending |
| NFS | Native setup passed; central publication sequence pending |

The full lifecycle sequence comprises stopped definition/update, enable without start, disable without stop, current start/stop/restart, stop while enabled, a deferred runtime update followed by shutdown/start, stable VM identity where applicable, and removal retaining external data. Incus is deliberately retained stopped and boot-disabled after its sequence because destructive removal/export is deferred. Container removal and the unchanged data sentinel were verified before subsequent cases.

## Corrections found by native tests

Native verification exposed and corrected unsupported podman-compose project-directory flags, incorrect raw Quadlet scalar quoting, missing pasta/passt prerequisites, un-linkable Compose launcher source placement, implicit switching to Docker's Compose plugin, and false libvirt runtime drift from a daemon-resolved network bridge. Shared package/provider/unit-value/readiness rules cover both applicable adapters. Ordinary type/race/SDK checks and targeted native probes accompany the fixes in the [release tracker](../plans/single-server-release.md).

Failures stop the runner. Retained fixture recovery is explicit and reviewed, preserves data, and uses central apply/recovery wherever possible. One stopped Compose removal required checking/deleting only its known generated files after the wrong provider prevented native down; central recovery then verified removal without replay. No failed run is counted as an all-target pass.

## Outstanding evidence

The remaining lifecycle targets, actual host reboot and daemon restart, guest OS/application health, SMB/NFS clients, Debian/Arch fresh installations and broader supported profiles remain separate gates. Do not infer them from a successful process start, autostart setting or daemon reload. Archival/restore acceptance is deferred by user instruction.
