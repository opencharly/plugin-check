# plugin-check

The `charly check` command family for OpenCharly — served as a charly
`command:check` plugin (compiled-in).

`charly check` is the box/live/run/feature evaluation surface plus the
AI-iteration harness and the R10 disposable bed-runner. The plugin owns the
command end to end: the kong grammar, the plan gathering, the iteration loop, and
the text/json/tap/junit/yaml output.

## What it provides

| Capability | Surface |
|---|---|
| `command:check` | the `charly check` CLI — `box` / `live` / `run` / `feature` / `note` / `scope` / `stop` / … |

## What it owns

The composite host-serving Mechanisms it cannot compute itself stay in core and
are reached through the remaining HostBuild seams (`cli` /
`check-load-plugins` / `check-bed-gpu-prereq`), the plugin-side `loaderkit`
reads, `InvokeProvider(kind:agent)`, and the `charly` reentry. The check ENGINE
itself (the Runner + plan walk + grammar) lives in `sdk/kit` as a library both
the host seam and this plugin import.

`check` is compiled-in because its `Invoke(OpRun)` needs the in-process reverse
channel; the out-of-process path has no reverse channel and errors. The R10
witness is the disposable `check-local` bed run to a fresh `charly update`.

## How to use it

Compose the plugin candy in a box's `candy:` list:

```yaml
- '@github.com/opencharly/plugin-check/candy/plugin-check:<tag>'
```

## Layout

- `candy/plugin-check/` — the plugin module: `plugin.go`, `provider.go`,
  `command.go`, `check_cmd.go`, `run_cmd.go`, `live_cmd.go`, `feature_cmd.go`,
  the bed-runner (`bed_*.go`), `schema/checkroster.cue`, `cmd/serve/main.go`.
- `charly.yml` — the root project manifest (`discover: candy`).
- `.github/workflows/tag-on-merge.yml` — CalVer tag + `CHANGELOG/` on merge.

## Related

- Owning skill: `/charly-check:check` — the `charly check` reference.
- `/charly-internals:agents` — how the beds drive agent grading.
- [`opencharly/charly`](https://github.com/opencharly/charly) — the charly CLI.
