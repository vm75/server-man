# Managed virtual machines

Applications manages local KVM/libvirt and Incus VMs from central YAML. The former LXD/classic LXC inventory/actions and arbitrary libvirt XML import have been removed. Stable guest identities are authoritative; generated XML and native API settings are derived outputs.

See [libvirt](declarative.md#initial-libvirt-vm-adapter) and [Incus](declarative.md#initial-local-incus-vm-adapter) for supported inputs, data retention, lifecycle and current verification limits. Provisioning, backup/restore and live boot verification remain incomplete.
