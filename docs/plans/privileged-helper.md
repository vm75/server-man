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
- [x] Support named package installation and explicit Samba/NFS setup, alongside
  existing updates and share transactions.
- [ ] Package both executables and sudoers setup; document deployment as X.
- [ ] Verify fixtures, Go race tests, frontend checks/build and MCP compatibility;
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
