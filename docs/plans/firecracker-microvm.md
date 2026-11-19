# Firecracker microVM backend: implementation plan

Status: active; feature branch feat/firecracker-microvm-20261119. Baseline: main c1a9f32f5898146ae7ac339043b910b8e2a2c438 (2026-11-19).
Scope: additive backend only; existing Ubuntu-first single-server release gates remain authoritative. No production Firecracker support claim until explicitly listed native acceptance gates pass. No changes to host workloads during fixture tests.

## 1. Goals and boundaries

Support persistent, prepared Linux microVMs as an optional third `kind: vm` backend, alongside libvirt and Incus, in the one authoritative `server-man.yaml`. The main use is long-lived or short-lived isolated Go, Node.js and Python web services; the app inside the guest is launched by the guest's own init and is not a separate server-man container. Web UI and MCP must call the same authenticated registry and coordinator functions. Preserve current model invariants: Start/Stop changes execution, Enable/Disable changes boot policy independently, Apply must never start a newly defined stopped guest, and removal must preserve attached disks.

Only supported initial guest profiles: Linux on the same architecture as the host, compatible prepared kernel, one raw writable root block image, optional additional raw block files, one TAP-backed interface attached to an existing Linux bridge, a persisted MAC address and CPU/RAM. VM names and native resources are owned, deterministic and collision-resistant. Never implicitly change an unmanaged host bridge, firewall, guest filesystem or preexisting VM.

Explicit exclusions for initial release: Windows and GUI guests, OCI image conversion, guest-image generation, automatic NAT/DHCP/port publication, host directory mounts, virtiofs, hotplug, GPU/USB, live migration, managed snapshots and restore, remote hosts, and multi-tenant per-request orchestration. Backup/restore remains deferred under the existing release policy; however Firecracker native backup coverage must be explicitly marked unresolved rather than misclassified as libvirt. No generic shell-command field or raw API JSON passthrough.

## 2. Upstream integration and versions

Use the official Firecracker REST API over a Unix domain socket with Go standard-library HTTP transport and bounded JSON. The firecracker-go-sdk repository was archived in November 2026; do not introduce it as a production dependency. Configure `machine-config`, `boot-source`, `drives`, `network-interfaces`, and `actions` only through known typed payloads; use `SendCtrlAltDel` only when supported by the installed release/guest. Prefer a verified native Firecracker+jailer package; where the distro lacks one, an operator-approved, checksummed official-release install with the matching jailer binaries is a separate installer task. Do not pin a hardcoded Firecracker version; fail closed on unsupported API behavior and document a minimum tested version after native acceptance.

A normal non-jail Firecracker launch is suitable only for a bounded development proof-of-concept and must never be silently selected as production fallback. Production requires the native jailer (or an equivalently reviewed hardening boundary), a restricted per-guest identity, cgroups/process limits, precise access to backing files, and tight trust boundaries on the Unix socket.

Upstream references:
- https://github.com/firecracker-microvm/firecracker/blob/main/docs/getting-started.md
- https://github.com/firecracker-microvm/firecracker/blob/main/docs/jailer.md
- https://github.com/firecracker-microvm/firecracker/blob/main/docs/prod-host-setup.md
- https://github.com/firecracker-microvm/firecracker/blob/main/docs/network-setup.md
- https://github.com/firecracker-microvm/firecracker/blob/main/src/firecracker/swagger/firecracker.yaml

## 3. YAML and shared model

Keep `services.<id>.kind: vm`, `backend: firecracker`, `enabled`, existing `vm.cpus`, `memory_mib`, `mac`, `uuid`, `disks` and `network`. Introduce one optional typed `vm.firecracker` mapping with required kernel storage reference and bounded kernel boot args; do not borrow libvirt firmware or Incus pool/project/image semantics. Require `vm.disks[0]` as the root, a raw format file; require each additional disk to be raw. Require a bridge binding (not Incus managed-network) and a valid MAC identity. Pin supported architecture and avoid presenting arbitrary hardware overrides. Root backing file and kernel are distinct storage references; the kernel may be a regular file without a `format`. Preserve existing `PrepareIdentities` ownership on edits and reject backend switches of existing VMs.

Example YAML accepted by the branch's typed parser (jailer execution and host boot acceptance are not yet verified):

    storage:
      guest-kernel: {kind: file, path: assets/firecracker/vmlinux}
      guest-root: {kind: file, path: data/microvm/root.ext4, format: raw}
    bindings:
      guest-bridge: {kind: bridge, name: br-microvm}
    services:
      web:
        kind: vm
        backend: firecracker
        enabled: false
        vm:
          cpus: 2
          memory_mib: 512
          network: guest-bridge
          disks: [guest-root]
          firecracker:
            kernel: guest-kernel
            boot_args: "console=ttyS0 reboot=k panic=1 root=/dev/vda rw"

The runtime config renderer must produce deterministic typed JSON, not expose arbitrary API options. Every referenced storage ID must be included in fingerprints, file preflight, backup planning and any 'changed storage' checks. File ownership and mount requirements must remain authoritative, and a missing configured image must never be replaced by an empty one.

## 4. Native definition ownership and recovery

Implement `internal/declarative/firecracker*.go` using the existing `Adapter`, `LifecycleAdapter`, `PrerequisiteAdapter`, `ResourcePrerequisiteAdapter`, and `ManagedLogAdapter` contracts. Generate private, ownership-marked Firecracker configuration and supervised systemd unit(s) under deterministic per-resource paths. A unit is persistent across server-man process restarts. Generated native bytes must use the same ownership-manifest and exact-byte drift/reconciliation system as Quadlet/Compose/libvirt/Incus.

`Validate`: validate typed schema, references, local bridge and KVM/prerequisites without side effects; render exact guest config; never run a guest. `Inspect`: return registered, active, activeMatches and matches independently; use native process/unit state rather than a stale socket as proof of health. `Apply`: stage and verify private artifacts, update owned stopped definition and boot policy, never start a previously stopped guest; `now` on an active incompatible hardware change must reject with actionable stop requirement, while `next_start` must not falsely report an active guest as reconfigured. `VerifyRemoved`: require stopped, retire only owned unit/TAP/jail artifacts, preserve root/kernel/other data. Foreign native files or a drifted ownership sidecar block destructive operations.

Review and explicitly integrate the current type switches in `artifact_recovery.go`, native backup inventory and resource-specific logs/status, rather than treating the new backend as an unknown or as libvirt.

## 5. Secure host execution and networking

Use root-owned approved helper operations with fixed validated argument schemas, not shell commands, for creating/checking/deleting a per-guest TAP, attaching it to an explicitly configured existing bridge, staging jail resources, applying the jailer configuration and cleaning up only known owned state. Verify host bridge type, ownership, path safety, native binary identity and /dev/kvm access before mutation. Unique stable TAP names must fit IFNAMSIZ and avoid other workloads; an existing foreign TAP must cause a conflict. Do not change global iptables/nftables policy, ip_forward, DHCP server, host bridge configuration or unrelated systemd units.

A hardened systemd-owned persistent supervisor controls each jailed microVM. It must preserve the VM when server-man exits, restart only under explicitly defined policy, and honor independent boot enablement. Before using systemd to stop a VM, coordinate guest shutdown and avoid automatic SIGKILL on graceful timeout; require explicit destructive acknowledgement for force stop. Handle boot race ordering, release of orphaned TAP/jail state, conflict verification, and recovery after host crash/power loss without blindly deleting unknown files or sockets. Guest must configure its own network (DHCP/static); server-man's host bridge does not imply successful HTTP reachability.

Consider VMM disk files non-shareable while writable; never attach the same writable raw root to two concurrent VMs. Jail staging must never use unsafe symlink traversals or replace source images. Use absolute known jail paths with socket length limits, restrictive file modes, per-VM identity mapping and bounded cgroup memory/pids/fd resources.

## 6. User-facing behavior

Applications: add 'Firecracker VM' type; editor includes CPU, RAM, kernel file storage ref, raw disks/root storage ID, bridge binding and boot args. Explain prepared-image requirements, lack of automatic IP/port configuration, and that the guest HTTP service must be started by guest init. Show 'VM running' separately from optional HTTP guest-health evidence. Offer lifecycle/boot controls and bounded Firecracker/systemd diagnostics; never expose arbitrary host paths or image contents. Host installation: optional Firecracker dependencies with tested package availability, selected workload-account permissions and distinct missing dependency messages. Explicitly show 'feature incomplete/unavailable' while the secure execution path is unfinished.

## 7. Implementation sequence and acceptance gates

### Phase 0 — plan and design
- [x] Inspect current single-server constraints and existing declarative adapter conventions.
- [x] Record design, scope, failure states, native permission boundaries and tests.
- [x] Create a dedicated feature branch; no PR or merge to main.

### Phase 1 — typed definition, renderer, contracts
- [x] Add typed Firecracker YAML fields, validation and provider-specific limitations.
- [x] Include kernel storage in shared reference/fingerprint/preflight logic.
- [x] Implement deterministic typed Firecracker config rendering and unit tests with no native execution.
- [x] Add bounded, isolated Unix-socket API client tests using a fake Unix server (test source committed; execution pending matching toolchain).
- [x] Reject unsupported disk/firmware/pool/project/network combinations before save.
- [ ] Execute the Go race suite on the repository's Go 1.26 toolchain, and resolve any errors.

### Phase 2 — secure supervisor and native adapter
- [x] Add allowlisted root-helper requests, strict resource validation and source unit tests (native execution unverified).
- [x] Implement jailed systemd definition, bind-mounted prepared disks, owned TAP setup, cleanup, and interrupted-publication/removal journal code (native recovery testing pending).
- [x] Connect Validate/Inspect/Apply/Action/VerifyRemoved and independent boot policy through one backend; source-level contract tests added (native acceptance pending).
- [x] Integrate local definition into owned artifact sidecar validation and reviewed reconciliation (fault-injection verification pending).
- [x] Implement separate guest-API graceful stop and explicit acknowledged force-stop; running hardware edits rejected. [ ] Verify real systemd timeout and guest power-off semantics.

### Phase 3 — UI, prerequisites and packaging
- [x] Add Firecracker VM form, Applications launcher, boot/force-stop controls and backend-specific guidance.
- [x] Add bounded scoped native journal diagnostics through the existing registry.
- [x] Add executable ownership, raw-file ownership/shape, bridge, KVM and cgroup-v2 presence checks (native permission and version compatibility acceptance pending).
- [x] Package the root-only runtime helper and add optional Firecracker dependency selection (APT installs iproute2 only; upstream Firecracker+jailer binaries still require a trusted manual install on APT targets). [ ] Verify native package and release installation on each claimed distro.
- [x] Classify Firecracker native backup as explicitly unsupported/unresolved rather than misclassifying it as libvirt.

### Phase 4 — test and rollout gates
- [ ] Execute `make check test mcp-test ui-test` with Go 1.26/Node toolchains. New source-level fixtures cover config rendering, Unix API, host input validation, and adapter lifecycle, but have not been executed in this environment.
- [ ] Explicitly authorized disposable Ubuntu x86-64 KVM test: create stopped, apply, start, guest HTTP request, stop, restart, enable/disable with no immediate state change.
- [ ] Cold boot with enabled and disabled VMs; server-man restart without guest termination; VM crash and unexpected host-power-loss recovery.
- [ ] Tests for missing kernel/disk, readonly mount, bridge loss, disk-sharing, stolen socket, foreign TAP/unit, ownership drift, stale journal, partial artifact publication, helper path escape and failed stop.
- [ ] Separate Debian native prerequisites and ARM64 tests, if claimed; Arch native remains deferred unless separately authorized.
- [ ] Record actual Firecracker/jailer/systemd version and resource/runtime measurements; update architecture, README and support matrix after verification. Do not claim native support until gates pass.

## 8. Rollback and completion

Branch may ship independently as an incomplete experimental implementation; avoid modifying main until complete acceptance. Backend unavailable must be explicit and must not block other backends. Reverting the branch does not remove VM data. A safe backend removal must stop the VM, preserve data, and verify deletion only of owned generated unit/jail/TAP artifacts.

Completion requires genuine jailed guest execution, stable boot and stop behavior, auth/MCP parity, exact-byte definition ownership, clear readiness, no regressions to libvirt/Incus/containers, and native evidence. A schema-only or direct unjailed proof-of-concept does not satisfy release acceptance.

## 9. Progress and evidence

- 2026-11-19: reviewed code on main c1a9f32 and upstream official jailer/getting-started docs; created branch. External online environment has no access to a trusted disposable KVM host, and the available container has Go 1.23 rather than repository's required toolchain. Native execution, boot and privilege tests cannot be claimed here. All subsequent completed steps should record precise checks and limitations.

- 2026-11-19 (implementation checkpoint): added the Firecracker typed YAML model and validation, deterministic jailed config renderer, bounded Unix-socket API, unit-test fixtures, kernel storage reference propagation and explicit unknown Firecracker native backup coverage. The coordinator also refuses Firecracker changes before persisting the desired YAML if the native adapter is absent, avoiding an unapplyable journal that could block unrelated workloads. This is **phase 1 code only**: no Firecracker adapter is registered, no privileged runner/systemd unit/TAP/jail is created, and no lifecycle operation is claimed. Main remains unchanged; only the feature branch contains these changes. Hosted Go/Node test execution and native acceptance remain pending.

- Verification this turn: GitHub confirmed the branch and expected changed files against its base, and the source of the Go contract tests is committed. Go vet/race tests, frontend compilation, systemd verification and native KVM tests were **not executed**: the available container does not contain the checkout and has Go 1.23 rather than the project's Go 1.26 toolchain. This source-only checkpoint is not a runnable VM backend.


## 10. Current implementation and required release gates (2026-11-19)

Source is implemented on this branch in `internal/declarative/firecracker*.go`, `internal/firecrackerhost/`, `cmd/server-man-firecracker-runtime/`, `internal/admin/helper.go`, the unified VM UI and shared installer. The adapter is registered only when `server.privileged_operations` is true; no unjailed fallback is available. Host supervisor definitions live in `/etc/systemd/system`, with private root-owned metadata in `/var/lib/server-man-firecracker`. The native Firecracker and jailer binaries are currently expected at `/usr/bin/firecracker` and `/usr/bin/jailer`. The selected workload account currently supplies the jailer's UID/GID and owns the guest disk files.

**Not yet production-accepted:** source/race/frontend tests are unrun; a disposable KVM guest has not been booted; systemd service/jailer compatibility, cold-boot persistence, shutdown behavior, outage recovery, root-helper access, guest HTTP service and host TAP data-plane tests are unverified. Separate jailer UID/GID identities per microVM are not yet implemented; sharing the workload UID is a security limitation for hostile multi-tenant workloads. Official-release binary verification/install is not yet automated on Ubuntu/Debian. Complete snapshot/backup and guest health are deferred by scope.

Do not mark the user request or original deployment goals complete solely from committed source; only a disposable native acceptance campaign can close the production gates. No real host bridge, guest disk, or service was modified during this online session. The full `make check test mcp-test ui-test` suite was not run, so even source compatibility is not yet established.
