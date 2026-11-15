# Declarative example

Copy `server-man.yaml` and `data/` together into a new private directory outside existing workloads. Select the copied unified file with `server-man -config /absolute/path/server-man.yaml`; its optional `server` section holds application settings. Do not overwrite an existing configuration or data directory. Workload paths resolve relative to the YAML.

The example declares a stopped, boot-disabled Podman Quadlet application serving static content on container port 8080 without publishing a host port. It uses a read-only mount of the bundled site directory. Copying the bundle does not pull images, publish native definitions or start containers. Inspect configuration health, review and save/apply the definition, then explicitly Start when ready. Native Podman/Quadlet and the application's user manager must be available; host package installation remains an explicit action. Review image provenance and updates before use.

To use Podman Compose, change `backend` to `compose`. To use Docker Compose, also change `engine` to `docker`. Both Compose adapters currently use boot-only user units with container restart policy `no`. Keep the application stopped while changing the backend: an existing service's kind/backend cannot be changed in place, so choose a new stable ID when using another backend.

This example contains no credentials, VM disks or file-share publication. It does not establish live lifecycle, boot or backup/restore verification. See [current design and limits](../../docs/design/declarative.md).
