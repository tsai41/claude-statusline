# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A single-binary Go status line for Claude Code. Claude Code invokes it as a
`statusLine` command (`type: command`), pipes one JSON object on **stdin**, and
renders whatever the binary prints to **stdout** (three lines). There is no server,
no long-running process — a fresh process runs per status refresh.

## Commands

- `make build` — build `./claude-statusline`
- `make test` — `go test ./...` (no tests exist yet; `go test` is the harness)
- `make fmt` / `make vet` — format / vet
- `make install` — `go install` to `$GOPATH/bin`
- `./install.sh` — build straight to `~/.claude/statusline-go` (the path `settings.json` points at)
- `make release-snapshot` — local goreleaser dry run

Manual end-to-end test (the only way to exercise rendering — feed it the stdin JSON):
```bash
echo '{"cwd":"'"$PWD"'","model":{"display_name":"Test"}}' | ./claude-statusline
```
Add `context_window`, `rate_limits`, `transcript_path`, `session_id` keys to the JSON
to exercise line 2 / line 3.

## Architecture

Everything lives in **`main.go`** (single file, `package main`). Understanding it:

**Pipeline (`main()`):** decode stdin `Input` → resolve `dir` (prefers `input.cwd`,
falls back to `input.workspace.current_dir`) → fan out N goroutines, each pushing a
`Result{Type, Data}` into a buffered channel → fan in via `switch r.Type` into local
vars → render three lines joined by `SEP`.

**Adding a segment** = five coordinated edits in `main.go`: bump `wg.Add(N)` **and**
`make(chan Result, N)` together, add the goroutine, add the `case` in the fan-in switch,
append to a `lineN` slice in the render block (guard on non-empty to hide). Worker funcs
return `string`; **empty string means "hide this segment"** — that's the universal
convention (`getGitBranch`, `readEffortLevel`, `extractLastTool` all follow it).

**The three lines:**
- Line 1 — identity: dir, git (`⚡ branch` + `(wt) name` for linked worktrees + `+staged~unstaged` dirty), model, effort
- Line 2 — metrics: context bar, 5h/7d rate limits with reset countdown, daily session time
- Line 3 — activity: special tool line (Agent/Skill/MCP only), then last user message

**Two JSON sources, do not conflate them:**
1. `Input` struct (`main.go` top) models the **stdin** contract from Claude Code. To
   consume a new stdin field, add it here first — unmodeled fields (e.g.
   `workspace.project_dir`) are silently dropped.
2. `extractLastTool` / `extractUserMessage` parse the **transcript JSONL** file
   (`input.transcript_path`) as `map[string]interface{}`, scanning lines bottom-up and
   filtering on `isSidechain == false` and `sessionId == <this session>` so subagent
   noise and cross-session bleed are excluded.

**Git** is derived by shelling out (`git -C dir ...`), never supplied by Claude Code.
`getGitBranch` gates on `rev-parse --git-dir`, reads branch via `symbolic-ref` (so
**detached HEAD renders no branch — and no worktree badge**, since the func returns early),
counts dirty via `status --porcelain`, and detects linked worktrees via
`rev-parse --absolute-git-dir` containing `/worktrees/`. Result is cached process-wide
with a 5s TTL (`gitBranchCache`, `cacheMutex`) — the cache has **no dir key**, which is
safe only because each render is a fresh process handling one dir.

**Color convention:** ANSI codes are string constants at the top; `modelColors` maps
substrings (Opus/Sonnet/Haiku) to truecolor. When a segment embeds its own color inside
a string that the render block wraps in another color (git branch is wrapped white
`\033[37m…\033[0m`), the embedded color must reset back to the **wrapper's** color, not a
full `ColorReset`, or trailing text loses its color. See how `dirty` and the worktree
badge re-emit `\033[37m` after their green/yellow.

**Session time tracking:** `updateSession` writes a per-session heartbeat JSON under
`~/.claude/session-tracker/sessions/<id>.json`; `calculateTotalHours` aggregates today's
files across all sessions for the line-2 total and active-session count. This is a write
side effect of every render, independent of the displayed segments.

## Deploy contract

The install path Claude Code reads is `~/.claude/statusline-go` (per README + `install.sh`),
wired via the `statusLine` block in `~/.claude/settings.json`. `make install` uses a
different path (`$GOPATH/bin`) — don't confuse the two when verifying a change actually
reaches Claude Code.
