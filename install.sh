#!/bin/bash
set -e

BINARY="$HOME/.claude/statusline-go"

echo "Building statusline..."
go build -o "$BINARY" main.go
chmod +x "$BINARY"

echo "Done. Binary at $BINARY"
echo ""
echo "Add to ~/.claude/settings.json:"
echo '  "statusLine": {'
echo '    "type": "command",'
echo '    "command": "~/.claude/statusline-go",'
echo '    "padding": 0'
echo '  }'
