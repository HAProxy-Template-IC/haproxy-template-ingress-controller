#!/bin/bash
# Block direct invocation of linting/security tools.
# Rule: Use "make lint" or "make check-all" instead (CLAUDE.md)
INPUT=$(cat)
COMMAND=$(echo "$INPUT" | jq -r '.tool_input.command')

if echo "$COMMAND" | grep -qE '\bgolangci-lint\b'; then
  echo "Blocked: Do not run golangci-lint directly. Use 'make lint' instead." >&2
  exit 2
fi

if echo "$COMMAND" | grep -qE '\bgovulncheck\b'; then
  echo "Blocked: Do not run govulncheck directly. Use 'make check-all' instead." >&2
  exit 2
fi

exit 0
