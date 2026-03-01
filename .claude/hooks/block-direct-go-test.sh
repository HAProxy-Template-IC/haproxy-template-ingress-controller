#!/bin/bash
# Block direct "go test" invocation.
# Rule: Use "make test" instead — the Makefile handles toolchain setup correctly.
INPUT=$(cat)
COMMAND=$(echo "$INPUT" | jq -r '.tool_input.command')

if echo "$COMMAND" | grep -qE '\bgo\s+test\b'; then
  echo "Blocked: Do not run 'go test' directly. Use 'make test' instead." >&2
  exit 2
fi

exit 0
