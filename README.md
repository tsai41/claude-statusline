# claude-statusline

[![CI](https://github.com/tsai41/claude-statusline/actions/workflows/ci.yml/badge.svg)](https://github.com/tsai41/claude-statusline/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/tsai41/claude-statusline?display_name=tag&sort=semver)](https://github.com/tsai41/claude-statusline/releases)
[![License](https://img.shields.io/github/license/tsai41/claude-statusline)](LICENSE)
[![Go](https://img.shields.io/github/go-mod/go-version/tsai41/claude-statusline)](go.mod)

A Go-based status line for [Claude Code](https://claude.ai/code).

## Features

- Model name with color (Opus / Sonnet / Haiku)
- Current directory and git branch with dirty status (`+staged~unstaged`)
- Worktree indicator (`→wt:name`) when recent edits land in a linked git worktree
- Context window usage with progress bar
- Rate limit remaining (5h / 7d) with reset countdown
- Effort level indicator
- Special tool usage (Agent, Skill, MCP only)
- Last user message preview
- Account tag naming the account the session runs as: the [ccs](https://github.com/tsai41/agent-conversation-handoff) registry entry whose home is `CLAUDE_CONFIG_DIR` (`#4 orli`), falling back to the first 3 characters before `@` for a home the registry does not know (hide with `STATUSLINE_ACCOUNT=0`, point at a different registry with `STATUSLINE_ACCOUNT_REGISTRY=/path/to/accounts.json`)
- Optional rate-limit usage snapshot written per-account to `STATUSLINE_USAGE_DIR` for another program to read (see below)

## Preview

```
~/go/src/myproject │ ⚡ main +2~1 │ Claude Sonnet 4.6 │ effort:L
#4 orli │ ctx ━━━━━━━┄┄┄ 23% │ 5h:55% (1h22m) │ 7d:92% (19h50m) │ 1h30m
│ tool: Skill(autopilot)
│ last message you sent...
```

Line 1 — location + identity: directory, git branch (with dirty indicator), worktree edit target (`→wt:name`), model, effort level  
Line 2 — metrics: account tag, context usage, rate limit remaining with reset countdown  
Line 3 — activity: special tool calls (Agent / Skill / MCP only), last user message

## Environment variables

| Variable | Effect |
|---|---|
| `STATUSLINE_ACCOUNT` | Set to `0` / `false` / `off` / `no` to hide the account tag |
| `STATUSLINE_ACCOUNT_REGISTRY` | Path to a ccs-format accounts registry, overriding the default `~/.config/agent-conversation-handoff/accounts.json` |
| `STATUSLINE_USAGE_DIR` | When set, write a per-account rate-limit usage snapshot into this directory on every render that carries `rate_limits` (see below) |

### Usage snapshot (`STATUSLINE_USAGE_DIR`)

Rate-limit usage is only ever sent to a live status line render — there is no
API to query it otherwise. Setting `STATUSLINE_USAGE_DIR` makes this the one
place that data gets captured, as a JSON file per account. The directory is
created (including parents) if it doesn't already exist; the writer never
requires the program that reads these files to exist or be running.

```json
{
  "version": 1,
  "config_dir": "/Users/karen/.claude-4",
  "checked_at": "2026-09-04T07:12:33Z",
  "five_hour": {"used_percentage": 55.0, "resets_at": "2026-09-04T09:22:00Z"},
  "seven_day": {"used_percentage": 92.0, "resets_at": "2026-09-05T02:00:00Z"}
}
```

- `config_dir` is `CLAUDE_CONFIG_DIR` (or `~/.claude` when unset), recorded
  as-is — this is what a reader keys its lookup on.
- The filename is the SHA-256 hex digest of `config_dir`, so it is stable
  across runs and collision-free between different config dirs.
- `five_hour` / `seven_day` are each omitted when absent from the render
  input, and `resets_at` is omitted when it can't be parsed into a plausible
  time (including a value that arrives in milliseconds instead of seconds).
- `version` only moves for a change that would make an existing reader
  wrong — a reader accepts nothing but an exact match. Adding a new
  optional field is not such a change, since readers already tolerate
  unknown or absent fields, and keeps `version` at 1.
- The write happens after the status line has already been printed, not on
  the path that gates it, so a slow or hung filesystem here can delay or
  lose that one snapshot but never the printed output.
- A changed value writes immediately. An unchanged value only refreshes
  `checked_at` at most every 5 minutes, to avoid a write on every render for
  data that hasn't moved; an idle session's file still gets touched
  occasionally so a reader doesn't report it as older than it really is.
- Writes are atomic (temp file, fsynced, then renamed, mode `0600`), and a
  successful write also clears out any `.usage-*.tmp` file older than an
  hour left behind by a previous run that was killed mid-write. Any failure
  here — an unwritable directory, a marshal error — is silently skipped; it
  never affects what the status line prints.

## Requirements

- Go 1.21+
- Claude Code

## Install

### Option A — pre-built binary (recommended)

Grab the matching tarball from the [Releases page](https://github.com/tsai41/claude-statusline/releases) (`darwin_arm64`, `darwin_amd64`, `linux_arm64`, or `linux_amd64`), extract, and install:

```bash
tar -xzf claude-statusline_*.tar.gz
xattr -d com.apple.quarantine ./claude-statusline 2>/dev/null || true   # macOS only
chmod +x ./claude-statusline
mkdir -p ~/.claude
mv ./claude-statusline ~/.claude/statusline-go
```

### Option B — build from source

```bash
chmod +x install.sh
./install.sh
```

Either way, add to `~/.claude/settings.json`:

```json
"statusLine": {
  "type": "command",
  "command": "~/.claude/statusline-go",
  "padding": 0
}
```

Restart Claude Code.
