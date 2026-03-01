#!/bin/bash
# Block //nolint directives in Write tool content.
# Rule: "NEVER use //nolint directives" (CLAUDE.md)
INPUT=$(cat)
CONTENT=$(echo "$INPUT" | jq -r '.tool_input.content // empty')

if echo "$CONTENT" | grep -qE '//\s*nolint'; then
  echo "Blocked: //nolint directives are not allowed. Fix linting issues by refactoring code." >&2
  exit 2
fi

exit 0
