# Declarative management implementation plan

Status: implementation in progress; step 1 partial. Created: 2026-11-14.

This document is the scope and progress authority for the next server-man phase, linked from [the implementation tracker](implementation.md). All implementation checkboxes start unchecked. Commit each verified implementation step with its tracking update. Record evidence and limits, not intentions, before checking a step complete. This planning change does not authorize running mutations against development-host workloads.

## Product goal and agreed scope

Build an easy-to-maintain Linux administration application with a friendly UI. One portable server-man YAML configuration is the source of truth for managed applications, virtual machines, storage references, and file shares. UI and MCP edits update that configuration and publish the corresponding native definitions. Generated artifacts and backend configuration are derived state.

| Resource | Supported targets | Configuration ownership |
| --- | --- | --- |
| Container application, including multiple related containers | Podman Quadlet by default; Podman Compose selectable; Docker Compose only | Central configuration |
| Virtual machine | KVM/libvirt and Incus | Central configuration |
| File share | Samba and NFS | Central configuration |
| Host system service | Existing direct systemd management | Host system; excluded from managed configuration |
| Software/dependency installation and updates | Existing direct package operations | Host system; excluded from managed configuration |
| Monitoring, authentication, jobs and audit | Existing application facilities | Retain shared boundaries |

No migration, legacy configuration import, backward-compatible API, LXD backend, or classic LXC backend is required for this phase. Remove obsolete application paths when their replacements are verified. Removing code does not authorize deleting old host data or changing `~/vms`. Incus system containers, remote/cluster management, arbitrary hypervisor conversion, and a general guest configuration-management agent are outside this phase. Local Incus VMs are included alongside local libvirt VMs.

YAML is the initial format. A TOML parser is unnecessary for the agreed goal; do not maintain two user schemas. Keep a schema version and reject unsupported versions explicitly, without implementing speculative migrations. Local server bootstrap/access settings (listener, token location, privilege policy and manifest location) remain installation settings; there is one portable workload configuration, not a second editable workload database.

## Engineering tenets

- **DRY:** define domain validation, defaults, state transitions, capability descriptions and operations once. HTTP, MCP, forms and adapters must use those shared contracts. Share rendering primitives and storage checks where semantics match; do not copy policy into each backend or UI.
- **KISS:** retain one Go application, typed models, fixed native commands/API calls, explicit backend adapters and a small file-based operation journal. Prefer standard libraries and narrowly justified dependencies. No general orchestration framework or plugin system.
- **YAGNI:** implement the supported workflow and its failure handling. Avoid compatibility layers, universal YAML conversion, arbitrary native-option passthrough and unneeded host/cluster abstractions.
- Preserve authentication, write policy, destructive acknowledgements, process/output bounds and HTTP/MCP parity. Rootless Podman runs as the configured application account; never silently elevate it or change ownership to make an operation work.
- Architecture documentation describes shipped behavior. This plan describes intended behavior until each step has evidence.

## Behavioral contract

### Definitions and lifecycle

Use stable resource IDs, separate display names and explicit backend choices. A managed service is either a container application (one or more containers) or one VM. Do not flatten container and VM properties into an untyped options map.

| User operation | Required behavior |
| --- | --- |
| Define | Validate, save and register native definitions; a new workload remains stopped |
| Update | Save desired configuration and update affected native definitions; disclose recreation, shutdown/start or deferred changes |
| Start | Start now using the latest applicable definition; do not change boot enablement |
| Stop | Gracefully stop now; do not change boot enablement; show failure/timeout rather than silently force-stopping |
| Enable / Disable | Persist the boot-start boolean and update native boot behavior; do not start/stop a currently running workload |
| Restart | Explicitly restart; distinguish a guest reboot from a full shutdown/start required to load a new VM definition |
| Remove definition | Review affected dependants, stop/remove owned runtime resources as approved, preserve persistent data |
| Publish / Unpublish share | Persist publication state and apply exports/share fragments, preserving directory data and host daemon enablement |

The UI labels the startup toggle "Start at boot". For daemon-backed engines, document that native autostart can also trigger on daemon startup. Current running state is observed, not a permanent `running: true` configuration field. Restarting server-man must not restart manually stopped applications. Enabled workloads start through native startup integration even when the server-man web process is unavailable.

Keep crash-restart behavior separate from boot enablement. Hard dependencies cannot silently start a disabled dependency at boot: reject inconsistent boot dependency graphs or require the user to enable the dependency. Validate dependency cycles and distinguish ordering, readiness and successful one-shot completion. Start/stop of a multi-container application must expose partial failure and have defined dependency ordering.

### Save, publish and recovery

1. Acquire the existing management lock; require the editor's current configuration revision and revalidate it inside the job.
2. Parse/validate the candidate and all references. Compute affected resources and their dependency closure. Perform bounded preflight checks and render candidate artifacts.
3. Validate native candidates without activating resources. Return a redacted impact plan identifying changes that need restart/recreation, resource removal or prerequisites.
4. On an accepted save/apply operation, durably journal the intended transition and atomically replace/sync the central file. Publication and activation follow in ordered, resumable stages.
5. Publish only server-man-owned artifacts, apply affected definitions, and verify observed results. Do not restart unrelated resources or start stopped resources as a side effect of editing.
6. Retain desired configuration on apply failure. Report per-resource failed/pending status; permit retry or explicit revert to an earlier revision. Record best-effort runtime recovery separately from success.

Support "Save for next start" for settings that cannot be applied safely while running; publish persistent definitions when the backend permits, otherwise retain an explicit pending update. Ordinary invalid edits fail before replacing the good file. A structurally valid imported/restored config with missing host prerequisites remains inspectable/editable but is blocked from unsafe activation. Do not confuse a saved definition, a registered definition, and a running instance using that definition.

There is no global transaction across native daemons. Store bounded private configuration history, resource fingerprints and a durable operation journal, with no secret values in audit or job output. Track desired, registered and active revision/status when relevant. On process restart, inspect incomplete work and backend state before retrying; do not replay destructive actions blindly. Never reconstruct user intent from runtime configuration or remove daemon-generated volatile fields/identities while updating owned fields.

### Configuration, storage and portability

The typed model should cover:

- Version, container defaults, service definitions, storage resources, network bindings, secret references and share definitions.
- Containers: image/digest, command argument arrays, ports, nonsecret environment, secret/env-file references, storage mounts, resource limits, supported network options, health checks, dependencies and bounded restart behavior.
- VMs: backend, stable identity, initial image/existing-disk source, CPU/memory, disk resources, firmware/boot settings, network bindings, optional initial guest configuration and boot enablement. Creation-only fields must not reinitialize a guest during ordinary edits.
- Storage: relative bundle paths for owned directory/file data; explicit external paths or backend volumes with declared portability limits. Stable storage IDs allow both applications and shares to reference a directory. Permission/ownership requirements are explicit.
- Shares: storage reference, protocol, publication state and supported access controls. Samba users/groups and credentials, NFS identities/Kerberos and host directory permissions remain prerequisites, not implicitly managed accounts.
- Logical host bindings for required bridges, libvirt networks, Incus pools/projects/networks and external mounts. Store explicit bindings in the config and expose a restore review form; avoid ambient client defaults and accidental dependence on mutable default profiles.

Resolve relative paths against the manifest location, not the process working directory. Generated absolute paths are host-local outputs rebuilt after relocation. Persist stable VM identities and generated MAC addresses in authoritative configuration when needed. Separate portable secrets/assets from disposable generated artifacts; referenced secrets must be backed up securely, excluded from routine APIs/diffs/logs and protected on disk. Do not put secret values into sample configs or fixtures.

A conceptual bundle contains `server-man.yaml`, `data/`, `secrets/`, `assets/`, and prepared VM/volume exports. Generated files and operation caches can be regenerated and are not required backup inputs. Preserve filesystem metadata and account for rootless UID/GID mappings, ACLs, SELinux, sparse disks, backing chains, NVRAM and TPM state. Copying a live database or VM disk is not a consistent backup.

For Incus, use native portable instance export/import plus separate custom-volume exports. Do not promise portable backups by copying internal daemon storage. Avoid storage-driver-specific optimized exports by default. Native export metadata assists restoration, but server-man config remains authoritative for managed settings after import. Detect conflicts rather than silently overwriting existing instances.

### Porting checks and user alerts (required)

Run read-only preflight when loading a configuration on a host, after relocation/restore, before apply/start/enable, and on explicit "Check configuration". Refresh bounded cached checks after relevant config/system changes; show last-checked time and unknown/stale results. Avoid expensive probes on every UI poll.

Report structured issues with severity, resource ID/name, configuration field, observed cause, affected dependants and an actionable remedy. Distinguish errors that block startup, warnings, checks that could not run, and checks requiring a live start. Never claim readiness merely because a command/file exists.

Check at least:

- Missing executables, supported versions/flags, Compose provider behavior, Quadlet generator/cgroup v2/systemd user manager, daemon/socket connectivity, permissions and required native host services.
- KVM availability, compatible architecture/firmware/machine type, libvirt connection and Incus project/storage/network availability; resource capacity and relevant access rights.
- Missing/inaccessible data directories, VM disks and backing files, secret/env files, build contexts and guest assets; wrong file type, symlink escape, mount availability, read-only filesystems and required ownership/mappings.
- Missing network/bridge/pool bindings, conflicting managed names/ports, invalid dependencies, unavailable pinned images or credentials where a bounded check is possible, and unsupported target features.
- Samba/NFS tooling/setup, access identities, paths and supported export options. Do not claim remote client access solely from a successful reload/export-table query.

The UI must show a prominent configuration-health alert after porting, per-service/share blockers and linked repair actions. Missing dependencies link to explicit direct package/setup operations; saves do not install packages. Missing persistent data blocks startup: never silently create an empty replacement directory/disk. New-resource directory/disk creation is a separate reviewed provisioning operation. A mountpoint directory existing without its expected filesystem mounted must not count as restored data.

Treat this as a required post-port workflow, including a manually copied bundle: loading the configuration must automatically schedule validation without relying on the user to attempt Start or discover a separate check button. While checks are pending, show "Readiness not checked" rather than a healthy state. Surface the resulting alert in the application UI and affected resource views; expose the same issue records through MCP. Keep unresolved blockers visible across navigation and application restarts until a fresh check confirms resolution. Report checks that cannot complete as unknown, with the reason and a retry action.

Each blocking alert must identify what is missing or misconfigured, which services cannot start, and the next repair step. For example: "Application media cannot start: storage media-data resolves to /srv/media, but the directory is missing. Restore the data or update this storage reference, then check again." Missing tools must name the required backend dependency; inaccessible files must distinguish missing paths from permission failures. Show paths only under the existing private-configuration access policy and never include secret file contents. A summary count must link to the complete issue list rather than hide additional blockers behind the first error.

Users can inspect/edit broken configurations and use host-management screens while workloads are blocked. Invalid YAML must produce a useful location/error and a recovery view, not an application crash; errors must not echo secrets. Start rechecks transient prerequisites. Actual launch/health failures remain visible even after preflight passes. Do not automatically start services simply because dependencies have been repaired.

## Intended component boundaries

Exact package names may follow existing conventions, but responsibilities must remain clear:

| Area | Responsibility |
| --- | --- |
| Configuration model/store | Strict YAML, typed defaults/validation, revisions, atomic persistence and bounded history |
| Managed resource coordinator | Preflight, affected-resource plan, lifecycle semantics, apply journal, recovery and status |
| Backend adapters | Capability probes, deterministic rendering/native configuration, scoped publish/apply, observe/start/stop and backup/restore primitives |
| Shared storage/path policy | Reference resolution, containment, permissions, ownership requirements and portability diagnostics |
| Existing core | Registry, transport-independent schemas, authentication/write policy, bounded execution, locks/jobs/audit |
| Existing privileged helper | Narrow validated root operations for owned files and native management; no arbitrary command/file-write interface |
| Svelte UI and MCP | The same resource operations and issue/impact/status contracts |

Reuse current Compose validation/execution, machine lifecycle commands, share generators and transactional publication, and native service/log helpers. Replace fragment-owned share metadata as the source of definitions. Root-owned publication must be revalidated by the helper, including paths and allowed operations, rather than trusting a user-writable generated script. Keep native host services/packages on their existing direct management path. Managed generated units shown there must link to their application and route edits through its lifecycle functions so direct controls cannot contradict central enablement.

## Ordered implementation steps

### 1. Model, schema and configuration store

- [ ] Define typed service/container/VM/storage/share/binding models, strict schema version and documented defaults. Reject unknown/duplicate fields, unsupported YAML constructs and oversized inputs; choose a small maintained parser with a recorded rationale.
- [ ] Add private atomic revision-checked storage, bounded history, safe recovery reads and deterministic serialization. Document that UI saves normalize YAML formatting rather than promise comment-preserving round trips.
- [ ] Persist stable generated identities; reject ID/path collisions, dangling references, invalid backend combinations and dependency cycles.
- [ ] Add redacted read/validate/edit operations through core; preserve existing secret-read/write policy and protect config data independently of public inventory.

Acceptance: fixture configs round-trip deterministically; invalid input and stale writes cannot replace valid config; loading from a different directory resolves paths correctly; no backend command runs during parsing. No TOML or legacy importer is introduced.

### 2. Shared storage policy and portability preflight

Depends on step 1.

- [ ] Implement declared-storage resolution and reuse it across containers, VMs, shares and backup inventory; replace the per-project-only mount assumption.
- [ ] Add explicit new-directory/new-disk provisioning plans with private defaults and ownership validation; refuse accidental creation during restoration.
- [ ] Implement structured capability, file/mount, permissions, binding, dependency and startup-readiness checks described above, with bounded probes and per-backend unavailable/unknown states.
- [ ] Add health inventory and recheck operations to the shared registry; distinguish static validity from host readiness and actual runtime health.

Acceptance: porting fixtures expose every missing dependency/file/folder and dependent workload, including absent mounted filesystems with existing mountpoint directories. A manually relocated configuration triggers checks and a visible alert without a Start attempt. Pending/unknown checks cannot appear healthy; unresolved alerts survive navigation/restart, and a successful recheck clears only resolved issues without starting workloads. Symlink/traversal and wrong-type paths fail safely; hidden secret content never enters errors. Healthy resources remain inspectable when others fail.

### 3. Apply coordinator, journal and privilege boundary

Depends on steps 1–2.

- [ ] Add redacted impact plans, expected-revision checking, affected-resource ordering and typed apply modes (now versus next start).
- [ ] Implement journal stages, desired/registered/active status, private owned-artifact manifests, deterministic no-op detection and narrow removal of stale owned outputs.
- [ ] Reuse the management lock and jobs; recover interrupted save/publish/apply stages without replaying destructive actions or restarting manually stopped services.
- [ ] Extend helper operations only for necessary owned publication/native operations. Revalidate identities/paths and avoid nested lock deadlocks between coordinator and helper.
- [ ] Add retry/revert operations, bounded independent cleanup, redacted diagnostics and explicit partial-failure reporting.

Acceptance: fault injection before/after each durable stage proves configuration survival and honest recovery status. Concurrent HTTP/MCP edits conflict safely. Cancellation, disk-full, denied privileges and backend failures cannot report false success or delete data. Repeated apply of an unchanged healthy resource does not restart it.

### 4. Podman Quadlet adapter (default)

Depends on steps 1–3.

- [ ] Detect installed generator capabilities and supported keys; generate owned `.container`/`.network` and only necessary auxiliary units. Do not turn every application into a shared-network pod automatically.
- [ ] Publish into the correct rootless search path for the application account; implement daemon reload, application membership, ordered lifecycle, logs and native observed state. Rootful support, if exposed, requires a separate explicit validated scope.
- [ ] Implement enable/disable using source `[Install]`/dependency changes, not ordinary enablement of generated services. Detect missing user manager/lingering as startup prerequisites.
- [ ] Implement health/readiness, restart semantics, grouped stop and dependency checks; reject unsupported mappings before save/apply.

Acceptance: isolated generator dry-run/native validation passes; unit fixtures cover quoting and hostile values. Disposable-host tests prove boot startup without the web process, disable-without-stop, stop-without-disable, multi-container partial failure and updated definitions. No shell interpolation of user input or unvalidated systemd directives.

### 5. Docker and Podman Compose adapter

Depends on steps 1–3; shares container model and policy with step 4.

- [ ] Render Compose directly from the common model for both engines; keep provider detection and capability checks explicit. Give projects stable identities and deterministic path/environment handling independent of server ambient variables.
- [ ] Define one startup/restart owner. Implement generated native startup integration for Compose with the accepted enable/start separation; do not allow engine boot-restart policy to bypass disabled startup or run competing supervisors.
- [ ] Establish a tested restart-policy matrix. A minimal implementation can use boot-only systemd activation with no competing runtime supervision and a constrained supported engine policy; advertise only semantics proven for both providers. Resolve this before marking the adapter complete.
- [ ] Reconcile running applications with reviewed recreation; update/create stopped definitions without launching them. Resolve health, dependency and command-escaping differences explicitly.
- [ ] Expose Podman Quadlet/Compose selection and Docker Compose-only validation. Reject changing backend on an existing provisioned workload in v1; require an explicit new definition rather than implicit data migration.

Acceptance: generated Compose validates using each supported provider; fixture and disposable-host tests cover start/stop, boot/daemon restart behavior, enable/disable while running/stopped, provider absence, data retention and environment privacy. The same portable container fixture works through all three container targets within advertised capabilities.

### 6. KVM/libvirt VM adapter

Depends on steps 1–3.

- [ ] Generate validated domain XML from typed definitions with stable UUID/MAC and explicit CPU/memory/firmware/network/disk bindings.
- [ ] Add create-without-start provisioning for an existing disk or a supported verified image source; allocate disk assets only as an explicit creation action. Support bounded image operations and reject overwrite, missing backing-chain or incompatible image cases.
- [ ] Reuse UUID-addressed lifecycle commands and native autostart. Save persistent definitions separately from live state; accurately identify changes requiring full shutdown/start.
- [ ] Keep guest initialization creation-specific; preserve disks, NVRAM/TPM and backing chains on edits/removal. Deleting persistent data is a separate explicit operation, not required by definition removal.

Acceptance: XML/schema fixtures and disposable guests cover creation, updates, identity stability, start/stop and boot enablement. Missing KVM/network/firmware/disks produce actionable preflight errors. An ordinary edit cannot recreate a disk or guest identity; reboot is not falsely reported as applying shutdown/start-only changes.

### 7. Incus VM adapter

Depends on steps 1–3.

- [ ] Detect the `incus` client/local daemon and API capabilities; explicitly select project, network, pool and managed profile/device settings.
- [ ] Generate/apply native instance configuration using fixed CLI arguments/stdin or a narrow native client. Create via init-with-VM semantics rather than launch so definition does not imply start.
- [ ] Implement typed CPU/memory/disk/network updates, capability-aware live versus deferred changes, start/stop/logs and explicit boolean `boot.autostart`.
- [ ] Preserve volatile backend metadata and guest data; prevent ambient profile edits from silently becoming the managed specification. Avoid reinitializing images on update.
- [ ] Remove LXD/classic LXC implementation/UI/tool exposure once Incus coverage is verified; do not provide compatibility aliases or mutate native old workloads.

Acceptance: command/API fixtures and a disposable Incus VM prove stopped creation, persistent settings, boot enablement, updates and failure reporting. Missing daemon, permissions, project/pool/network/image produce specific blocked status. Incorrect platform capability cannot be presented as supported.

### 8. Central-config Samba/NFS shares

Depends on steps 1–3.

- [ ] Move authoritative share definitions into the YAML model; adapt existing render/validate/apply/recovery functions to accept typed definitions from the coordinator.
- [ ] Resolve storage references and render owned native fragments. Preserve unmanaged host configuration; remove dependence on typed metadata comments as editable authority.
- [ ] Implement publication toggles and supported access controls; retain targeted export changes, Samba validation and explicit existing-session limitations.
- [ ] Keep package install, daemon enable/start and account/permission setup as explicit host operations. Block publication with actionable diagnostics until prerequisites are satisfied.

Acceptance: create/edit/unpublish/reapply uses the central revision and shared paths. Fixtures prove unmanaged config preservation, validation rollback, cancellation and no data deletion. Disposable client tests verify Samba access and NFS mounts, including permission denial and changed access behavior.

### 9. Friendly UI and MCP workflows

Build incrementally with steps 1–8; complete after all adapters exist.

- [ ] Provide an Applications view for container applications and VMs with search/filter, backend badges, running state, startup policy, readiness and configuration-application status. Keep Host services and Software clearly separate.
- [ ] Add guided create/edit forms, container grouping, VM source/resource/network selection, storage selection and share forms. Common defaults hide unnecessary details; backend-specific fields appear only when relevant.
- [ ] Show an impact review for disruptive changes, Save/apply and Save for next start, graceful lifecycle controls, job progress, retries and explicit revert. Do not label partial application as success.
- [ ] Add a prominent configuration-health banner and resource issue lists after porting; provide recheck, locate/rebind missing paths and links to direct dependency installation/setup. Support usable malformed-config recovery.
- [ ] Expose identical typed operations/status/diagnostics through MCP with proper read/write/destructive annotations. Do not retain raw native-file editors as a second source of truth.
- [ ] Ensure managed units visible on Host services route management back through the application coordinator; retain direct controls for ordinary host services.

Acceptance: keyboard-accessible desktop/mobile flows create/update/start/stop/enable/disable every supported resource type. HTTP and MCP enforce identical validation and write policy. Fixtures cover stale edits, missing dependencies/data, partial running groups, restart-required updates, denied privileges, inaccessible backends and recovery after refresh.

### 10. Portable backup preparation and restore

Depends on steps 1–9.

- [ ] Inventory all required data/assets/secrets and external requirements from config; label exclusions and unresolved prerequisites. Preserve the config revision associated with the snapshot.
- [ ] Implement a manual "Prepare backup" workflow: stop/quiesce selected workloads or use supported native consistency primitives, collect libvirt guest state and Incus instance/custom-volume exports, write a private checksummed manifest and publish completion only after all parts succeed.
- [ ] Stream large data through bounded processes rather than core output capture; handle sparse files, free-space checks and cancellation. Define bounded configurable backup deadlines instead of silently inheriting an inadequate 30-minute management timeout.
- [ ] Restore to a chosen root with archive/path validation, identity/name conflict checks, metadata/UID mapping review and required host binding changes. Import backend data before applying the authoritative definitions; never overwrite existing guests/data silently.
- [ ] Run the complete portability preflight on restore. Display blockers and leave workloads stopped until explicit start; restoring enabled policy must not cause premature boot/daemon activation before data and bindings are ready.
- [ ] After backup preparation, restore only workloads that were running beforehand, with explicit handling when resume fails. Failed/incomplete backups cannot be presented as restorable complete bundles.

Acceptance: a bundle restores on a clean disposable host under a different directory and, where supported, account mapping. Container data, libvirt guest state, Incus custom volumes and share data are verified by checksums and application/client behavior. Remove each prerequisite in turn and verify the UI/MCP identifies the blocked resource without creating empty data. Live-copy-only tests do not establish consistency.

### 11. Integration, documentation and plan cleanup

Depends on steps 1–10.

- [ ] Remove obsolete discovery/import/edit paths, LXD/classic LXC tools, `.disabled` marker semantics and stale packaging/examples after replacement coverage passes. Preserve unrelated host-service/package/monitoring capabilities.
- [ ] Run the repository checks and end-to-end acceptance matrix below. Record exact environments, backend/provider versions, evidence and unresolved limits.
- [ ] Update README, installation/config examples, component design docs, extension guide, MCP docs and operations/verification docs where behavior changed. Document prerequisites and the supported feature/version matrix.
- [ ] Update `ARCHITECTURE.md` to describe the actual configuration authority, coordinator, adapters, storage/preflight, journal/recovery, native boot ownership, privilege boundaries and host-direct management split. Include DRY, KISS and YAGNI as design tenets; remove superseded descriptions.
- [ ] Consolidate durable design/operations decisions from this plan into their appropriate documents. Replace this detailed plan with a short completed record linking to architecture/design and verification evidence; retain actual unresolved limitations. Consolidate the parent implementation tracker so it no longer gives contradictory active instructions or duplicates old evidence logs. Git history retains the original plan.
- [ ] Audit every scope row and acceptance criterion before declaring completion. Do not remove open work merely to tidy the planning document.

Acceptance: architecture matches code, commands/examples match the delivered UI/MCP, the plan is no longer an alternative architecture reference, and verification limits remain discoverable. No feature is marked complete based only on generated text or mocked lifecycle commands.

## Verification matrix and completion gates

| Area | Required evidence |
| --- | --- |
| Configuration authority | Revision conflicts, atomic save, validation failures, deterministic output, no native-file reverse import |
| Lifecycle | Every target: define stopped, update stopped/running, start, graceful stop, enable without start, disable without stop, stop while enabled then native boot, disabled while running then native boot |
| Apply/recovery | Failures at durable boundaries, concurrent processes, cancellation, interrupted jobs, retry/no-op, stale native files, partial-resource success and no unrelated restart |
| Porting alerts | Missing executables/provider/daemon, missing folders/files/secrets/disks, unavailable mount/network/pool, permissions/UID mapping, architecture/firmware issues; UI and MCP parity, dependent-resource propagation |
| Shared storage/shares | Correct shared reference resolution; no path escape/data deletion; Samba client access and NFS mounts on disposable hosts |
| Backup/restore | Consistent data, complete dependencies, relocated roots/account mappings, Incus custom volumes, libvirt guest state, interrupted backup refusal and stopped restore |
| Security/bounds | Authentication/write policy, redaction, helper revalidation, config/archive/systemd injection, traversal/symlink/FIFO handling, bounded processes/output/probes/history |
| UI/MCP | Desktop/mobile/keyboard flows, same domain validation, useful error messages and honest observed/pending states |
| Host-direct features | System service/package changes do not edit workload config; managed-unit controls cannot bypass central policy |

Run focused tests while implementing, then `make check`, `make test`, `make build`, `make mcp-test`, and `make ui-test` for release verification. Native syntax checks use temporary fixtures: Compose validation, Quadlet generator dry-run/systemd verification, domain XML validation, Samba testparm, and scoped NFS/Incus command fixtures. Use disposable hosts/VMs for real startup, privileged apply, boot and restore tests; never install packages or start/stop real development-host workloads for verification. Record fixture evidence separately from live integration evidence. Missing infrastructure is an explicit open gate, not a passing integration result.

The full phase is complete only when all selected backends and shares pass their lifecycle and portability acceptance, user-facing blockers are verified, direct host management still works, and documentation cleanup is finished. A container-only implementation or output-only renderer is not completion of this plan.

## Evidence and progress

| Step | Status | Evidence | Remaining limits |
| --- | --- | --- | --- |
| Planning | Prepared | Repository boundaries and feasibility decisions reviewed; detailed plan and project tenets documented | No implementation, native mutations or runtime acceptance tests performed |
| 1 | In progress | Initial `internal/declarative` typed model, strict YAML parsing, deterministic serialization and private atomic revision-checked store. `GOCACHE=$PWD/.cache/go-build go test -race ./internal/declarative` passes. | Stable VM identity generation/preservation, bounded private history and additional native-field validation now have race-tested coverage. Read/validate/health operations now use the shared registry; malformed YAML can be privately inspected and revision-repaired, preserving its exact bytes in history. Complete schema coverage, coordinated save/apply, location diagnostics and final step acceptance remain open. |
| 2 | In progress | Static file preflight reports missing/wrong-type paths, private-file modes and portable symlink escapes, propagates blockers to services/shares, never creates data, and fails closed for unverified expected mounts. Race tests pass. | Initial Quadlet native prerequisite checks, DAC access, expected mount-source checks and cached shared status/recheck operations are implemented. Complete backend/binding/account/image checks, provisioning and final step acceptance remain open. |
| 3 | In progress | Coordinator, private durable journal, dependency-ordered impact plans, drift inspection, shared preview/save/status/retry tools, conservative interrupted-removal handling and failure fixtures added. | Native adapters, complete fault injection, privilege publication, full registered/active revision tracking and complete recovery fault coverage remain open. |
| 4 | In progress | Initial Quadlet renderer, owned publication, native generator validation, source/active fingerprints, user-scope apply/lifecycle/boot-policy controls and fixtures. | Health/readiness/schema expansion, runtime prerequisites, network/drop-in cleanup, publication recovery, real grouped lifecycle and boot tests remain open. |
| 5 | In progress | Shared Compose renderer/adapters for Docker and Podman, boot-only systemd launchers, lifecycle commands, active/source revision checks, ownership validation and fixtures. | Full provider/restart matrix, native syntax/lifecycle/boot verification, complete recovery and remaining container schema features are open. |
| 6 | In progress | Initial ownership-marked libvirt XML generation, native schema checks, existing-disk registration, autostart, active/persistent comparisons and graceful lifecycle fixtures. | Image provisioning, UEFI, full host/network/backing-chain access checks, boot dependencies, backup guest-state handling and real lifecycle/boot verification remain open. |
| 7 | In progress | Initial local Incus VM adapter, explicit bindings/no profiles, stopped creation, volatile-preserving updates, native autostart and graceful state operations with fixtures. | Live integration, complete capabilities/runtime tracking, custom volumes, retention/removal/export, boot dependency handling remain open. |
| 8 | In progress | Central share projection, native protocol-set validation/replacement, shared rollback engine, helper-only root operations, publication/status tools and fixtures. | Central share create/edit/review forms now use the shared typed editor. Complete setup/identity/credential/permission diagnostics, drift repair and real client integration remain open. |
| 9 | In progress | Initial Applications status/lifecycle page with actionable health issues, native timestamps, explicit checks and disabled blocked-start controls. | Typed revision-checked edit operations and shared Applications/Configuration/File shares forms plus YAML recovery are implemented. Remaining schema features, details/logs, provisioning and final workflow gates remain open. |
| 10–11 | Not started | None | Backup/restore, release integration, cleanup and final acceptance remain open |

## Reference material

Use installed capability checks and verify upstream behavior during implementation; documentation for a newer release does not establish support on a target host.

- [Podman Quadlet](https://docs.podman.io/en/latest/markdown/podman-systemd.unit.5.html) and [Compose provider delegation](https://docs.podman.io/en/latest/markdown/podman-compose.1.html).
- [Docker restart policies](https://docs.docker.com/engine/containers/start-containers-automatically/) and [Compose service semantics](https://docs.docker.com/reference/compose-file/services/).
- [Libvirt domain format](https://libvirt.org/formatdomain.html), [virsh](https://libvirt.org/manpages/virsh.html) and [backup guidance](https://libvirt.org/kbase/live_full_disk_backup.html).
- [Incus instance creation](https://linuxcontainers.org/incus/docs/main/howto/instances_create/), [configuration](https://linuxcontainers.org/incus/docs/main/howto/instances_configure/), [boot options](https://linuxcontainers.org/incus/docs/main/reference/instance_options/) and [backup](https://linuxcontainers.org/incus/docs/main/howto/instances_backup/).
- Existing repository foundations: [architecture](../../ARCHITECTURE.md), [Compose](../design/compose.md), [machines](../design/machines.md), [shares](../design/shares.md), and [privileged helper plan](privileged-helper.md). These describe the current/earlier implementation until updated alongside verified steps.

### Step 1 implementation notes

The initial parser uses `go.yaml.in/yaml/v3` v3.0.4, a maintained YAML implementation under the YAML organization. One parser supplies strict typed field decoding and a node pass for duplicate keys, aliases/anchors and explicit tags. YAML errors are sanitized to avoid echoing scalar values; precise location reporting is implemented for rejected node constructs and remains to be expanded for syntax/type errors. No host/backend command runs during parse/store tests. Store writers currently require the shared management lock; coordinator integration remains open.

Continuation evidence: `GOCACHE=$PWD/.cache/go-build go test -race ./internal/config ./internal/core ./internal/declarative` and focused Go vet pass. New fixtures verify identity preservation across omitted editor fields, refusal of identity changes, bounded eight-entry history, native-field rejection and relocated filesystem blockers without data creation. No daemon or host workload mutations were performed. Static preflight does not establish runtime readiness; unverified expected mounts remain blocking issues until native mount checks are added.

Registry continuation evidence: `make check test` passes (zero frontend diagnostics and full Go race suite). `make mcp-test` passes production build, official SDK stdio and Streamable HTTP, 60-tool schema parity and added declarative validation/health/private-read-policy assertions. Inspection registration never writes workload files at startup. `manifestPath` is an optional absolute bootstrap setting. Architecture and design docs describe the implemented foundation; all native apply/backend/UI completion gates remain open.

Coordinator evidence: `make check test` passes; official SDK `make mcp-test` passes stdio/HTTP and 64-tool parity with coordinator discovery/status/denied-write assertions. Additional focused race tests cover partial application without repeated successful resources, interrupted-removal refusal and journal symlink rejection. Candidate validation precedes publication; failed apply retains desired YAML and per-resource failure state. No real backend adapters are wired yet and no host workloads were changed. Step 3 remains partial.

Quadlet continuation evidence: `make check test mcp-test` passes with zero frontend diagnostics, full race suite, production build and 69-tool stdio/HTTP parity. Installed Quadlet generator accepted isolated minimal and escaping fixtures. Publication fixtures cover manual-change rejection, stale-owned-file removal and interrupted-output acceptance; command fixtures prove define-without-start and disable-without-restart. No real workload/systemd mutations were performed. Step 4 remains partial; generated syntax is not evidence of successful native boot or lifecycle behavior.

Readiness/UI continuation: missing-tool and unknown-backend fixtures, dependency propagation, ten-second native cache/explicit refresh, bounded mountinfo parsing and source matching are covered by race tests. Applications navigation is included in desktop/mobile/320px browser checks; desktop fixture assertions verify an actionable missing-folder warning disables Start while leaving boot disable available. Remaining checks and full forms are not complete. Existing development-host workloads were not changed.

Verification for the readiness/UI commit: `make check test ui-test mcp-test` passes with zero frontend diagnostics, the full race suite, browser assertions at 1440/390/320px, official SDK stdio/HTTP and 71-tool parity. Subsequent focused declarative race tests pass after DAC write-access and boot-only issue propagation refinements. UI creation/editing, all target backends and portable restore remain incomplete.

Compose continuation evidence: `make check test mcp-test` passes; focused race tests also pass after shared ownership checks, user-manager prerequisite reuse and resource-limit fixes. Fixtures cover literal dollar handling, private candidate validation, Docker/Podman inventory shapes, define without start, disable without interruption, recreation to load saved settings and refusal to remove from foreign generated files. Initial boot policy is explicitly oneshot-only with engine restart `no`; broader policies remain unimplemented. No live Compose mutations or boot tests were performed, and step 5 is not complete.

Libvirt continuation evidence: `make check test` passes. Installed `virt-xml-validate` accepted an isolated generated domain through a read-only temporary-file test. Fixtures verify stable UUID/MAC and escaped disk paths, stopped definition, unchanged execution on boot toggles, refusal of immediate running hardware edits, deferred persistent registration, full shutdown/start loading pending settings, foreign-domain rejection and disk-preserving undefine. VM runtime fingerprints now reuse the same generic helper as containers; removal verification can use the prior identity. No real libvirt domains or host guest data were modified. Step 6 remains partial.

Libvirt verification update: `make check test mcp-test` passes after adapter registration, including full race tests, production build and official SDK stdio/HTTP schema parity. Further focused race fixtures verify failed shutdown never forces/restarts the guest and unsupported image-only provisioning is rejected before native commands.

Incus continuation evidence: `make check test mcp-test` passes (full race suite, frontend checks, production build and official SDK parity). Focused fixtures cover local/project-scoped requests, stopped creation, no inherited profiles, volatile identity preservation, deferred active settings, graceful restart, autostart without interruption, ownership rejection, shrinking/reprovisioning refusal, and data-safe removal refusal. Resource-specific binding issues now feed shared preflight. Incus is not installed in this environment; no real Incus guests, pools or networks were changed. Step 7 remains partial, including conservative active-revision reporting and unimplemented deletion/export/custom volumes.

Shares continuation evidence: `make check test mcp-test` passes (full race suite, frontend, production build and 73-tool stdio/HTTP parity). Temporary Samba/NFS fixtures prove authoritative set replacement removes stale owned definitions, retains unmanaged exports/config and shared data, rejects stale revisions/collisions, validates candidates without publication/reload, and restores prior output after reload failure. Root-helper tests reject unknown fields/reserved native definitions before commands. Central projection tests verify omitted disabled shares, relative storage paths, NFS defaults and typed helper arguments. Protocol changes require explicit new definitions in this initial implementation. No real shares/exports/clients were mutated; step 8 remains partial.

Definition-editor continuation: typed resource edits validate the complete candidate, reject stale revisions/unknown fields/dangling references and leave preview non-mutating. Initial forms cover container groups, existing-disk libvirt/local-image Incus VMs, storage references, bindings, private-file references and shares, with reviewed immediate/deferred saves. Configuration includes YAML recovery and incomplete-apply inspection/retry. Existing direct container/machine editors and native APIs still need retirement. No host workloads were modified; browser mutation flows are fixtures with real write-disabled denial assertions.

Editor verification: `make check test ui-test mcp-test` passes; the current browser verifier additionally passed the new Docker application definition/review flow and central Samba share flow, with request payload assertions and rejection by the real write-disabled server. Navigation/overflow checks cover 1440/390/320px, including Configuration and central File shares. Frontend checks pass after guarding edits of removed definitions. Focused race tests cover typed-edit conflicts/dangling references and reused-history retention/tamper rejection. These remain fixture UI/native boundaries, not evidence of successful real backend activation.

Legacy-route retirement continuation: removed the old Compose discovery/editing module, LXD/classic LXC/arbitrary-XML machine module, unused pages/client types, direct Samba/NFS save/remove transport/helper operations and obsolete discovery-root bootstrap settings. Host monitoring, ordinary system/user services, native share inspection/setup and package management remain direct. Application-owned user units now route lifecycle/startup actions through the coordinator; details identify ownership and direct reload is refused. Broken YAML blocks reserved-unit writes. Removed the classic-LXC dependency preset; Incus installation remains explicit through host setup rather than a speculative distribution package matrix. Replaced discovery-layout examples with a boot-disabled portable YAML/static-site bundle.

Retirement evidence: `make check test mcp-test ui-test` passed with zero frontend diagnostics, race tests, production build, official SDK stdio/HTTP 42-tool parity and browser navigation/forms/denied writes at 1440/390/320px. Added focused race coverage for user/system routing, default scope, central disable-without-stop, managed details, direct-reload rejection and malformed-config refusal. No host workloads, guests, shares or packages were changed. These results establish removal and fixture behavior, not native lifecycle/boot acceptance; the full implementation phase remains incomplete.

Global-readiness continuation: added `managed_config_readiness` for shared cached native/fresh filesystem checks without runtime inventory. An authenticated global alert checks on initial mount, management-job completion, tab visibility and bounded visible-tab polling, stays mounted across navigation, exposes pending/unknown/invalid states, retains old issues on failed refresh, lists all issues and links to repair screens. Rechecks never start workloads or create missing data. New Go fixtures cover absent/malformed manifests, relocated missing data, restart/recheck repair and no native apply. Native capability completeness, detailed path diagnostics and real restored-host acceptance remain open.

Global-readiness verification: `make check test mcp-test ui-test` passed, including zero frontend diagnostics, full Go race tests, production build and official SDK stdio/HTTP 43-tool parity. An additional browser run covers the global alert at 1440/390/320px, persistence across navigation/reload, explicit unknown/invalid results, repair clearing, and no extra mutation calls during recheck. Fixture assertions and temporary filesystem checks do not establish exhaustive porting diagnostics or successful live backend startup.

History/revert continuation: added private bounded history list/read and reviewed revert operations plus the Configuration history UI. Malformed revisions remain inspectable for repair, while direct revert requires valid YAML and current revision. Revert reuses normal identity/native validation and save/apply semantics, supports explicit supersession of failed transitions, refuses to abandon unresolved removals and retains one bounded prior journal during prepared-save recovery. Current/history YAML now share descriptor-based private-file validation; partially decoded invalid YAML cannot authorize native removals. Fixture tests cover stale writes, failed-update revert, unresolved removals, both sides of the durable save boundary, failed history persistence, unsafe entries and invalid YAML repair. Backup/data restoration and exhaustive native recovery remain open.

History/revert verification: `make check test mcp-test ui-test` passed (zero frontend diagnostics, full race suite, production build, official SDK stdio/HTTP 47-tool parity and desktop/mobile/320px browser checks). The browser exercises historical inspection, next-start review/confirmation payloads and real write-disabled rejection. Final `make check test` and focused declarative race tests also pass after shared private-file reading and invalid-prior-definition guards. Tests use temporary files/recording adapters; no live workload, disk, share or package changes were made. Complete fault-injection coverage and live native recovery remain open acceptance gates.

New-data provisioning continuation: shared plan/create tools and the Configuration review form create explicit new empty directories or blank sparse raw/qcow2 disks. Plans bind current revision, parent identity/ownership and destination to a review token. Existing/external/private-state paths, symlinks, unsafe/missing parents, expected-mount failures, active consumers and published/unknown shares are refused. Publication rechecks consumer/path conditions. Native qcow2 create/info runs against a held new staging descriptor and a no-replace link publishes verified output. Missing restored data remains blocked; ordinary saves/preflight/start never call provisioning.

Provisioning verification: `make check test mcp-test ui-test` passed, including the full race suite, zero frontend diagnostics, production build, official SDK 49-tool parity and reviewed new-data payload/acknowledgement/real denied-write browser assertions. Temporary-file tests verify permissions, sparse raw disks, no overwrite, revision/review-token conflicts, parent replacement, cancellation, failed staging cleanup and consumer guards. Installed qemu-img created and inspected only a new temporary qcow2 disk; no real guest or persistent host data changed. Focused race tests pass after publication revalidation refinements. VM image/guest provisioning, backend volumes, ownership/ACL/SELinux administration, backup/restore and live integration remain open.
