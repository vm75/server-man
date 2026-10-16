# Samba and NFS shares

File shares and the five `shares_*`/protocol tools inspect existing shares and manage application-owned fragments. server-man does not edit unmanaged host configuration, create accounts, change directory permissions/ownership/ACLs/SELinux labels, or remove shared data. The server account needs native file/daemon permissions; writes also require `allowWrites: true`.

## One-time host setup

### Samba

Install and configure a standalone Samba file server using your distribution's normal setup. server-man uses `testparm`, `smbcontrol` and optional `smbstatus`. Set these absolute configuration paths if the defaults do not fit:

```json
{
  "sambaConfig": "/etc/samba/smb.conf",
  "sambaManagedFile": "/etc/samba/serverman.conf"
}
```

Choose a new, unused managed filename. Add exactly one literal include at the **end** of the main config:

```ini
include = /etc/samba/serverman.conf
```

Use the configured managed path verbatim, without variable substitutions. The owned file may be absent initially; server-man creates it on the first successful save. Do not prefill it with another configuration or create an empty placeholder: existing files must have server-man's ownership marker and exact generated format. No include is inserted automatically into the host file. See Samba's [include and reload semantics](https://www.samba.org/samba/docs/current/man-html/smb.conf.5.html).

The account must be able to read the main configuration, create temporary files in its directory and the managed directory, replace the owned fragment, and send smbd reload messages. server-man preserves main-file bytes and uses its directory for validation snapshots so relative configuration context is retained. File-based configuration is inspected; registry/usershare-only discovery and AD controller administration are outside this module.

### NFS

Install/configure the Linux NFS server, including its normal exports loading, filesystem support, firewall and mountd/nfsd services. Create `/etc/exports.d` using your host's normal setup if absent. The native paths are `/etc/exports`, `/etc/exports.d/*.exports`, and the owned fragment `/etc/exports.d/serverman.exports`. They follow nfs-utils rather than introducing a parallel configuration tree. The account needs permission to create/replace the fragment and manage the export table through `exportfs`.

No existing exports file is rewritten. A new fragment is picked up by the native server's normal exports loading on subsequent startup/reload. Runtime changes use individual client/path operations. The upstream [exports file format](https://github.com/linux-nfs/nfs-utils/blob/master/utils/exportfs/exports.man) documents native fragments and access rules.

## Supported access fields

All managed shares have a portable name (up to 64 ASCII letters/digits/dot/underscore/hyphen, starting with a letter/digit), an existing absolute directory, and explicit read-only/read-write access. Names are normalized to lowercase; Samba's reserved sections cannot be used. Symlinks in a selected directory are resolved and the canonical path is stored. Paths must be clean/literal and exclude control characters and configuration metacharacters; spaces are supported and escaped in NFS output. Directories are not created or otherwise modified.

Samba adds browseability, guest access, and up to 32 existing local users or `@group` names. A non-guest share requires an explicit access list. Guest mode requires an empty valid-users list and still depends on the host's guest mapping/account settings. Credentials never enter these tools; use the host's Samba account setup. The supported generator sets path, read only, guest ok, browseable and valid users explicitly.

NFS adds 1–16 IP/CIDR client selectors or an explicit `*`, root squashing, and one of `sys`, `krb5`, `krb5i`, `krb5p`. Bare addresses become `/32` or `/128`; prefixes are masked and sorted. Hostname patterns/netgroups remain available through unmanaged native configuration. Managed exports use `sync`, `secure`, `no_subtree_check`, and `no_all_squash`. Kerberos requires existing host configuration. NFS identity mapping and underlying filesystem permissions still apply. Only one managed definition may own an export path.

## Inventory and runtime state

`shares_list` accepts optional `protocol: "samba" | "nfs"`. It returns backend/tool/setup state, managed filename, current revision, warnings and selected share metadata. Missing tools do not break other modules. The UI shows host-owned entries for inspection with their editing controls disabled.

Samba configuration is read through bounded `testparm --suppress-prompt`; only names, paths and supported access fields are returned. Current use comes from whitelisted `smbstatus --json --shares` connection counts. Counts do not prove complete daemon configuration or share availability. Existing connections can retain old settings after reload, including a removed share; observed runtime-only shares remain visible. Full daemon configuration, sessions, open filenames and credentials are excluded. Samba's [testparm manual](https://www.samba.org/samba/docs/current/man-html/testparm.1.html) and [smbstatus JSON format](https://www.samba.org/samba/docs/current/man-html/smbstatus.1.html) define those native interfaces.

NFS inventory reads the main exports file plus up to 64 visible `.exports` fragments, preserving every file. Standard paths/quotes/octal escapes, client options, defaults and line continuations are parsed for display. The registered table comes from `exportfs -s`. Managed state distinguishes matching registration, absence, option mismatch and unknown runtime. Extra unmanaged/runtime-only client entries remain visible. Table registration does not prove filesystem exportability, daemon availability or a successful client mount; those require host integration checks.

Reads are bounded to 1 MiB per file/captured command, 1 MiB total unmanaged NFS config, 1,024 Samba shares and 4,096 NFS entries. Command deadlines are seven seconds for testparm, three for smbstatus, and five for export-table reads. A managed fragment contains at most 64 definitions. Parser/access failures are explicit, and mutations fail closed when safe ownership/conflict checks are unavailable.

## Transactions

`samba_share_save`, `samba_share_remove`, `nfs_share_save` and `nfs_share_remove` require the current fragment `revision` from inventory and `confirm: true`. They return serialized jobs. Save is also the reapply operation for an unchanged definition. The UI captures a form's revision when opened so an intervening refresh cannot silently approve a stale edit.

1. Read the owned file without following a final symlink or blocking on a FIFO. Verify its marker and generated body against typed metadata comments. Reject foreign files, manual edits, stale revisions and unmanaged identity/path conflicts.
2. Validate all input, directory existence, supported access fields and size limits. Build a temporary candidate in the fragment directory. Existing mode bits are retained; new fragments use 0644 and temporary validation shadows use 0600. Replacement files belong to the server account.
3. For Samba, replace only the owned include in a temporary main-file shadow. Test both the candidate and an empty-fragment shadow to detect unmanaged section collisions and verify effective managed values, including inherited global settings. The real main file is unchanged. See the native [configuration checker](https://www.samba.org/samba/docs/current/man-html/testparm.1.html).
4. Check that the owned file still has its original revision and that Samba's main file did not change during validation. Sync the candidate, atomically rename it over the fragment, and sync its directory.
5. Samba sends a bounded `smbcontrol ... smbd reload-config` request. It does not disconnect existing sessions. NFS uses `exportfs -i -o <validated options> <client>:<path>` for desired pairs and `exportfs -u <client>:<path>` only for removed, previously owned pairs. IPv6 command targets are bracketed. Global `-ra`, `-ua` and flush operations are never used, preserving unrelated runtime exports. NFS validates typed syntax before publication; native exportability checks happen during apply, followed by a registered-table access check. See the upstream [exportfs interface](https://github.com/linux-nfs/nfs-utils/blob/master/utils/exportfs/exportfs.man).
6. On failure/cancellation after publication, restore the previous fragment (or its prior absence), then request Samba reload or restore the affected NFS pairs and their complete prior native options. NFS rollback rereads/verifies affected entries; unrelated pairs are retained. Cleanup gets an independent 30-second deadline. Failed or refused rollback is reported in job error/output, including when the job is cancelled. External edits to the new fragment prevent overwriting it during rollback.

This is an atomic file replacement with bounded best-effort runtime recovery. Other administrators and native daemon reloads are not covered by server-man's lock. Coordinate external changes; daemon/client effects are not a multi-process atomic transaction. A crash between publication and apply can leave configured and runtime states different; inventory and Edit/apply expose that distinction. Rollback cannot undo client I/O or guarantee old settings for already connected Samba sessions.

## Verification

`make test` uses temporary directories and a recording runtime to exercise both protocols through create/edit/remove, exact targeted commands, metadata privacy, revision conflicts, malformed/foreign/symlink/FIFO files, candidate validation failures, unmanaged conflicts, runtime option mismatches, partial native failures, first-create cleanup, independent cancellation recovery and incomplete/refused rollback. It checks byte preservation of unmanaged files and shared data, plus unchanged unrelated runtime exports. The real installed testparm also validates temporary shadow configs, guest/authenticated shares and overridden global access defaults without contacting or reloading a daemon.

The official MCP SDK checks all tool/schema parity, share inventory, invalid protocol values, destructive annotations and denied writes through stdio and the production HTTP handler. Real Samba reload/client reconnection, NFS export/mount operations, and rendered browser layouts remain unverified in this restricted development environment. No host shares, accounts, permissions or shared data were changed during verification.
