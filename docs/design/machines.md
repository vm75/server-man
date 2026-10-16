# Machine management

The Virtual machines page and MCP expose LXD, classic LXC and KVM/libvirt independently. A missing backend does not disable the others. server-man runs the installed CLIs as its own account, using the same registry, write policy, jobs and cancellation as Compose. It does not install a hypervisor, initialize LXD, create networks/storage pools, or elevate privileges.

## Backend setup

| Setting | Default | Behavior |
| --- | --- | --- |
| `lxdRemote` | `local` | Explicit configured LXD client remote; instance operations do not follow the client's ambient default remote |
| `lxdProject` | `default` | Explicit LXD project for inventory/actions |
| `lxcPath` | Unset | Classic utilities use the account's default path; an absolute override is passed to every command |
| `libvirtURI` | `qemu:///system` | Supported local libvirt connections: system or `qemu:///session` |
| `libvirtXMLRoot` | `vmRoot` | Absolute root containing existing XML files allowed for domain definition |

LXD requires the `lxc` client and access to an initialized daemon, project, profiles, images and storage. The account's configured remote/image credentials remain in its native LXD client configuration. Launch uses the project's default profile and storage. See Canonical's [LXD first steps](https://documentation.ubuntu.com/lxd/default/tutorial/first_steps/) and [instance listing](https://canonical.com/lxd/docs/latest/reference/manpages/lxc/list/).

Classic LXC requires `lxc-ls` and `lxc-info` for inventory/details. Start and stop are advertised separately when `lxc-start` and `lxc-stop` exist. These tools are separate from the LXD `lxc` client; see the [classic LXC tools overview](https://linuxcontainers.org/lxc/manpages/man7/lxc.7.html). Configure container ownership, permissions and networking using the backend's normal setup.

KVM requires `virsh`, a working local libvirt/QEMU connection, suitable account permissions and host virtualization support. server-man accepts only the two local QEMU URIs above. Remote libvirt/SSH connection management is outside this version's scope. The [virsh manual](https://www.libvirt.org/manpages/virsh.html) defines the inventory, domain, autostart and XML commands used here.

## Inventory and identifiers

`machines_list` takes an optional `backend` (`lxd`, `lxc`, `kvm`), returning capabilities, context, available actions, machines and per-backend failure notices. Installed tools and runtime access are separate states. A failed query is shown as unavailable inventory, not an empty successful list.

LXD uses `list <remote>: --format json`, returning selected identity/state, architecture/location, memory usage and global IP addresses. Configuration, devices, cloud-init and other unselected fields are discarded. Classic LXC lists only `NAME,STATE` using fancy output. Libvirt uses one `domstats --raw --state --balloon --vcpu` query for all domains; optional resource fields remain absent when not reported. Memory is represented in bytes; libvirt KiB is multiplied by 1,024. Inventory is capped at 1,024 entries per backend, with the shared runner's 1 MiB capture limit and four-second command deadline.

`machines_details` takes `backend` and `name`. LXD uses an exact instance-name filter; classic LXC uses `lxc-info`; KVM uses `dominfo`. Libvirt details add UUID, persistence and autostart when reported. Only whitelisted fields are returned. No full guest XML/configuration or console credential endpoint exists.

Supported classic/libvirt names begin with a letter/digit and contain ASCII letters/digits, dot, underscore or hyphen, up to 128 characters. LXD names begin with a letter and contain letters/digits/hyphens, up to 63 characters, without a trailing hyphen. Unusual existing names remain visible in inventory with controls disabled. Libvirt tools also accept a UUID; mutations resolve details and then address the UUID. If a numeric identifier resolves a differently named domain, the action is rejected. The UI uses a UUID once details supply it.

## Actions

All actions require enabled writes and execute as serialized, cancellable jobs. A destructive action also requires `confirm: true`, with a matching browser dialog.

| Tools | Behavior | Confirmation |
| --- | --- | --- |
| `lxd_launch` | Launch image alias/fingerprint as a container or VM (`--vm`) in the configured project/remote | No |
| `lxd_start`, `lxc_start`, `kvm_start` | Start an existing machine; classic LXC starts as a daemon | No |
| `lxd_stop`, `lxd_restart` | Request stop/restart with a 60-second timeout, without force | Yes |
| `lxc_stop` | Request shutdown with `--nokill --timeout 60` | Yes |
| `lxd_delete` | Require stopped state, then delete the instance, snapshots and root storage; no force option | Yes |
| `kvm_shutdown`, `kvm_reboot` | Submit guest shutdown/reboot requests; completion of the request does not prove guest completion | Yes |
| `kvm_autostart` | Set required boolean `enabled`; pass `--disable` when false | No |
| `kvm_define` | Define/replace persistent configuration from trusted existing XML; do not start it | Yes |
| `kvm_undefine` | Require shut-off state; retain disks and NVRAM (`--keep-nvram`) | Yes |

Classic LXC's default stop can kill the guest after a timeout; server-man selects its documented [no-kill option](https://linuxcontainers.org/lxc/manpages/man1/lxc-stop.1.html). Libvirt undefine never requests storage removal, wiping, snapshot cleanup or managed-save cleanup. Existing metadata or unsupported flags cause a failure for the operator to resolve through native tools. LXD deletion permanently removes instance-owned storage; separately attached custom volumes follow LXD's own lifecycle.

The shared mutation lock covers server-man processes using the same state directory. External administrators can still change a machine between a state check and an action. Backend failures remain visible in the job output. Cancellation stops the CLI process group and does not undo changes already submitted to a daemon. Refresh inventory/details to observe eventual guest state.

## Domain XML import

`kvm_define` accepts an absolute existing XML `file` inside `libvirtXMLRoot`. It resolves symlinks, rejects escapes, nonregular files, files above 1 MiB, malformed XML, directives, multiple roots and non-KVM domain types. The domain name must use the supported format. The file is opened without following a final symlink and without blocking on a FIFO.

server-man copies the validated bytes to an owner-only temporary XML file in `stateDir`, passes it to `virsh define --validate`, then removes the snapshot on success or failure. The original is never edited. Libvirt validates the full domain schema. Device paths, disks, QEMU extensions and other XML references are trusted administrative input and are interpreted by libvirt. This is a registration operation, not an XML editor or guest provisioning wizard.

## Verification

Race-tested command fixtures exercise all lifecycle actions, explicit backend contexts, parser shapes, unavailable/partial utility sets, runtime failures, name/flag injection rejection, UUID resolution, stopped/inactive removal guards, secret-field filtering, missing resources, XML snapshot cleanup and XML size/symlink/FIFO checks. No test changes actual containers, domains, storage or host configuration. The official MCP SDK checks the expanded tools/schema parity, read-only machine inventory, invalid identifiers and denied writes across both transports.

On this development host, classic LXC utilities and `virsh` are absent. The installed LXD Snap client cannot initialize its runtime/home directories in the restricted filesystem. Live daemon access, real machine mutations and rendered browser checks remain unverified; fixture success is not evidence of privileged integration.
