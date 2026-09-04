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
- Account tag naming the account the session runs as: the [ccs](https://github.com/tsai41/agent-conversation-handoff) registry entry whose home is `CLAUDE_CONFIG_DIR` (`#4 orli`) — no tag shown when the registry doesn't list this account, or is missing or unreadable (hide explicitly with `--no-account` / `STATUSLINE_ACCOUNT=0`, point at a different registry with `--account-registry` / `STATUSLINE_ACCOUNT_REGISTRY`)
- Optional rate-limit usage snapshot written per-account to a directory set via `--usage-dir` / `STATUSLINE_USAGE_DIR` for another program to read (see below)

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

## Configuration

Every setting below is available as both a command-line flag and an
environment variable. **Precedence: flag > environment variable > default.**
A flag that isn't passed changes nothing — the matching environment variable
(or the built-in default, if that's unset too) still applies. With neither
set, the status line prints exactly as it always has and writes nothing to
disk.

Since `settings.json`'s `command` string runs through a shell, flags placed
right in that string reach this program — no separate config file needed.

An unrecognized or malformed flag is ignored rather than treated as fatal:
this program's output is a status line embedded in Claude Code's UI, so
exiting on a bad flag would leave that UI blank with no way to see why.

| Flag | Environment variable | Effect |
|---|---|---|
| `--usage-dir <path>` | `STATUSLINE_USAGE_DIR` | Write a per-account rate-limit usage snapshot into this directory on every render that carries `rate_limits` (see below) |
| `--account-registry <path>` | `STATUSLINE_ACCOUNT_REGISTRY` | Path to a ccs-format accounts registry, overriding the default `~/.config/agent-conversation-handoff/accounts.json` |
| `--no-account` | `STATUSLINE_ACCOUNT` set to `0` / `false` / `off` / `no` | Hide the account tag |

### Usage snapshot (`--usage-dir` / `STATUSLINE_USAGE_DIR`)

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

To also enable usage snapshots, pass `--usage-dir` right in the command
string. The path below is where [ccs](https://github.com/tsai41/agent-conversation-handoff)
looks by default, so the two need no further configuration to meet:

```json
"statusLine": {
  "type": "command",
  "command": "~/.claude/statusline-go --usage-dir ~/.config/agent-conversation-handoff/usage",
  "padding": 0
}
```

Restart Claude Code.
