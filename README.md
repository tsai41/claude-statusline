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
- Daily session time tracking

## Preview

```
~/go/src/myproject │ ⚡ main +2~1 │ Claude Sonnet 4.6 │ effort:L
ctx ━━━━━━━┄┄┄ 23% │ 5h:55% (1h22m) │ 7d:92% (19h50m) │ 1h30m
│ tool: Skill(autopilot)
│ last message you sent...
```

Line 1 — location + identity: directory, git branch (with dirty indicator), worktree edit target (`→wt:name`), model, effort level  
Line 2 — metrics: context usage, rate limit remaining with reset countdown, session time  
Line 3 — activity: special tool calls (Agent / Skill / MCP only), last user message

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
