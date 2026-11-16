# Single-server release

Status: active, 2026-11-15. This checklist supersedes the broader plan's release sequencing. Deliver a usable deployment first; preserve unfinished archival code without continuing it. DRY, KISS and YAGNI remain key tenets.

## Scope

First deployment: this Ubuntu 26.10 machine, as its workload account. Debian and Arch Linux remain support targets; neither has native acceptance evidence yet.

- One authoritative server-man YAML for application settings, managed containers, VMs and Samba/NFS shares. Native definitions are derived state.
- Podman Quadlet and Compose, Docker Compose, KVM/libvirt and Incus VMs. Configure/update, start/stop and independent boot enable/disable through the same validated web/MCP functions.
- Friendly typed editors, review before apply, useful status and alerts for missing programs/providers, permissions, files/directories/disks, mounts, pools and networks. Do not create empty data to conceal a failed prerequisite.
- Host system services and software remain direct operations outside workload YAML. Preserve rootless workload ownership and unmanaged share configuration/data.
- Existing supported VM profiles are the initial boundary: prepared existing disks for libvirt; local images and explicit initialized project/pool/network for Incus. Advanced guest profiles and destructive VM removal are not deployment prerequisites.
- Backup, archival, restore and their native acceptance are deferred. Preserve existing capture/recovery code and tests. Pending recovery journals continue to block unsafe mutations; never bypass them to enable deployment.
- Use the canonical `server-man` spelling throughout; no backward compatibility or migration layer.

## Deployment sequence

- [x] Preserve interrupted archival work separately: `c03b60e`; previous full `make check test` passed. No native archival acceptance.
- [x] Inspect target without mutation. No existing system/user server-man unit or bootstrap configuration found. Podman/libvirt executables present; Docker, Incus, Samba and NFS management executables absent. Presence does not establish working runtime access.
- [x] Simulate missing dependency installation: `apt-get --simulate install docker.io docker-compose-v2 incus samba nfs-kernel-server`. Cached repositories propose 48 new packages, zero removals/upgrades; Compose v2 package already installed. No refresh or installation performed.
- [x] Run `make check test mcp-test ui-test`, then stage `make install DESTDIR=...` and verify packaged paths/unit syntax. Fix actual deployment blockers before extending scope.
- [x] Install the verified application/helper and workload-account service with a private unified YAML configuration and loopback listener. Validate helper sudo policy and service health without exposing the token.
- [x] Obtain an explicit exception to AGENTS.md's development-host package prohibition before installing missing runtime/share dependencies. Package installation may start system daemons. Do not initialize Incus storage/network or expose shares without concrete reviewed settings.
- [x] Inspect account access to Podman user systemd, Docker socket, libvirt and Incus. Report necessary access/setup explicitly; executable presence alone is insufficient. Do not alter `~/vms` or import its workloads.
- [ ] Confirm readiness diagnostics for absent prerequisites and fix blockers affecting the core UI/configure/apply workflow.
- [ ] Perform explicitly authorized live acceptance with disposable, clearly owned resources: each backend defines stopped, updates, starts/stops and changes boot policy independently; central shares preserve unrelated native configuration and data. Record actual versions and any unavailable backend. Boot and remote share-client checks require a suitable disposable environment; do not reboot this workstation implicitly.
- [ ] Record access/run instructions and remaining limits. Consolidate completed release decisions into architecture/design/operations docs; replace this checklist with a concise completion record once deployment acceptance is achieved. Keep deferred work tracked separately.

## Completion boundary

A running web service is an application deployment, not proof that all workloads are ready. Distinguish app health, provider availability, fixture coverage, live lifecycle, boot and client checks. Do not claim Debian/Arch support verified from Ubuntu checks. Release scope excludes backup/restore; the broader declarative phase remains incomplete until its retained future requirements pass.

Deployment evidence: [Ubuntu first deployment](../operations/ubuntu-first-deployment.md). Application is installed, enabled and serving HTTP 200. Authenticated readiness reports the unified configuration valid; initial missing-file behavior is covered separately. Docker/Incus account access is verified; Incus initialization remains pending; no workload/share lifecycle acceptance is claimed. Explicit user approval allowed the helper rule; its isolated syntax check passed. Full sudoers validation found an unrelated existing file-mode issue, recorded in the operations note.

## Repeatable installation increment

- [x] Add one native setup tool shared by source `install.sh` and release archives. Explicit plan/confirmation or `--yes`, dry run, backend selection, existing-account validation and configuration preservation are implemented. Native package lists are shared with Updates; Incus installation is now available through APT/pacman dependency management too.
- [x] Package static application/helper/installer binaries, entry script, instructions and SHA256 sidecar with `make release`. Keep `make install DESTDIR=...` as copy-only packaging.
- [x] Apply setup on this host with existing packages; explicitly requested Docker/Incus-admin memberships are active in the account and service process. Workload-account system service replaces the earlier user unit without restarting the desktop manager. Docker/Incus read-only inventory and HTTP health pass.
- [x] Complete final fixture/build/SDK/browser checks, release extraction and repeat-install preservation checks; evidence and limits below.

This increment does not initialize Incus or claim native VM/container boot/share-client acceptance. No hosted release publication is requested. The broader release checklist remains active; archival stays deferred.

Installer verification: `make check test` passed after the final changes (Go vet, zero frontend diagnostics and full Go race suite). `make mcp-test ui-test` passed with the Incus dependency schema/UI addition: official SDK parity across 55 tools and Firefox desktop/mobile coverage. Focused installer tests cover rejected account/backend input, supported distro package selection/deduplication, safe leaf-symlink replacement, root-parent rejection, account unit/user bus settings and private initial configuration with preservation/invalid-existing-file refusal. `sh -n` passed for entry/build/release scripts. Source `install.sh --dry-run` completed after allowing network access for locked build dependencies; the sandbox-only attempt could not fetch an uncached npm tarball. The existing lockfiles report npm audit advisories; dependency versions were not changed in this increment.

`make release` generated the native amd64 archive/checksum; binary inspection confirms static linking. Checksum, extraction, release script dry-run/help and copy-only `make install DESTDIR=...` passed. An actual release-installer rerun here with `--skip-packages` preserved bootstrap configuration and token exactly; its root-owned unit passed systemd verification and HTTP health remained 200. Unrelated installed xfs units emitted obsolete CPUAccounting warnings during verification. Native package installation by the new tool was not executed; package mapping is covered by fixtures, and this host's earlier approved dependency installation is recorded separately. Debian/Arch fresh installs, alternate architectures, complete lifecycle/boot and remote share-client acceptance remain open. The requested installer increment is complete; the broader single-server acceptance checklist is not.

## Unified configuration increment (2026-11-15)

User requested one configuration for application settings and resources. The default is now `~/.config/server-man/server-man.yaml` (respecting XDG_CONFIG_HOME), with server settings under `server` and services/storage/shares/bindings/secrets alongside it. No JSON bootstrap, TOML or `manifestPath` selector remains. Token, jobs, journals and referenced data remain separate operational files.

- [x] Share bounded YAML structure policy across startup, resource parsing and private metadata; validate server settings with defaults and strict fields, while allowing typed resource repair through the UI.
- [x] Preserve server settings through shared typed edits, lifecycle policy saves and full-file revision/history handling. Review and readiness report pending restart. Server-only saves avoid native drift repair; mixed server/native-resource changes and further managed mutations before restart are refused.
- [x] Update installer, source/release packaging, manual units/examples, CLI and current architecture/design/user instructions for unified YAML.
- [x] Finish SDK/browser/staging checks and apply the one-file configuration to this existing empty deployment. Authenticated readiness/model checks and HTTP health passed before removing the obsolete active JSON file; release verification is recorded below.

The earlier two-file installation records above are historical and superseded by this increment. Archival remains deferred; the only change to capture entry is use of the same pending-server-restart mutation guard.

Unified-config verification: `make check test` passed with zero frontend diagnostics and the full Go race suite. Added tests cover strict/default/private YAML startup parsing, preserved server fields through typed resource edits, revision safety, restart preview/readiness/lifecycle blocking, rejection of mixed server/resource changes and absence of native drift apply during server-only saves. SDK stdio verification now starts with the unified configuration and checks its actual revision/configured state; HTTP still covers missing initial configuration. `make mcp-test ui-test` passed with 55-tool schema parity and Firefox desktop/mobile checks. `make install DESTDIR=/tmp/server-man-unified-stage` passed. Installed unit verification passed with only unrelated xfs-unit warnings. Live checks verified retained server settings, configured/valid readiness, no pending restart and HTTP 200. Release packaging/checksum and extracted-installer preservation checks are recorded with the final artifact below. Fresh native Debian/Arch, live guest/container boot/share-client acceptance and archive/restore remain outside this increment.

Final unified artifact check: native amd64 release checksum, safe extraction and extracted installer dry-run passed. The installed shared tool was rerun with existing dependencies and existing YAML preserved; no compatibility reader or general migration layer was added.

## First-use readiness follow-up (2026-11-15)

A unified configuration exists before applications are defined. Applications now distinguishes that initial empty inventory from a search with no matches and from invalid configuration. Shared readiness propagation now copies configuration-wide blockers to service/share issue lists, including the pending server restart, so application startup controls reflect the existing mutation guard. The native cache cannot retain the derived blocker after restart.

Verification: `make check test ui-test` passed (zero frontend diagnostics, full Go race suite and Firefox desktop/mobile checks). Added fixtures prove one restart blocker at configuration/service/share scopes on fresh and cached checks, inventory visibility and clearing after running settings match. Browser fixtures verify first-use guidance with Define application enabled and a distinct unmatched search. No host workload, package, share or VM state changed during verification. These checks improve the readiness gate but do not complete its full native/porting matrix. Incus pool/network selection remains a deployment question; archival stays deferred.

Deployment follow-up: `b6c33d3` is installed and included in the regenerated native release archive. An authenticated live Firefox mobile check at 390x844 confirmed the first-use guidance and enabled definition control without overflow/page errors or resource mutations; see the operations record. SDK schemas/transports are unchanged, so the SDK run was not repeated for this UI/readiness-only increment.

## Docker boot prerequisite follow-up (2026-11-15)

The shared installer now reviews and applies one host-unit list, explicitly enabling `docker.socket` before `docker.service`; libvirt/Incus retain their socket setup, while share exposure stays explicit. Docker Compose readiness checks the system socket's persistent enablement independently of current engine access. The bounded three-second probe never changes native state or echoes command output. Disabled/runtime-only/missing states and unknown probe results block boot enablement, while a reachable engine can still serve current-run Start.

Verification: `make check test` passed with zero frontend diagnostics and full Go race tests. Fixtures cover enabled, disabled, runtime-only, missing, malformed and failed probes, redacted diagnostics, fixed system scope/timeouts, propagation into startup policy and preservation of current-run readiness. Installer fixtures verify the exact selected host-unit set and that share selection adds no explicit daemon activation. Read-only native inspection here confirms docker.socket is already persistently enabled, precedes sockets.target, and the user manager follows basic.target; no reboot or daemon restart was performed. The full native lifecycle/boot matrix remains open.

Further acceptance audit found `tools/mcp/verify-vm.mjs` still calls retired direct native share-edit operations. That disposable-host harness needs conversion to the central declarative workflows before it can provide current native acceptance evidence; its earlier historical results are not current phase completion.

Docker deployment evidence: `598b55d` is installed and packaged. Installer rerun preserved the Docker PID and exact unified configuration bytes, with socket enablement, HTTP health and archive checksum verified. No package changes, native workload test, reboot or daemon restart was performed. SDK/browser checks were not repeated because schemas/transports and UI code are unchanged; full Go race/type checks cover this increment.

## Disposable acceptance harness update (2026-11-15)

The old guest verifier's direct native share writes and automatic package installation/full upgrades have been removed. The current runner requires explicit acknowledgement, private pinned SSH credentials, the exact guest identity, enabled writes, a fresh central resource inventory and all selected fixture inputs before any mutation. Seven explicit targets cover all container modes, libvirt, Incus and both share protocols. Workload checks use typed revision-pinned edits and current lifecycle/boot controls; share checks use central publication/removal and compare unmanaged native inventory. Server settings and fixture references are preserved. Incus intentionally remains stopped/boot-disabled; archival/removal export work remains deferred.

The runner bounds each job, requests cancellation at deadline and stops on failure without blind cleanup/retry. It does not provision a guest, install dependencies or change this workstation's workloads. The new operations guide records exact fixtures and limits, superseding the historical repeat instructions. Native boot/daemon-restart, guest OS, share clients, portability and distro acceptance remain separate gates.

- [x] Replace retired calls and add refusal/invariant contract tests using only in-memory scenarios.
- [x] Finish ordinary SDK/contract checks and verify the native entry refuses missing acknowledgement; evidence and limits below.
- [ ] Run the selected native matrix on a separately provisioned disposable guest, with real images and backend/share prerequisites. No such guest is currently supplied or provisioned; this gate remains open.

Harness evidence: `make mcp-test` passed after wiring the new contract suite; the final `npm --prefix tools/mcp run verify` passed all seven contracts and the official SDK stdio/HTTP, 55-tool schema parity, authentication/origin and denied-write checks. JavaScript syntax checks and the Makefile native-target dry run passed. Direct invocation without acknowledgement exited before a guest connection. No SERVER_MAN_VM_DIR credentials were supplied in this session, and no native guest run, image download, package operation, workload mutation or host service restart was performed. The in-memory full-target contract is explicitly not native backend evidence. Production Go/UI code and schemas did not change; their broader suites were not repeated beyond the SDK build.

## Libvirt delegated hardware readiness (2026-11-15)

Native environment inspection found that the sandbox hides `/dev/kvm`, while the actual host exposes it to group kvm. The workload account lacks direct KVM access but its authorized system libvirt daemon successfully reports the supported KVM profile; the session connection cannot. The previous app-account device-open check would falsely block working system-libvirt management.

The readiness check now queries the configured daemon's x86_64/q35 KVM domain capabilities with fixed arguments, locale and five-second timeout. It accepts exactly one supported domain/architecture, rejects malformed/duplicate/trailing/oversized data as unknown, and reports native failures without echoing diagnostics. No extra account group or device permission was granted.

Verification: `make check test` passed (zero frontend diagnostics, full Go race suite); final focused declarative race tests passed after strengthening the valid-resource fixture. Tests cover both connection scopes, failed connection/profile queries, malformed/wrong/duplicate/trailing/oversized data, redaction, bounded commands and startup blocking. A direct invocation of the revised read-only server-man prerequisite function outside sandbox restrictions returned zero issues for qemu:///system and the expected KVM blocker for qemu:///session. No VM was defined/started, no host device/group setting changed, and no native workload acceptance is claimed. Archival and the pending Incus pool/network choice remain unchanged.

Libvirt deployment evidence: `79859c0` is installed and included in the refreshed native archive. Exact unified configuration preservation, service running state, eventual HTTP 200 and archive checksum were verified. The first immediate HTTP request raced normal Type=simple startup; rechecking the same service passed without another restart. No new KVM permission, package operation, VM definition/start or native lifecycle acceptance was performed. UI/MCP schemas and transports did not change, so those broader checks were not repeated for this backend-only correction.

## Native guest preparation and Compose provider correction (2026-11-15)

Prepared a private isolated KVM guest under the ignored native-acceptance cache, with 2 vCPUs, 6 GiB RAM, a sparse 24 GiB overlay, no host filesystem sharing and only loopback SSH forwarding (outbound package traffic uses user-mode NAT). The official Ubuntu 26.10 release image was verified against its publisher's HTTPS SHA256 list: `9ef2243a4319e43164ab11d61e324efa0dd19ed850979d10c3a6e44feb5684c5`. Pinned guest host keys and hostname were verified before guest mutations. Native packages and libvirt/Incus test backends were prepared inside this guest, not on the workstation. No permanent KVM group grant was made; QEMU runs as UID 1000 with the temporary kvm primary group. Guest credentials/logs remain private and are not committed.

Native inspection found a real packaged-provider mismatch: podman-compose rejects `--project-directory`. Runtime commands already set an explicit cwd and all generated references are absolute, so the flag is unnecessary. Runtime and boot commands now share one argument builder without it; boot units explicitly use the same working directory. Native unit verification caught quoted WorkingDirectory being interpreted as a literal non-absolute path; the path directive now uses its own validated scalar form with literal percent escaping.

Verification: `make check test` passed (zero frontend diagnostics and full Go race suite). Fixtures check runtime/boot argument parity, explicit environment input, unchanged boot-only policy, paths containing spaces/dollars/percent signs and refusal of ambiguous control/backslash/trailing whitespace. Actual temporary definitions validate through the packaged Podman Compose provider and Docker Compose, and both boot-unit fixtures pass native systemd verification, with no host container/unit activation or resolved configuration logging. The native guest lifecycle matrix is not yet run. Preparation corrected a guest-only non-TTY Incus stdin issue and a fixture bridge name exceeding Linux's 15-character limit; these are recorded as preparation failures, not backend acceptance passes. The repeat guide now uses a valid short bridge name.

## Native verifier response correction (2026-11-15)

The first live attempt refused before resource writes because the verifier incorrectly expected `managed_config_check` to return a wrapped readiness response. The actual operation returns Health directly. The verifier and in-memory fixture now consume that shape, and the official SDK test explicitly checks the real direct Health response. `npm --prefix tools/mcp run verify` passed all seven contracts and SDK stdio/HTTP parity. Before retry, the failed pre-write log was retained and absence of an apply journal was verified. The subsequent attempt passed definition preflight for all seven selected targets and reached a real Quadlet start, which exposed a separate native renderer bug recorded below; no full native matrix pass is claimed.
