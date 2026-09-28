# AGENTS.md — plugin-check

Standalone plugin repo owning the externalized `charly check` command family
(`command:check`, compiled-in). The plugin is a Go module at
`candy/plugin-check/` (module path
`github.com/opencharly/plugin-check/candy/plugin-check`); the root `charly.yml`
only declares `discover: candy` so the repo is a project and its candy is
scanned.

Canonical files:

- `candy/plugin-check/charly.yml` — the `plugin-check:` candy entity
  (`plugin:` block, `plan:` checks).
- `candy/plugin-check/plugin.go` / `provider.go` — `NewProvider()` /
  `NewMeta()` / the `Invoke(OpRun)` surface.
- `candy/plugin-check/command.go` / `check_cmd.go` / `run_cmd.go` /
  `live_cmd.go` / `feature_cmd.go` — the kong command tree and its modes.
- `candy/plugin-check/bed_*.go` — the R10 disposable bed-runner.
- `candy/plugin-check/schema/checkroster.cue` — the plugin schema.
- `charly.yml` — the root project manifest (`discover: candy`).
- `.github/workflows/tag-on-merge.yml` — CalVer tag + `CHANGELOG/` on merge.
- `README.md` — user overview only; never agent guidance.

## Load these skills first (R0)

- `/charly-check:check` — every `charly check` mode, plan authoring, disposable
  beds, R10 runs, agent grading. Load before changing the command tree or the
  bed-runner.
- `/charly-internals:agents` — how the beds drive agent grading and fresh
  validator sessions.
- `/charly-internals:plugin` — the plugin authoring reference: the `plugin:`
  block, the `command` provider class, the per-plugin CUE-schema contract.
- `/charly-internals:git-workflow` — before any git/PR action.

## Build / validate / test

- `go build ./...` in `candy/plugin-check/` — compile the plugin module.
- `go test ./...` in `candy/plugin-check/` — the plugin's Go tests (the
  bed-runner, plan, and venue seams).
- `charly box validate` at the repo root — the structural check (the candy +
  `plugin:` block, CUE schema).
- The merge gate is the **org-wide** `charly/pr-validator` (required check
  `validate / validate`, defined in `opencharly/.github`); this repo has **no**
  per-repo candy gate.
- The R10 witness is the disposable `check-local` bed (in `opencharly/charly`),
  run to a fresh `charly update` plus a short iterate.

## Modify this repo

- Edit the `plugin-check:` candy entity, the Go source, and
  `schema/checkroster.cue` **together** — the schema is the single source for the
  plugin's served declaration surface.
- The check ENGINE (Runner + plan walk) lives in `sdk/kit`; keep the plugin the
  command/formatting owner and route host-serving atoms through the existing
  HostBuild seams — no new core symbol crosses the boundary.

## Landing

- PR-only. Every change lands through a pull request; the org-required
  `charly/pr-validator` validates the diff and body and arms native auto-merge on
  PASS. Direct pushes to `main` are blocked.
- History lives in `CHANGELOG/` (written by `tag-on-merge` at merge time); the PR
  body IS the changelog.
- The authoritative rulebook is the umbrella `AGENTS.md` in
  `opencharly/opencharly` and `charly/AGENTS.md` in the charly repo. Do not
  restate its rules here.
