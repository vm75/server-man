# Run as X with explicit privileged operations

Implementation follow-up to the main implementation plan. The main app remains
under the workload owner's account; a root-owned one-shot helper supplies the
explicit administrative operations through a narrow sudoers rule.

## Steps

- [x] Add the one-shot helper and reuse core schema/domain validation. Restrict
  operations, request size, output retention, environment, working directory,
  privileged paths and cross-process concurrency independently of caller config.
- [x] Add the sudo client and route privileged tool execution while preserving
  app write policy, jobs, error codes, audit and cancellation.
- [x] Support system and current-user services together in core, HTTP/MCP and UI.
- [x] Add native dependency installation for Podman/Compose, Docker/Compose,
  Samba, NFS, x86 QEMU/libvirt and classic LXC.
- [x] Support named package installation and explicit Samba/NFS setup, alongside
  existing updates and share transactions.
- [x] Package both executables and sudoers setup; document deployment as X.
- [x] Verify fixtures, Go race tests, frontend checks/build and MCP compatibility;
  record whether disposable-VM integration is available.

## Evidence

2026-11-13: `go test ./internal/admin ./internal/core ./cmd/server-man-admin`
passes using repository-local Go caches. Helper tests reject unknown operations,
unknown envelope fields, multiple requests, oversized input, unconfirmed writes,
unknown operation arguments and invalid service identifiers without executing a
native command. No privileged mutation was exercised. This is foundation only;
the application is not yet connected to the helper and no deployment changed.

2026-11-13 routing/scopes: focused Go race tests pass for admin, core, services,
config and updates. Frontend checks report zero diagnostics. Client fixtures
verify fixed sudo arguments with requests on stdin, root error-code/output
preservation, invalid scope rejection before invocation, and direct user-scope
execution. The helper handles termination signals to cancel native process
groups. No real sudo operation or host service mutation was attempted.

2026-11-13 domain operations: full `make check test build` passes, including
race-tested package argument fixtures for APT/DNF/Pacman/Zypper and Samba/NFS
setup tests using temporary files. Configuration-preservation tests cover
idempotence, native validation failure before publication, existing file modes
and unchanged native NFS exports. No development-host packages were refreshed or
installed. The helper routes complete setup/share transactions rather than
attempting to elevate individual filesystem writes.

2026-11-13 packaging: staged `make install DESTDIR=<temporary directory>` verifies
both executables, the user unit, configuration and sudoers example with expected
modes. `visudo -cf packaging/server-man.sudoers.example` passes. The built helper
rejects an unprivileged invocation before performing setup. Browser verification
passes 1440px/390px/320px navigation and new scope, named-package and share-setup
controls with real write-disabled handler checks.

User follow-up: add native dependency installation for Podman/Compose,
Docker/Compose, Samba, NFS, QEMU/libvirt and classic LXC. The user explicitly
authorized installing QEMU/libvirt on this development host, superseding the
usual package-fixture-only restriction for that installation. Native APT
installation completed: 54 new packages, no upgrades/removals, no recommendations;
QEMU 11.0.3 and libvirt 12.6.0 are present. Package scripts enabled libvirt's
system service/socket and default network configuration. No /dev/kvm is exposed,
so the disposable guest uses software emulation. VM integration remains pending.

2026-11-14 guest integration in progress: Ubuntu 24.04 minimal guest booted using
QEMU TCG and a temporary QCOW2 overlay under `/tmp`, with only loopback SSH
forwarding. Its unprivileged `manager` account has only the exact helper sudo
rule; unrestricted sudo is denied. The official SDK successfully exercised
start/reload/restart/stop/enable/disable in both system and manager-user scopes.
The guest exposed an existing five-second unit-file inventory timeout; native
discovery took eight seconds. Raised the individual command deadline to twelve
seconds while retaining the overall API deadline, then verified both inventories.
The standalone guest verifier is `tools/mcp/verify-vm.mjs`; it requires an explicitly
provisioned disposable guest and `SERVER_MAN_VM_DIR` with its temporary SSH key.

2026-11-14 dependency implementation: the native package selector, confirmation,
shared HTTP/MCP tool (`dependencies_install`), fixed distribution package groups,
and rejection fixtures are implemented. `make check test mcp-test ui-test` passes
with 57 tools and zero frontend diagnostics; the browser exercises the dependency
confirmation and the production write-disabled handler rejects it. APT package
installation omits optional recommendations and explicitly includes the rootless
UID/network helpers for Podman. Focused race checks also pass after increasing the
service deadline and adding the unowned-Samba-fragment preservation test. In the
disposable guest, real `packages_install` and `updates_install` have succeeded;
the real Podman dependency-group installation also passed. Share integration is
still running.

2026-11-14 completion: the entire real guest verifier passed, including Samba and
NFS setup plus share save/remove. QEMU exited after graceful guest shutdown;
all temporary guest artifacts and keys were removed. Final full Go race/vet and
frontend checks pass. Rebuilt both binaries; final official SDK verification
passes all 57 tools. The last helper boundary checks confirm cancellation of a
stalled request reader and visible output truncation without exceeding job
retention. See [the verification record](../operations/privileged-helper-verification.md)
for exact evidence, host installation effects, reproduction and limits. This
follow-up is complete; earlier unrelated TLS/provider/CI gaps remain separate.
