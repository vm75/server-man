# Compose management

The Containers page and MCP use the same nine `compose_*` tools. Install Docker with its Compose plugin, or Podman with a compatible Compose provider, under the account running Serverman. [Podman delegates `podman compose` to an external provider](https://docs.podman.io/en/latest/markdown/podman-compose.1.html); its provider selection remains configurable through Podman's normal settings. Serverman never invokes `sudo`. Rootless inventory belongs to the invoking account and its runtime context.

## Project layout

With the default `vmRoot: "~/vms"` expanded to the user's absolute home directory, projects are discovered under:

```text
vms/
  config/podman/<project>/compose.yml
  config/docker/<project>/compose.yml
  data/data_root.env
  data/global.env
  data/<project>/*.env
  data/<project>/.disabled
```

Optional absolute `podmanRoot` and `dockerRoot` settings replace the two discovery roots. Environment files and markers always come from `vmRoot/data`. A missing root or engine is shown explicitly; discovered file metadata remains available when an engine is absent.

Each project uses the first existing file in this order: `compose.yaml`, `compose.yml`, `docker-compose.yaml`, `docker-compose.yml`. Serverman explicitly selects that one file with `-f`; it does not add override files. Put required configuration in the selected file, or use the provider's supported includes. It passes `--project-directory` and sets the command's working directory to the project directory. It does not force `--project-name`, preserving the [provider's project-name precedence](https://docs.docker.com/reference/cli/docker/compose/).

Existing `data_root.env`, then `global.env`, then visible project `*.env` files in lexical order are passed as separate `--env-file` arguments. Missing global files are allowed. Hidden project env files are excluded, matching the existing Bash launcher. With no explicit files the provider's default environment behavior applies. Serverman returns filenames, never env values or resolved configuration. Providers still inherit the server's environment and interpret variables, mounts, devices and extensions themselves.

Project and service identifiers must start with an ASCII letter/digit and contain only letters, digits, dot, underscore or hyphen, up to 128 characters. Project directories, selected Compose files and env files are checked against their configured roots after resolving symlinks. Symlinks escaping those boundaries are rejected. Compose contents are trusted administrative configuration: mount paths and references inside them are interpreted by the provider. Serverman does not sandbox a Compose workload or edit these files.

## Inventory and logs

`compose_projects` accepts an optional `engine` filter. It discovers up to 1,024 projects, then runs one native `ps --all --format json` per installed engine with discovered projects. Compose working-directory/config-file labels associate containers with projects; a project-name label is the fallback when path labels are absent. JSON arrays and Docker's newline-delimited objects are accepted. Commands and unrelated labels are discarded. See the native [Podman container JSON fields](https://docs.podman.io/en/latest/markdown/podman-ps.1.html).

Unmatched projects after a successful native query show **Not detected**; this is not proof the provider has never created them. Runtime failures show **Unknown** with an access notice. `compose_status` asks the project's provider for `ps --all --format json`, yielding authoritative provider membership. An empty successful result shows **Not created**. For Python podman-compose providers that reject `--all`, it retries `ps --format json`; [that provider already includes stopped containers](https://github.com/containers/podman-compose/blob/main/podman_compose.py). Other errors are not retried. Podman details also query [pod JSON](https://docs.podman.io/en/latest/markdown/podman-pod-ps.1.html) and return only pods referenced by the project's containers.

Native inventory queries have five-second deadlines; provider status has seven seconds per attempt. Captured JSON is limited to 1 MiB and 4,096 objects. A project has at most 128 explicit env files. Invalid/unsupported JSON and missing permissions produce explicit errors rather than an empty successful inventory.

`compose_logs` accepts `engine`, `project`, optional `service`, and `tail` from 1–2,000 (default 200). It runs non-following, uncolored logs with a 15-second deadline, retaining the newest 128 KiB. Logs can contain private application data; authentication protects access. Provider status failures use generic diagnostics to avoid exposing expanded configuration values.

## Actions

All actions require `allowWrites: true`, return a tracked job, and use the shared single-mutation lock. The project is resolved again when execution begins. A `.disabled` marker blocks up, start, restart **and pull**, matching the launcher; stop and down remain available.

| Tool | Provider operation | Requires `confirm: true` |
| --- | --- | --- |
| `compose_up` | `up -d`; create/reconcile, possibly recreate containers | Yes |
| `compose_start` | Start existing containers | No |
| `compose_stop` | Stop project containers | Yes |
| `compose_restart` | Restart project containers | Yes |
| `compose_pull` | Pull project images | No |
| `compose_down` | Stop/remove project containers and networks | Yes |

Down never requests `--volumes`, `-v`, or `--remove-orphans`. Named volumes and bind mounts remain; container writable layers are removed by the provider. Cancelling a job terminates its command group but cannot undo completed changes. Output and completion state appear in Activity; selected project details refresh when its job finishes.

## Verification scope

Command-recording fixtures cover both engines, all six actions, directory paths containing spaces, file precedence, env ordering, disabled behavior, missing backends, JSON variants, custom project names, associated pods, bounded logs, and traversal/symlink rejection. Core registry tests cover write policy and destructive acknowledgements; the official MCP SDK checks tool/schema parity, discovery, invalid identifiers and denied Compose writes.

Read-only filesystem discovery found the 22 existing Podman projects under `~/vms`; no Compose/env contents were returned. Docker Compose version detection succeeded. This sandbox prevents Podman from preparing its runtime directory and blocks sockets, so real workload mutations and live engine inventory have not been verified. Desktop/mobile browser rendering remains an outstanding acceptance check in the implementation plan. Verification did not change the host's workloads or configuration.
