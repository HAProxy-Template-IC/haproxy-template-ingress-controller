#!/bin/bash
# Block //nolint directives in Edit tool changes.
# Rule: "NEVER use //nolint directives" (CLAUDE.md)
INPUT=$(cat)
NEW_STRING=$(echo "$INPUT" | jq -r '.tool_input.new_string // empty')

if echo "$NEW_STRING" | grep -qE '//\s*nolint'; then
  echo "Blocked: //nolint directives are not allowed. Fix linting issues by refactoring code." >&2
  exit 2
fi

exit 0
