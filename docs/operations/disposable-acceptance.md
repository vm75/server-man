# Declarative disposable-guest acceptance

The native verifier uses the same central configuration tools as the UI. It is separate from the ordinary read-only/fixture checks. The [native acceptance record](native-acceptance.md) lists the actual disposable environment, versions, selected passes and outstanding gates; a partial result is never an all-target pass. Historical helper acceptance in [the earlier verification record](privileged-helper-verification.md) used retired share operations and does not prove this workflow.

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
    name: smtest0
```

The directory must already exist, with DAC permissions for the workload account and share daemons. The libvirt fixture must be a standalone bootable raw/qcow2 x86 BIOS guest compatible with the current adapter, with working graceful ACPI shutdown, a serial login prompt after boot, and access for both the manager and QEMU accounts. The native runner waits up to three minutes for the dedicated libvirt or Incus fixture to reach serial login after start/restart before requesting further lifecycle operations; a running hypervisor does not establish that the OS can receive graceful shutdown yet. The bounded read-only console probe requires coreutils timeout, emits no console contents and disconnects before continuing. Incus uses its explicitly configured local fixture project, validated as a simple native identifier before SSH invocation. This is a fixture precondition, not application guest-health monitoring. Do not use an important disk or a host path. Native virtualization must be available inside the disposable guest (nested KVM or supported software emulation).

For Incus, initialize the named local project/pool/network and preload a VM image alias named `server-man-test`, compatible with secure boot and an 8 GiB root disk, working graceful shutdown and an accessible serial login prompt. No image download or pool/network setup is supplied by the verifier. For container cases, preload the same trusted image reference into each selected engine under the manager account (rootless Podman and local Docker), and provide that reference as `SERVER_MAN_VM_IMAGE`. It must contain `sleep`; the test uses `sleep 3600` and a read-only fixture mount. Images and VM guest OS behavior remain the fixture author's responsibility.

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

The official SDK starts the guest's server-man over pinned SSH stdio with the standard system-tool PATH (`/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`), matching the installed service/user manager; Debian's non-login SSH default omits sbin tools such as nft. This does not install missing tools or bypass prerequisite checks. For selected workloads, it checks define/update while stopped, enable without start, disable without stop, start/stop/restart and stop while enabled. Container and libvirt targets check a deferred running update applied by shutdown/start. **Incus v1 instead checks that a running hardware change is refused at review without changing YAML, then stops explicitly, applies the update while stopped and starts again.** It preserves the server section, fixture storage/bindings/private-file map and generated VM identities. Containers and libvirt definitions are removed while retaining external data. Incus remains defined, stopped and boot-disabled because its destructive removal/export workflow is deferred.

For selected share protocols, it checks central define/update/publish/unpublish/remove through revision-pinned typed operations, publication status and preservation of unmanaged native share definitions. The test does not log raw configuration, resolved Compose data or private files.

Each mutation runs as a tracked job with a 25-minute deadline. At deadline the runner requests cancellation and stops; it does not assume cancellation completed or launch replacement jobs. Any failure stops subsequent cases and retains the guest state for inspection. Do not blindly rerun over retained services/shares. Inspect jobs/journals and either provision a fresh guest or perform an explicit reviewed cleanup of only known fixture resources through central plans. Before a new attempt, verify empty central service/share inventories, no containers or unused private network left from the failed fixture, complete apply status and unchanged data sentinel. This is an operator recovery procedure, never an automatic runner fallback. A failure proven to occur entirely before the first mutation may be retried only after confirming the central service/share inventory is still empty and no apply journal was created. Stop/discard the whole disposable guest after reviewing results; no automatic resource deletion is attempted after a failed case.

## Evidence limits

A native run proves only its selected observable checks on its recorded guest/provider versions. It does not prove actual host reboot/daemon-restart policy, guest OS/application health, SMB/NFS client access, portable restore, every distribution or unrelated host settings. Those remain separate acceptance gates.

`make mcp-test` includes seven in-memory guard/workflow contract tests, five mocked VM serial-console precondition tests and the usual official SDK checks. Contracts verify refusal conditions, safe target selection, central mutation vocabulary, enable/disable and configuration preservation, and safe fixed libvirt/Incus serial console commands. They neither start a guest nor substitute for a native run. The native entry point is never executed by ordinary checks or CI automatically.

## Isolated share-client follow-up

This is a separately prepared, operator-controlled native check, not part of `make mcp-vm-test`. The [recorded run](native-acceptance.md#sambanfs-client-acceptance) uses a stopped/disabled retained Incus fixture and refuses existing central shares. Snapshot all unrelated central references and observed unmanaged native share definitions before testing; compare them after each protocol. Use only the dedicated shared fixture directory and a data sentinel whose hash is known beforehand.

Inside the disposable guest, prepare smbclient and mount.nfs, a temporary Samba credential for an existing fixture account, and an isolated veth-backed network namespace. Refuse a conflicting interface/namespace/address range. The recorded subnet is 198.18.31.0/30 with no default route. Generate the credential at runtime, submit it through stdin, and give smbclient a root-private authentication file through `-A`; never put the password in argv, output, configuration or Git. Preserve any preexisting Samba account instead of overwriting its credential. Keep all network/account preparation outside the application configuration.

Run clients in the separate network namespace and a private mount namespace (`unshare --mount --propagation private`). NFS mounts must be unmounted before the probe exits; do not mount them in the guest's global namespace or on the workstation. Use bounded subprocess deadlines/output and fixed arguments. Assert the client actually occupies different network/mount namespaces. Use a distinct reserved test filename; never overwrite an existing file or alter the fixture directory's DAC permissions to make a check pass.

For each protocol, use reviewed central typed edits and publication jobs in this sequence:

1. Define stopped/unpublished with read-only access, then publish. Read the sentinel through the native client and compare its hash; confirm client writes fail with an explicit permission/protocol denial.
2. Save writable access, then create/read/delete a temporary file as the intended UID. For Samba, also deny wrong-password and anonymous access. For NFS with root squashing, verify UID 0 cannot create a file while the intended UID can.
3. Exclude the authenticated user (Samba) or client address (NFS). Confirm fresh access/mount is denied for that reason. Restore the intended policy and repeat the writable check.
4. Unpublish and confirm fresh access/mount is denied, then remove the central share definition. Compare unrelated central references/native share definitions and the sentinel hash.

Stop immediately on failure and retain the checkpoint; do not silently loosen access rules or treat timeouts as permission denials. After a successful run, verify no client processes/mounts remain, remove only the temporary namespace/veth and newly added test credential, and erase private credential/runtime files. Record exact application/client/server versions and the shared-kernel limitation. These tests prove fresh-session behavior; they do not promise revocation of existing Samba sessions or certify other clients/authentication modes.
