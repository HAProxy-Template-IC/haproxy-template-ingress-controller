#!/bin/bash
# Block --no-verify on git commands.
# Rule: "NEVER use git commit --no-verify" (CLAUDE.md)
INPUT=$(cat)
COMMAND=$(echo "$INPUT" | jq -r '.tool_input.command')

if echo "$COMMAND" | grep -qE '\bgit\b.*--no-verify'; then
  echo "Blocked: --no-verify is not allowed. Fix pre-commit issues instead of bypassing hooks." >&2
  exit 2
fi

exit 0
