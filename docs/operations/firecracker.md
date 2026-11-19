# Firecracker microVMs (experimental backend)

Firecracker is an **experimental** third VM backend in the feature branch `feat/firecracker-microvm-20261119`. The source implementation has not passed the native disposable-KVM acceptance gates in [the implementation plan](../plans/firecracker-microvm.md). Do not treat it as production support, especially for untrusted multi-tenant guest code.

## Scope

Firecracker runs prepared Linux guests with a dedicated kernel, one to eight existing raw block images and a single TAP attached to an already configured Linux bridge. There is no UEFI/BIOS, graphical console, Docker image launch, host directory mount, automatic DHCP/NAT/port mapping, guest application installer, snapshot backup or live migration. Root images are persistent and are never deleted by removing a VM definition.

Node.js, Go and Python servers work inside a compatible guest, but the **guest** must have its own init/systemd service, network address configuration and application binaries. Server-man observes the VMM and systemd state, not whether the guest's HTTP server is healthy. A reverse proxy on the host can connect to a guest IP reachable on the configured bridge.

## Host preparation and limitations

The optional dependency target is `firecracker`:

```sh
./install.sh --user YOUR_ACCOUNT --backends firecracker --dry-run
```

Review the plan and use the normal guided install for approved changes. `--backends all` does **not** currently include experimental Firecracker. Source/release builds include `server-man-firecracker-runtime`, installed root-owned at `/usr/local/libexec/server-man-firecracker-runtime`; it is a systemd hook and has no sudo rule.

The host must have:

- Linux with KVM (`/dev/kvm`) and cgroup v2, systemd and a working `ip` command.
- A trusted matching `/usr/bin/firecracker` and `/usr/bin/jailer` pair, root-owned and not group/world writable. On Arch, the native `firecracker` package includes both binaries. On Ubuntu/Debian, the current installer only installs `iproute2`; an operator must separately obtain and verify the upstream matching release binaries from a trusted source and install them at the expected paths. Server-man does **not** download unverified binaries or auto-select an upstream release.
- An existing and persistent Linux bridge (for example `br-microvm`), set up outside server-man with DHCP/routing as appropriate. Do not attach a physical NIC to a bridge without a remote-console/recovery plan.
- One selected non-root workload account with the private `server-man.yaml`, `server.privileged_operations: true` and `server.allow_writes: true`. Kernel and raw disk images must be clean regular files **owned by that account** and accessible to the host service; Firecracker source paths may not contain symlinks.
- Root-owned helper and runtime binaries, systemd units and native metadata directories with their expected ownership. The helper alone performs TAP, jail staging, native unit and lifecycle operations.

For security, this initial backend launches separate jailed VMMs but currently uses the selected workload account UID/GID for every VMM. Separate VM-specific host identities/ACLs are not implemented; do not claim tenant-level isolation against hostile workloads. The jailer/cgroup/systemd combination and image permissions still require native review.

## Example configuration

The following is an illustration, not an instruction to reuse an existing host path without review. The kernel and root image must already exist; the root filesystem must boot with the chosen kernel and contain its own network and web service configuration.

```yaml
version: 1
storage:
  guest-kernel:
    kind: file
    path: assets/firecracker/vmlinux
  guest-root:
    kind: file
    path: data/firecracker/web-root.ext4
    format: raw
bindings:
  micro-bridge:
    kind: bridge
    name: br-microvm
services:
  web:
    name: Isolated web server
    kind: vm
    backend: firecracker
    enabled: false
    vm:
      cpus: 2
      memory_mib: 512
      network: micro-bridge
      disks: [guest-root]
      firecracker:
        kernel: guest-kernel
        boot_args: "console=ttyS0 reboot=k panic=1 root=/dev/vda rw"
```

There is one authoritative YAML file; VM identity UUID/MAC are generated and persisted when reviewed through server-man. The first raw disk is root (`/dev/vda`); additional disks are distinct raw block devices. The kernel storage reference is a separate file with **no** `format` field.

## Management semantics

- **Review / Apply:** validates typed source references and native host prerequisites, generates owned metadata and a systemd unit, and does not start a stopped VM. Missing inputs remain blocked; server-man never silently creates an empty guest root image.
- **Start:** systemd invokes the root-only hook to bind prepared kernel/disks inside the jail, create a marked TAP and attach it to the existing bridge, then launches the VMM through the jailer. Native startup checks the Firecracker API for a Running state; this does **not** prove HTTP readiness.
- **Stop:** sends a guest `SendCtrlAltDel` request and verifies the unit exited; failure does not silently force-stop.
- **Force stop:** separately acknowledged destructive action that asks systemd to terminate the unit and run owned cleanup. It may lose unwritten guest data.
- **Enable / Disable:** persistent systemd boot policy; does not start/stop current execution.
- **Edit:** active guest hardware/image changes are rejected; stop the VM, Apply, then Start. Boot policy can be changed while a guest is running.
- **Remove:** requires stopped guest, checks native ownership, retires owned TAP/jail/unit metadata while preserving kernel and disk files. Interrupted removal uses private native intent metadata for explicit retry.

Scoped logs are from the host's generated systemd unit/journal, not guest Node.js/Go/Python application logs. Native publication has independently retained root-owned intent for interrupted changes, alongside server-man's private generated-artifact and desired-config journals. Any unresolved native conflict must be inspected before manual cleanup.

## Native acceptance required

Use an explicitly acknowledged, disposable KVM-capable Ubuntu guest, not the workstation. Prepare a known compatible Linux kernel/root disk, persistent bridge and a guest HTTP service. Run source checks (`make check test mcp-test ui-test`) before touching native workloads. Then verify: stopped definition, start and HTTP response, guest graceful shutdown, force-stop after a deliberately unresponsive guest, Start/Stop/Enable/Disable independence, daemon restart, cold boot, missing data/mount, foreign TAP, hardlinked disks, run-as account permissions, failed/partial publication, interrupted removal, crash recovery and preservation of VM disk files. Repeat provider, boot and permission checks on Debian and any other distribution you claim supported.

Neither schema/command fixtures nor the presence of `/dev/kvm` prove guest boot, network reachability or stable long-running web services. Firecracker native backup/restore remains explicitly unsupported; create a separate validated data-protection plan before hosting irreplaceable data.
