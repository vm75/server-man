# Compose management

The Containers page and MCP use the same `compose_*` tools for inventory, actions and project files. Install Docker with its Compose plugin, or Podman with a compatible Compose provider, under the account running server-man. [Podman delegates `podman compose` to an external provider](https://docs.podman.io/en/latest/markdown/podman-compose.1.html); its provider selection remains configurable through Podman's normal settings. The provider must support `--project-directory`, repeated `--env-file` arguments in order, resolved JSON configuration and JSON `ps` output. Ubuntu podman-compose 1.6.0 lacks the first two requirements; use standalone Docker Compose v2 with Podman instead (a Docker daemon is not required). Set `compose_providers` under `[engine]` in the account's `containers.conf` or `PODMAN_COMPOSE_PROVIDER` to its executable path, and provide the account's rootless Podman API socket. server-man never invokes `sudo`. Rootless inventory belongs to the invoking account and its runtime context.

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

With `allowWrites: true`, the Containers page can create a project under the selected engine root, edit its selected Compose file, create/edit project `.env` files, and create nested directories under `data/<project>`. Project directories and data directories created by server-man are mode 0700; Compose and environment files are mode 0600. Environment files are restricted to one filename directly under that project's data directory; `data_root.env` and `global.env` remain shared host-managed files. Saves use atomic replacement and a SHA-256 revision check to reject stale edits. Editor file reads and writes are authenticated through the shared registry and are unavailable when management writes are disabled. Raw Compose and env content is returned to authenticated callers, including MCP clients with the same token. Env values travel in plain HTTP when the app is used directly over HTTP; use only on a trusted network.

Each project uses the first existing file in this order: `compose.yaml`, `compose.yml`, `docker-compose.yaml`, `docker-compose.yml`. server-man explicitly selects that one file with `-f`; it does not add override files. Put required configuration in the selected file, or use the provider's supported includes. It passes `--project-directory` and sets the command's working directory to the project directory. It does not force `--project-name`, preserving the [provider's project-name precedence](https://docs.docker.com/reference/cli/docker/compose/).

Existing `data_root.env`, then `global.env`, then visible project `*.env` files in lexical order are passed as separate `--env-file` arguments. Missing global files are allowed. Hidden project env files are excluded, matching the existing Bash launcher. With no explicit files the provider's default environment behavior applies. Inventory returns filenames only. The authenticated file editor can return raw project Compose and env file contents; these file-read tools are gated by `allowWrites` because env values are sensitive. Providers still inherit the server's environment and interpret variables, mounts, devices and extensions themselves.

## Project file tools

`compose_project_create` creates a new `compose.yaml` and matching `data/<project>` directory. `compose_file_read` and `compose_env_read` return the selected raw file and a revision hash; `compose_file_save` and `compose_env_save` require that revision (or `missing` for a new env file). Env filenames are a simple `*.env` name and cannot be the shared `data_root.env` or `global.env`. `compose_data_mkdir` accepts a relative nested directory path and rejects traversal and symlinks. Save and folder operations run as bounded jobs; job output contains only status and paths, never file contents. Project/environment file reads use the authenticated shared registry and are disabled until `allowWrites` is enabled.

Project and service identifiers must start with an ASCII letter/digit and contain only letters, digits, dot, underscore or hyphen, up to 128 characters. Project directories, selected Compose files and env files are checked against their configured roots after resolving symlinks. Symlinks that redirect a project to another project or escape its directory are rejected.

Before saving a Compose file or running `up`, `start` or `restart`, server-man asks the configured provider for resolved JSON and validates the resulting host access rules. Bind mounts and service-level env files must stay under `data/<project>`; named volumes, Compose `configs`/`secrets`, build SSH/secrets/additional contexts, devices, added capabilities, privileged mode, provider services, host namespaces, and security relaxations are rejected. A local build context must stay under that project's config or data directory. Validation failures return generic errors so resolved environment values are not included in errors or job output. This policy limits configured host filesystem access; it does not sandbox workload behavior or network access.

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

Command-recording fixtures cover both engines, all six actions, directory paths containing spaces, file precedence, env ordering, disabled behavior, missing backends, JSON variants, custom project names, associated pods, bounded logs, traversal/symlink rejection, atomic editor writes, stale revisions, private modes, and rejection of unsafe resolved mounts/host options. Core registry tests cover write policy and destructive acknowledgements; the official MCP SDK checks tool/schema parity, discovery, invalid identifiers and denied Compose writes and private-file reads.

Previous-host discovery found 22 existing Podman projects; sandbox restrictions prevented engine runtime checks there. On the migrated host, three safe examples pass Compose v2 syntax validation. Live authenticated HTTP/MCP discovers all three with ordered env filenames and disabled markers; actual Podman project status returns empty/not-created. Headless desktop/mobile browsers render the real inventory over TCP. No images were pulled or workloads created/started. Docker engine and live lifecycle remain unverified. See the [host setup record](../operations/host-setup.md).
