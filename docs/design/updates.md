# System package updates

The Updates page and three `updates_*` tools inspect cached package candidates, refresh metadata and install normal native updates. server-man never installs selected arbitrary packages, edits repositories, disables signature checks, requests a distribution migration or requests a reboot. Package scripts, native repository policy and permissions remain the host administrator's responsibility.

## Detection and capabilities

`packageManager` defaults to `auto`; supported explicit values are `apt`, `dnf`, `pacman`, `zypper`. Automatic detection reads bounded os-release metadata and selects the native family from ID/ID_LIKE. A known distribution missing its native CLI reports that absence; it does not switch to a foreign installed manager. Unknown distributions can use a single unambiguous installed family or an explicit override. Multiple candidates require configuration. OSTree/image-based Fedora variants are unavailable in automatic mode.

APT uses `apt-get`. DNF uses the native `dnf` command, which can be DNF4 or a DNF5 alias; `dnf5` is a fallback when `dnf` is absent. Pacman and Zypper use their installed commands. Detection happens at startup. server-man uses the account's native package configuration and permissions, without sudo or interactive privilege prompts.

Arch's separate refresh capability requires `checkupdates` from pacman-contrib. Without it, cached system inventory and full installation remain available, and the UI explains why metadata refresh is unavailable. No helper is installed automatically.

## Inventory

`updates_status` returns the selected manager/capabilities, distribution label, cache source, read time, package candidates, a `valid` flag, notice and reboot evidence. It performs no refresh or installation. Failure/malformed data leaves the list invalid instead of reporting that no updates exist. Reads are limited to 12 seconds, 1 MiB capture and 2,000 package rows. Versions are native strings; server-man does not invent version-comparison rules.

| Family | Cached read | Returned version information |
| --- | --- | --- |
| APT | `apt-get --simulate --no-remove --with-new-pkgs ... upgrade` | Inst rows supply old/new versions and new dependencies; kept-back/phased rows are marked deferred |
| DNF | `dnf --cacheonly --quiet repoquery --upgrades --latest-limit=1 --queryformat ...` | Name, architecture and candidate EVR; installed version is not reported |
| Pacman | `pacman --query --upgrades --color never` | Old/new versions and explicit ignored markers where present |
| Zypper | `zypper --non-interactive --no-refresh --xmlout list-updates --type package` | Package-only name/architecture/current/candidate fields from XML |

APT simulation disables locking and binary cache writes. It accepts native temporary dependency-state suffixes and keeps deferred packages visible. The [APT manual](https://manpages.debian.org/testing/apt/apt-get.8.en.html) defines simulation and normal upgrade behavior.

DNF queries use supported name/arch/EVR tags and choose latest candidates per name/architecture; duplicate rows are collapsed. [DNF4](https://dnf.readthedocs.io/en/stable/command_ref.html) and [DNF5 repoquery](https://dnf5.readthedocs.io/en/latest/commands/repoquery.8.html) document this interface. Candidate queries are not an exact solver transaction, and obsoleting/deferred packages or native policy can change the eventual plan.

After an Arch refresh creates `stateDir/updates-pacman/sync`, cached reads select that isolated database; before the first refresh they read the system database. checkupdates maintains the local database link and isolated sync metadata. [Its documented database override and exit codes](https://man.archlinux.org/man/checkupdates.8) allow refresh without leaving the real package database after a standalone `pacman -Sy`. No download/change-state options are requested.

Zypper reads use its [documented XML schema](https://github.com/openSUSE/zypper/blob/master/src/output/xmlout.rnc). Repository URLs/credentials, descriptions and unrelated metadata are excluded. XML errors, missing update-list structure and directives are rejected.

Cached candidate counts are not a claim that every configured repository is current or that an exact install plan has been approved. The UI explains freshness, dependencies and deferred packages. AUR, Flatpak, Snap, custom installers and image-based OS updates are outside these native package operations.

## Refresh and install jobs

Both actions require enabled writes and hold the same mutation lock as every other management job. Install additionally requires `confirm: true` and a browser review dialog. No tool arguments select packages, commands or extra flags.

| Family | `updates_refresh` | `updates_install` |
| --- | --- | --- |
| APT | `apt-get -o APT::Update::Error-Mode=any update` | Refresh, then `apt-get --assume-yes --no-remove --with-new-pkgs -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold upgrade` |
| DNF | `dnf --assumeyes --refresh makecache` | Refresh, then `dnf --assumeyes upgrade` |
| Pacman | `checkupdates` with a private `CHECKUPDATES_DB` | `pacman --sync --refresh --sysupgrade --noconfirm` |
| Zypper | `zypper --non-interactive refresh` | Refresh, then `zypper --non-interactive --no-refresh update --type package` |

APT keeps existing conffile choices and allows new dependencies without removals; it uses `DEBIAN_FRONTEND=noninteractive` and list-only needrestart mode. Native maintainer scripts may still restart services. DNF/Pacman/Zypper honor their normal dependency, replacement and repository policies. Commands do not disable signature checks or pass repository-trust override flags. Native noninteractive defaults and configured signing-key workflows still apply; configure trusted sources before enabling automatic updates. Unsupported prompts, unaccepted license decisions or dependency conflicts fail through native tools. Tumbleweed snapshot migration via `zypper dup` is not provided by this normal-update operation; use its native workflow when required. The upstream [Pacman](https://man.archlinux.org/man/pacman.8.en) and [Zypper](https://manpages.opensuse.org/Tumbleweed/zypper/zypper.8.en.html) manuals define those distinctions.

Refresh failure prevents the install phase. Each command has a 20-minute deadline under the job's total 30-minute deadline. Package databases also retain their native locks; other tools/processes are not coordinated by server-man's state-directory lock. The current repository state can differ from the cached list, as the confirmation dialog states. Jobs keep bounded native output in Activity; there is no package rollback promise.

### Exit codes and cancellation

Zero is normal success. Exceptions are command-specific: Pacman query exit 1 is accepted only with empty stdout and separately proven empty diagnostics; checkupdates exit 2 means no updates. Zypper list codes 100/101 are informational when valid XML is returned; install codes 102/103 represent a reboot hint or package-manager restart/recheck requirement. The UI asks the operator to review/run remaining updates after 103. Partial/skipped repository statuses, other nonzero results and APT's error 100 fail their operation. DNF repoquery does not use check-update's separate exit-100 convention.

Cancel requests SIGINT for the entire native command group, allowing 30 seconds for cleanup before forced group termination. A separate pipe deadline includes that grace period. Cancellation can interrupt a package transaction, including after some changes have committed, and may require native recovery before retrying. The Activity drawer and install dialog explain this material behavior; server-man does not run automatic repair commands or undo packages. Other modules retain immediate group cancellation.

## Reboot evidence

A present `/run/reboot-required` marker reports a declared requirement. For APT, an absent marker means no requirement is declared by that mechanism, not proof that a reboot is unnecessary. Other backends report unknown when no reliable marker is available. Zypper's legacy install exit 102 provides an additional in-process positive hint; that hint does not persist across server-man restarts. No reboot control is exposed here.

## Verification

Race-tested command fixtures cover all four manager families, DNF4/DNF5 selection, native-family/ambiguous/immutable detection, exact read/refresh/install commands, held/dependency rows, parser/size failures, diagnostic-sensitive empty results, informational versus fatal exit codes, stopped phase progression, shared job serialization, destructive acknowledgement and cancellation. Real helper processes prove SIGINT delivery, grace-period forced cleanup, separate capture diagnostics and preserved exit status.

On this host, read-only APT 3.2.0 simulation parses real cached candidate/deferred packages without refresh or installation. The official MCP SDK checks status, invalid arguments, destructive annotations, denied refresh/install and full HTTP/MCP schema parity. No actual package refresh/install, native DNF/Pacman/Zypper mutation, reboot or host service change was executed. Rendered browser and live network checks remain tracked in the implementation plan.
