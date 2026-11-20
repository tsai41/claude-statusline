# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A single-binary Go status line for Claude Code. Claude Code invokes it as a
`statusLine` command (`type: command`), pipes one JSON object on **stdin**, and
renders whatever the binary prints to **stdout** (three lines). There is no server,
no long-running process — a fresh process runs per status refresh.

## Commands

- `make build` — build `./claude-statusline`
- `make test` — `go test ./...`; one test: `go test -run TestName ./...`
- `make fmt` / `make vet` — format / vet. `.github/workflows/ci.yml` runs `go vet`, `go test`,
  `go build ./...` on push to main/master and on PRs (ubuntu + macos), so run those three locally
- `make install` — `go install` to `$GOPATH/bin`
- `bash install.sh` — build straight to `~/.claude/statusline-go` (the path `settings.json` points at);
  the file has no exec bit, so run it through `bash`
- `make release-snapshot` — local goreleaser dry run. A real release is a pushed `v*` tag, which
  triggers `.github/workflows/release.yml` (goreleaser; its `before` hooks run `go mod tidy` and `go test ./...`)

Tests (`main_test.go`) are stdlib `testing` only (no testify, no module deps), table-driven with
`t.Run` where several inputs share a shape, failures via `t.Fatalf`. `isolateRenderEnv` points `HOME`,
`CLAUDE_CONFIG_DIR` and the `STATUSLINE_*` vars at empty temp state and pins `nowFn` / `usageSnapshotFn`;
use it for any test that calls `render`. Tests set the parsed flags through `withFlags` instead of
passing argv. `TestRenderDefaultLayoutGolden` locks the default output byte for byte: a
layout change must not be "fixed" by editing its constant.

Manual end-to-end test (feed it the stdin JSON):
```bash
echo '{"cwd":"'"$PWD"'","model":{"display_name":"Test"}}' | ./claude-statusline
```
Add `context_window`, `rate_limits`, `transcript_path`, `session_id` keys to the JSON
to exercise line 2 / line 3.

## Configuration

Flags parsed once in `main()` by `parseFlags` (`flag.NewFlagSet` with `ContinueOnError`, output
discarded: a bad flag is ignored, never fatal, since a failed render blanks the UI). Each has an
env equivalent; **flag wins, then env, then default**, and an absent flag falls through to the env var.

| Flag | Env | Effect |
|---|---|---|
| `--usage-dir <path>` | `STATUSLINE_USAGE_DIR` | enable the usage snapshot (unset = write nothing) |
| `--account-registry <path>` | `STATUSLINE_ACCOUNT_REGISTRY` | ccs registry path; default `~/.config/agent-conversation-handoff/accounts.json` |
| `--no-account` | `STATUSLINE_ACCOUNT` | hide account tag |
| `--no-last-message` | `STATUSLINE_LAST_MESSAGE` | hide the user message on line 3 |
| `--no-last-tool` | `STATUSLINE_LAST_TOOL` | hide the `tool:` line on line 3 |

The three hide switches are bool flags; on the env side `envOff` treats only `0`/`false`/`off`/`no`
(case-insensitive) as off, any other value (including empty) leaves the segment on. The path
options treat an empty value as unset.

Compact toggle: while `<claudeConfigDir>/statusline-compact` exists (`compactModeOn`), line 3 is
not printed and the transcript is not read for it; lines 1-2 and the usage snapshot are unaffected.

`CLAUDE_CONFIG_DIR` is honored through `claudeConfigDir` (fallback `~/.claude`) by the compact
toggle, the account tag, and the usage snapshot's `config_dir`. `readEffortLevel` and
`updateSession` still hardcode `~/.claude`, so a non-default config dir gets effort from, and
session heartbeats in, `~/.claude`.

## Architecture

Everything lives in **`main.go`** (single file, `package main`). Understanding it:

**Pipeline (`main()`):** decode stdin `Input` → resolve `dir` (prefers `input.cwd`,
falls back to `input.workspace.current_dir`) → fan out N goroutines (currently 6), each pushing a
`Result{Type, Data}` into a buffered channel → fan in via `switch r.Type` into local
vars → render three lines joined by `SEP`. The work is in `render(input, out io.Writer)`, which
`main()` calls with stdout; tests call it with a buffer.

**Adding a segment** = five coordinated edits in `main.go`: bump `wg.Add(N)` **and**
`make(chan Result, N)` together in `render`, add the goroutine, add the `case` in the fan-in switch,
append to the `line1` slice in `render` (or to `line2` inside `formatMetricsLine`; guard on
non-empty to hide). Worker funcs
return `string`; **empty string means "hide this segment"** — that's the universal
convention (`getGitBranch`, `readEffortLevel`, `extractLastTool`, `readAccountTag` all follow it).

**The three lines:**
- Line 1 — identity: dir, git (`⚡ branch` + `+staged~unstaged` dirty), `→wt:name` (worktree where recent edits landed), model, effort
- Line 2 — metrics: context bar, 5h/7d rate limits with reset countdown, then the account tag last (`formatMetricsLine`)
- Line 3 — activity: special tool line (Agent/Skill/MCP only), then last user message; hidden by the compact toggle, and each part by its own switch

**Account tag (`readAccountTag`)** is the ccs registry entry whose `home` is this session's
`claudeConfigDir`, rendered `#<number> <alias>` (`#<number>` with no alias). Only `provider == "claude"`
entries count, and the home is matched with `os.SameFile` (not string equality) so symlinks and
trailing slashes still match. No tag when the registry is missing or unparseable, the config dir
can't be stat'd, no entry matches, or it is hidden. It is the last segment of line 2 in the
branch color (`ColorWhite`): leftmost would sit under Claude Code's own mode hint.

**Three JSON sources, do not conflate them:**
1. `Input` struct (`main.go` top) models the **stdin** contract from Claude Code. To
   consume a new stdin field, add it here first — unmodeled fields (e.g.
   `workspace.project_dir`) are silently dropped.
2. Config files on disk: `readAccountTag` reads the ccs accounts registry (see above), not
   `~/.claude.json`; `readEffortLevel` reads `~/.claude/settings.json`. `effortFromSettings`
   prefers `modelSettings[<model id>].effortLevel` over the top-level `effortLevel`, looking up
   `Input.Model.ID` with any `[...]` suffix (e.g. `[1m]`) stripped.
3. `extractLastTool` / `extractUserMessage` parse the **transcript JSONL** file
   (`input.transcript_path`) as `map[string]interface{}`, scanning lines bottom-up and
   filtering on `isSidechain == false` and `sessionId == <this session>` so subagent
   noise and cross-session bleed are excluded.

**Git** is derived by shelling out (`git -C dir ...`), never supplied by Claude Code.
`getGitBranch` gates on `rev-parse --git-dir`, reads branch via `symbolic-ref` (so
**detached HEAD renders no branch**, since the func returns early), and counts dirty via
`status --porcelain`. Result is cached process-wide with a 5s TTL (`gitBranchCache`,
`cacheMutex`) — the cache has **no dir key**, which is safe only because each render is a
fresh process handling one dir.

**Worktree segment (`detectEditWorktree`)** is deliberately *not* cwd-driven. Claude Code
pins the statusline `cwd`/`workspace.current_dir` to the session's launch dir — a Bash
`cd` never moves it (measured), so a cwd-based "am I in a worktree" badge could never fire
in the common flow (session stays in main, edits go to a worktree). Instead this scans the
transcript tail for the newest `Edit`/`Write`/`MultiEdit`/`NotebookEdit` `file_path` and
resolves it via `worktreeName` (`rev-parse --absolute-git-dir` containing `/worktrees/`).
It reports `→wt:<name>` only when that latest edit landed in a linked worktree; older edits
are ignored (most-recent-wins). `worktreeName` is the shared path→worktree resolver.

**Color convention:** ANSI codes are string constants at the top; `modelColors` maps
substrings (Opus/Sonnet/Haiku) to truecolor. When a segment embeds its own color inside
a string that the render block wraps in another color (git branch is wrapped white
`\033[37m…\033[0m`), the embedded color must reset back to the **wrapper's** color, not a
full `ColorReset`, or trailing text loses its color. See how `dirty` and the worktree
badge re-emit `\033[37m` after their green/yellow.

**Session time tracking:** `updateSession` writes a per-session heartbeat JSON under
`~/.claude/session-tracker/sessions/<id>.json` on every render. Nothing in this repo reads it
back or displays it (line 2 no longer shows session time); it is a write side effect for outside
consumers.

**Usage snapshot (`writeUsageSnapshot`)** records `rate_limits` to `<usage dir>/<sha256(config_dir)>.json`
because a live render is the only place that data appears. Contract is the `UsageSnapshot` struct
(`version`, `config_dir`, `checked_at`, optional `five_hour` / `seven_day` as `UsageWindow`
`used_percentage` + `resets_at`); external readers key on `config_dir` and accept only
`version == usageSnapshotVersion` (1), so renaming or retyping a field is breaking and adding an
optional one is not. It runs via `usageSnapshotFn` **after** `render` has printed, outside the
goroutine set that gates output, so a hung filesystem can never delay the status line; keep it
there. Every failure is a silent no-op. Writes are throttled (unchanged windows refresh
`checked_at` only every 5 min) and atomic (fsynced temp file, rename, 0600).

## Deploy contract

The install path Claude Code reads is `~/.claude/statusline-go` (per README + `install.sh`),
wired via the `statusLine` block in `~/.claude/settings.json`. `make install` uses a
different path (`$GOPATH/bin`) — don't confuse the two when verifying a change actually
reaches Claude Code.
