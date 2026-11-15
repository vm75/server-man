# Versioning and CI

server-man uses [Semantic Versioning](https://semver.org/), with a `v` prefix on Git tags and no prefix in the application's displayed version. The committed `VERSION` file is the intended next release version. While the [declarative management plan](plans/declarative-management.md) remains incomplete, keep releases in the `0.x` prerelease series (such as `0.1.0-alpha.1`). A tagged prerelease does **not** assert that unfinished features are implemented or that unverified native integrations are ready for general use.

## Build identification

`make build` embeds the version into the Go executable through `tools/build.sh`.

- On a clean checkout at the exact `v<VERSION>` tag, the executable prints `VERSION` (for example, `0.1.0-alpha.1`).
- On an untagged commit, it prints `VERSION+g<commit-sha>` (for example, `0.1.0-alpha.1+g0123456789ab`).
- Tracked working-tree changes append `.dirty` to the build metadata. A source archive without Git metadata uses `VERSION+source`.
- `SERVER_MAN_VERSION` can override the calculated version for a deliberately identified build.

Use `./bin/server-man -version` to inspect a binary. The same version appears in the authenticated `system_status` tool, `/api/status`, and the web footer. `web/package.json` is a private frontend build manifest, not the application release-version authority.

Build metadata identifies different development commits but does not change SemVer precedence. Feature availability must come from the current implementation and documented limits, not merely the version number.

## Manual prerelease procedure

1. Update `VERSION` in a PR targeting `main` for the next version (for example, `0.1.0-alpha.2`). Describe the implemented features and remaining limitations; do not check off unfinished work.
2. Merge only after the PR verification succeeds. Confirm CI also succeeds for the resulting `main` commit. If needed, run CI manually from the GitHub Actions tab.
3. Check out the intended `main` commit locally, with tags fetched and a clean working tree. Run `make setup`, `make check test mcp-test ui-test`, and `make build`. Native/host-specific acceptance remains a separate requirement.
4. Create an annotated tag on that exact commit and push it:

   ```sh
   git switch main
   git pull --ff-only
   git tag -a "v$(cat VERSION)" -m "server-man $(cat VERSION)"
   git push origin "v$(cat VERSION)"
   ```

5. Optionally create a GitHub Release from the tag, mark it **Pre-release**, and disclose incomplete or unverified capabilities. No automated release or artifact publication is configured.
6. Bump `VERSION` to the *next* intended prerelease in a subsequent PR before continuing normal development.

Do not tag an unmerged PR or publish a `1.0.0` release merely because versioning is enabled. The tag is permanent release identity, not an indication of implementation-plan completion.

## GitHub Actions trigger policy

The `.github/workflows/ci.yml` workflow runs only for:

- Pull requests whose **base** branch is `main` (on opening, reopening, or new commits to the PR).
- A push to `main`, including a merged PR.
- Explicit `workflow_dispatch` (Actions → CI → Run workflow).

Feature-branch pushes without a PR, PRs targeting other branches, and tag pushes do **not** trigger this CI workflow. Merging a PR normally produces one `push` run on `main`; the workflow does not also run on the PR's closed event. Manual runs may target the selected ref. Checkout fetches tag history so a tagged build is recognized.

The workflow verifies Go, frontend, MCP, browser checks and staged installation; it does not deploy, tag, or publish releases.
