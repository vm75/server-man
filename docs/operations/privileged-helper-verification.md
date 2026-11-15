# Privileged helper verification — 2026-11-14

The app remains under the workload account. A separate, root-owned one-shot
helper supplies package, share and system-service operations through an exact
no-argument sudo rule. Current-user service operations stay under that user.

## Local evidence

- Go vet, frontend type checking (zero diagnostics), and the complete Go race
  suite pass. Production UI, server and helper builds pass.
- Official SDK interoperability passes with 57 tools over stdio and the
  production Streamable HTTP handler, including schema parity, authentication,
  origin rejection and disabled-write checks for the new operations.
- Firefox checks pass at 1440, 390 and 320 pixels. Tests exercise both service
  scopes, named package installation, module dependencies and share setup;
  mutations are also rejected by the actual write-disabled handler.
- Temporary staged installation verifies both executables and the sudoers
  example. `visudo -cf packaging/server-man.sudoers.example` passes. An ordinary
  user invocation of the built helper is rejected before any setup.
- Fixtures verify malformed/oversized/stalled input, cancellation, native package
  argument restrictions, preserved error codes and bounded/truncated output,
  unowned-fragment refusal, setup idempotence, file modes, config preservation
  and the existing share rollback behavior.

## Real privileged guest evidence

Used the official Ubuntu 24.04 minimal cloud image, release 20261105. Its SHA256
was verified against the publisher's HTTPS checksum list:
`406630c08b5e545fcc7209854b129d47e3af822db128c9f19336593b5a2d3fe0`.

QEMU used software emulation, a temporary 8 GiB QCOW2 overlay and cloud-init seed
under `/tmp`, and loopback-only SSH forwarding. No host filesystems were shared
with the guest. The guest's `manager` account had only the exact helper sudo
rule; unrestricted `sudo id` was denied. The official SDK connected to server-man
running as that account over SSH stdio and verified:

- Both service inventories; start, reload, restart, stop, enable and disable of
  dedicated system and user test units.
- Real repository package installation (`hello`), normal system updates and the
  Podman dependency-group installation through the helper.
- Native Samba/NFS package installation and setup, managed share creation,
  native apply/validation, inventory and removal for both protocols.

Slow emulation exposed the old five-second service inventory timeout: native
unit-file enumeration took eight seconds. Twelve-second individual command
limits retain the shared API deadline and passed both inventories afterward.

The guest then shut down gracefully. Its image, overlay, seed, temporary SSH
keys and monitor files were removed. No files beneath `~/vms` were changed.

## Host installation and limits

At the user's explicit request, installed QEMU 11.0.3 and libvirt 12.6.0 on the
Ubuntu 26.10 development host, plus the native tools needed to seed the guest.
APT installed 54 new packages without recommendations, upgrades or removals.
Native package scripts enabled libvirt services/sockets and its default network
configuration. No KVM device was exposed, so the test used TCG. A Docker/Compose
APT simulation also passed without removals/upgrades; Docker itself was not
installed by this verification.

Real mutations above were confined to the disposable guest except for that
explicitly requested host tooling installation. Other distributions retain
fixture coverage. This does not claim client SMB authentication, NFS client
mounts, Docker lifecycle, rootless Podman workloads, external TLS or hosted CI
integration. Samba passwords, firewall policy and provider permissions remain
explicit host administration tasks. The application/helper were not deployed
over the existing host application service.

## Repeating current guest checks

The native result above is historical and predates central declarative shares and the unified YAML. The verifier has since been replaced with current central resource/lifecycle operations. Follow [declarative disposable-guest acceptance](disposable-acceptance.md) for the current fixtures, explicit acknowledgement, selected backends and limitations. Do not treat the older package/system-service/share result as evidence that the current declarative native matrix has passed.
