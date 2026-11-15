# Declarative disposable-guest acceptance

The native verifier uses the same central configuration tools as the UI. It is separate from the ordinary read-only/fixture checks. No currently provisioned guest or current native pass is claimed by the repository. Historical helper acceptance in [the earlier verification record](privileged-helper-verification.md) used retired share operations and does not prove this workflow.

## Boundary and prerequisites

Provision a disposable Linux guest named **server-man-test**, using an existing `manager` workload account. Install the current release through its shared installer, select the required backends, and enable the validated helper when testing shares. Enable lingering and the account's user manager. Do not share host directories or `~/vms` into the guest. Forward only its SSH port to `127.0.0.1:22472` (or another port above 1024). Place the key and verified known_hosts file in a private directory: directory 0700, files 0600, no symlinks. Obtain/verify the SSH host key through the guest console; do not disable host checking.

The verifier requires explicit acknowledgement, checks the exact guest hostname and enabled writes, and refuses existing configured services/shares, private-file references or unrelated storage/binding IDs before mutations. Every selected target's fixture and read-only definition plan are checked before the first case. It never installs packages, upgrades the OS, initializes Incus or runs obsolete native share-edit tools. Prepare dependencies and native share setup explicitly before testing; the host installer alone does not configure Samba/NFS publication or Incus pools/networks.

Use one private `/home/manager/.config/server-man/server-man.yaml`, mode 0600. Its server section must explicitly enable writes; selected share checks require privileged operations. Prepare the resources referenced below before running. Remove references for unselected targets if those fixtures are unavailable; a default run selects all seven targets and must not silently skip one.

```yaml
version: 1
server:
  allow_writes: true
  privileged_operations: true
storage:
  server-man-test-data:
    path: /srv/server-man-test/shared
    kind: directory
    external: true
  server-man-test-libvirt-disk:
    path: /srv/server-man-test/libvirt/guest.qcow2
    kind: file
    format: qcow2
    external: true
bindings:
  server-man-test-libvirt-network:
    kind: network
    name: default
  server-man-test-incus-project:
    kind: project
    name: default
  server-man-test-incus-pool:
    kind: pool
    name: server-man-test
  server-man-test-incus-network:
    kind: network
    name: server-man-test0
```

The directory must already exist, with DAC permissions for the workload account and share daemons. The libvirt fixture must be a standalone bootable raw/qcow2 x86 BIOS guest compatible with the current adapter, with working graceful ACPI shutdown and access for both the manager and QEMU accounts. Do not use an important disk or a host path. Native virtualization must be available inside the disposable guest (nested KVM or supported software emulation).

For Incus, initialize the named local project/pool/network and preload a VM image alias named `server-man-test`, compatible with secure boot and an 8 GiB root disk. No image download or pool/network setup is supplied by the verifier. For container cases, preload the same trusted image reference into each selected engine under the manager account (rootless Podman and local Docker), and provide that reference as `SERVER_MAN_VM_IMAGE`. It must contain `sleep`; the test uses `sleep 3600` and a read-only fixture mount. Images and VM guest OS behavior remain the fixture author's responsibility.

Set up Samba/NFS explicitly with the guest helper/UI first. Samba uses the existing manager account, without putting a password into test arguments. NFS test exports accept loopback only. Native unmanaged shares may exist and their inventory definitions are checked for preservation; managed native shares must not already exist. No SMB credential or NFS client mount is performed by this verifier.

## Run

After `make setup` installs the local SDK dependency, select a trusted preloaded image reference and run:

```sh
SERVER_MAN_VM_CONFIRM=disposable \
SERVER_MAN_VM_DIR=/absolute/private/guest-directory \
SERVER_MAN_VM_IMAGE=YOUR_PRELOADED_IMAGE_REFERENCE \
make mcp-vm-test
```

`SERVER_MAN_VM_PORT` overrides 22472. `SERVER_MAN_VM_TARGETS` is a comma-separated subset of `podman-quadlet,podman-compose,docker-compose,libvirt,incus,samba,nfs`; omission selects all. Report the exact selection with any acceptance result. A subset pass does not establish omitted backend support.

The official SDK starts the guest's server-man over pinned SSH stdio. For selected workloads, it checks define/update while stopped, enable without start, disable without stop, start/stop/restart, stop while enabled and a deferred running update applied by shutdown/start. It preserves the server section, fixture storage/bindings/private-file map and generated VM identities. Containers and libvirt definitions are removed while retaining external data. Incus remains defined, stopped and boot-disabled because its destructive removal/export workflow is deferred.

For selected share protocols, it checks central define/update/publish/unpublish/remove through revision-pinned typed operations, publication status and preservation of unmanaged native share definitions. The test does not log raw configuration, resolved Compose data or private files.

Each mutation runs as a tracked job with a 25-minute deadline. At deadline the runner requests cancellation and stops; it does not assume cancellation completed or launch replacement jobs. Any failure stops subsequent cases and retains the guest state for inspection. Do not rerun over retained services/shares; inspect jobs/journals, then provision a fresh guest. Stop/discard the whole disposable guest after reviewing results; no automatic resource deletion is attempted after a failed case.

## Evidence limits

A native run proves only its selected observable checks on its recorded guest/provider versions. It does not prove actual host reboot/daemon-restart policy, guest OS/application health, SMB/NFS client access, portable restore, every distribution or unrelated host settings. Those remain separate acceptance gates.

`make mcp-test` includes seven in-memory guard/workflow contract tests plus the usual official SDK checks. Contracts verify refusal conditions, safe target selection, central mutation vocabulary and detection of broken enable/disable or configuration-preservation semantics. They neither start a guest nor substitute for a native run. The native entry point is never executed by ordinary checks or CI automatically.
