# Portable backup input inventory

Backup/restore implementation is in progress. The current delivered operation is metadata inventory, exposed as `managed_backup_inventory` and through Configuration. It produces stage `inventory`, never a completed/prepared backup, and creates no archive, export, journal or workload changes. See the [implementation plan](../plans/declarative-management.md#10-portable-backup-preparation-and-restore) for the full acceptance gates.

The inventory pins the authoritative current configuration revision and rechecks it before returning. It includes all declared storage, even unused storage, private-file references, initial image assets and required backend guest-state entries. Each input identifies its logical resources, strategy, bundle path, sensitivity and inspection state. File size/allocation figures are top-level filesystem metadata; they are neither recursive totals nor a backup-size estimate. Current YAML maps to `server-man.yaml` in the eventual bundle.

| Strategy | Current meaning |
| --- | --- |
| `bundle_copy` | Declared owned input whose metadata can be inspected; contents still need collection/checksums |
| `external_data` | Required external storage, excluded pending explicit inclusion and destination-binding review |
| `native_export` | Required guest state/export whose dependencies have not yet been enumerated |

Shared directories and exact duplicate references retain their consumers while `includedBy` identifies an input covering the same data. Future copy code must honor that coverage to avoid duplicate data/private files. Missing/inaccessible/wrong-type inputs, unsafe links, configuration/derived-state collisions and unresolved native/external coverage produce structured issues. Metadata lookup uses a root-confined directory handle; FIFOs/devices are rejected without opening them. Referenced secret contents are never read or returned.

Collection/restore requirements explicitly include directory-tree scans, expected mounts, disk backing/dependent files, sparse allocation and filesystem metadata (UID/GID, ACLs, xattrs, SELinux and links). All bundles will need private destinations because configuration/data can contain credentials. Workload consistency and published-share client writes require deliberate quiescence; unpublishing does not disconnect existing Samba sessions. Runtime image availability, host accounts/credentials, bindings and native setup remain explicit destination requirements.

Native VM entries remain unknown until backend collection is implemented. Libvirt needs owned-domain guest state such as NVRAM/TPM and backing dependencies. Incus requires native instance/custom-volume enumeration and portable exports, rather than copying daemon storage. Inventory never infers that guest data is complete from the configured root disk alone.

Generated native definitions, apply journals/caches and configuration history are excluded from this initial inventory; regenerate definitions from the selected manifest. Host bootstrap/authentication, packages/system services, account/credential setup and native network/pool setup are not portable workload data. Container writable layers, unmanaged volumes and local image caches are excluded; only declared persistent storage is included.

Inventory is bounded to 2,048 combined inputs/requirements and at most 1 MiB of serialized output, under the shared read deadline. It checks cancellation and rejects revision conflicts. Metadata inspection does not prove consistent contents, complete recursive dependencies or a restorable bundle. Data streaming, native exports, manifest/checksum publication, configurable backup deadlines, restore/conflict handling, metadata/account mapping and pre-start restore validation remain unfinished.

Temporary-directory tests verify resource coverage, shared-path coverage, no private-content disclosure, missing paths, root escapes, special files, cancellation and bounds. Official SDK tests enforce private-read policy and transport discovery parity; browser fixtures verify honest inventory-stage output and responsive layouts. No real workloads or guest data were backed up or restored.
