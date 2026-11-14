# Declarative configuration foundation

The declarative implementation is in progress. Its [implementation plan](../plans/declarative-management.md) tracks the complete target; the functionality below describes the current foundation only.

`internal/declarative` owns the typed workload YAML independently of transports. The initial schema includes service, storage, binding, private-file reference and share definitions. Container definitions default to Podman with Quadlet; Docker accepts Compose only. VM backend choices are libvirt and Incus. Parsing rejects unknown/duplicate fields, aliases, anchors, explicit tags, multiple documents and files above 1 MiB. Domain validation checks references, dependency cycles, enabled dependencies, native field constraints and duplicate guest identities. The schema still needs additional target features before backend implementation is complete.

The private configuration store resolves relative asset paths against the manifest location. Saves validate before publication, generate persistent VM UUID/MAC values when absent, preserve existing identities and reject identity changes. An owner-only temporary file is synced and renamed, followed by directory sync. Callers must hold the shared management lock around the operation; external edits are checked by revision immediately before publication, but this is not a filesystem transaction with arbitrary external editors. A failed final directory sync can follow successful replacement and must be treated as uncertain publication by the future coordinator.

Up to eight previous configurations are retained in an adjacent owner-only `.history` directory, addressed by hashes of their exact original bytes. Invalid YAML can be read through the same private-file policy and repaired with its current revision; the invalid original is retained in history. UI saves normalize YAML formatting. Private reference files are never opened to collect configuration history.

The bootstrap `manifestPath` setting accepts an absolute path. When omitted, it selects `server-man.yaml` inside `stateDir`. Startup does not create that file or any workload data. Shared registry tools provide:

- `managed_config_read`: private configuration editor content and revision, gated by `allowWrites`, including malformed YAML for recovery.
- `managed_config_validate`: typed validation and file preflight of a candidate without saving it.
- `managed_config_health`: validity and file preflight of the current configuration, with missing/invalid configurations remaining inspectable.

File preflight reports missing/wrong-type paths, symlink escapes and private-file permission problems, and propagates blockers to referencing services/shares. It never creates missing directories or disks. An expected mount remains blocked as unverified until mount-identity probing is implemented. Responses explicitly report `runtimeChecked: false`: file checks do not prove permissions, daemon connectivity, backend capability, image availability or runtime readiness.

These tools do not yet save through the registry, publish native definitions or activate workloads. The apply coordinator, backend adapters, complete preflight, UI and portable backup workflow remain tracked work. Existing host modules continue operating independently until their planned replacements are implemented and verified.
