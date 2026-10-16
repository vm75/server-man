# Project guide

Serverman is a Linux administration application built with Go, Svelte 5, TypeScript, and Vite. Web and MCP operations must share the same validated management functions.

## Scope and tracking

- Follow [the implementation plan](docs/plans/implementation.md); commit each verified step and its tracking update separately.
- Use KISS and YAGNI. Prefer standard libraries and small explicit interfaces.
- Search before reading broad files; keep context relevant and avoid generated/dependency directories.
- Preserve existing conventions unless the task requires a change.

## Boundaries

- Never pass user input to a shell. Execute fixed programs with argument arrays and validate resource identifiers.
- Do not copy secrets, environment file contents, or resolved Compose configuration into logs, fixtures, APIs, or Git.
- Verification may read host inventory and metrics. Use temporary directories and command fixtures for operations that change containers, VMs, packages, shares, or services.
- Do not modify `~/vms`; preserve its conventions through configurable discovery.
- Keep monitoring, retained output, jobs, and process concurrency bounded.
- Authentication, write policy, and validation apply equally to HTTP and MCP.

## Completion

- Validate behavior relevant to each change. Record what ran and its limits in the plan.
- Update only documentation whose truth changed: user commands/capabilities in README, implementation tracking in the plan, component boundaries in architecture documentation when it exists.
- Keep unrelated changes out of each commit. Never claim unfinished plan items are complete.
