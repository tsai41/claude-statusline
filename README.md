# claude-statusline

A Go-based status line for [Claude Code](https://claude.ai/code).

## Features

- Model name with color (Opus / Sonnet / Haiku)
- Current directory and git branch with dirty status (`+staged~unstaged`)
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

Line 1 — location + identity: directory, git branch (with dirty indicator), model, effort level  
Line 2 — metrics: context usage, rate limit remaining with reset countdown, session time  
Line 3 — activity: special tool calls (Agent / Skill / MCP only), last user message

## Requirements

- Go 1.21+
- Claude Code

## Install

```bash
chmod +x install.sh
./install.sh
```

Then add to `~/.claude/settings.json`:

```json
"statusLine": {
  "type": "command",
  "command": "~/.claude/statusline-go",
  "padding": 0
}
```

Restart Claude Code.
