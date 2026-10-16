# Service management

The Services page and nine `services_*` tools manage existing systemd service units. Serverman does not expose arbitrary commands, unit-file editing, masking, daemon reexecution, or reboot through this module.

## Scope and permissions

`systemdScope` is `system` by default; set it to `user` to manage the server account's user manager. All systemctl and journalctl commands use the configured scope. It cannot be changed per request. A user manager also requires that account's normal runtime directory/session bus. System management and journal access require the account's normal operating-system permissions; Serverman never invokes `sudo` or an interactive authorization prompt.

`systemctl` and `journalctl` are detected independently at startup. The systemd capability reports the control utility's availability; service inventory also reports whether journalctl is installed. Installed binaries do not prove bus access. A non-systemd host, missing session bus or denied D-Bus connection produces an explicit notice/error while other Serverman modules remain usable. See the upstream [systemctl manual](https://github.com/systemd/systemd/blob/main/man/systemctl.xml) for command behavior.

## Inventory and details

`services_list` merges two queries by exact unit name:

- `list-unit-files --type=service` supplies installed units, enablement and preset where available.
- `list-units --type=service --all` supplies loaded state, active/substate and description where available.

Both select full, plain output without a legend/pager and request JSON. Modern systemd table JSON and older uncolored column output are accepted. Legacy loaded-unit descriptions are omitted because an in-flight job can add a variable JOB column; opening details supplies an unambiguous description. Runtime-only transient units and installed units absent from the loaded list are both visible. Missing runtime values remain `unknown`, never inferred as inactive. A failed query retains successful data from the other and adds a warning; two failed queries show unavailable inventory.

Each query has a five-second deadline and 1 MiB capture limit. The merged inventory is limited to 4,096 service units and sorted by name. The UI filters by name/available description and runtime state. Refresh is explicit and also runs after a service job completes.

`services_details` reads only these `systemctl show` properties: Id, Description, LoadState, ActiveState, SubState, UnitFileState, MainPID, Result, ExecMainCode/Status, ActiveEnterTimestamp, InactiveEnterTimestamp, FragmentPath, and CanStart/Stop/Reload. Environment, command lines, credentials, unit-file contents and StatusText are excluded. Aliases may return a canonical Id; the UI retains the selected alias for subsequent requests. A not-found LoadState is an error.

## Unit identifiers and logs

Every detail/log/action request requires one exact name ending in `.service`, up to 255 bytes. Supported characters are ASCII letters/digits, underscore, at sign, colon, dot, plus, hyphen and canonical `\xHH` escapes. Leading hyphens, whitespace, paths, glob patterns and other unit types are rejected. Fixed executable/argument arrays and an option terminator are used for systemctl actions. Instance and template names are accepted; actions a template cannot perform fail normally in systemd.

`services_logs` takes `unit` and optional `tail` from 1–2,000, default 200. It uses journalctl in the same scope, filters one unit, prints short ISO timestamps, and never follows the journal. It has a ten-second deadline and retains the newest 128 KiB. Both stdout/stderr are bounded; command failure becomes a generic access error. An empty successful journal can mean no entries or limited visibility. The UI explains that distinction. Logs may contain private application data and require authentication. See the upstream [journalctl manual](https://github.com/systemd/systemd/blob/main/man/journalctl.xml) for user/system matching and journal visibility.

## Actions

All actions require `allowWrites: true` and return tracked jobs under the shared mutation lock. Commands use `--no-ask-password` and preserve cancellation/process-group limits. Failures and output appear in Activity.

| Tool | Behavior | Requires `confirm: true` |
| --- | --- | --- |
| `services_start` | Start a service | No |
| `services_stop` | Stop a service | Yes |
| `services_restart` | Restart a service | Yes |
| `services_reload` | Ask the service to reload its own configuration | Yes |
| `services_enable` | Create startup enablement links | No |
| `services_disable` | Remove startup enablement links | Yes |

Enable/disable do not pass `--now`; current runtime state is unchanged by that command. Dependencies, socket/timer activation and administrator actions may still start a disabled unit. Reload is the service's own reload operation and does not run daemon-reload. Unsupported reloads, protected units and denied permissions fail through systemd. No privilege escalation or service-specific override is attempted.

## Verification

Race-tested fixtures cover both scopes, all six actions and exact arguments, JSON/legacy parsing, installed/runtime merging, partial/both query failures, absent tools, allowlisted properties, aliases/instance/escaped names, invalid paths/patterns/flags/types, journal limits and command failures. A registry job verifies enablement execution and destructive acknowledgement; the official MCP SDK checks inventory, invalid units, denied writes and full schema parity across both transports.

Read-only commands detect systemd 259.5 on this host, but the sandbox denies access to the system bus. No actual service or enablement changes were executed. Real bus integration, journal visibility and desktop/mobile browser rendering remain unverified.
