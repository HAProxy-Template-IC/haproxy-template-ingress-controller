#!/bin/bash
# Block interactive git flags (-i / --interactive) on rebase and add.
# These require interactive input which is not supported.
INPUT=$(cat)
COMMAND=$(echo "$INPUT" | jq -r '.tool_input.command')

if echo "$COMMAND" | grep -qE '\bgit\s+rebase\b.*\s-i\b|\bgit\s+rebase\b.*--interactive'; then
  echo "Blocked: git rebase -i (interactive) is not supported. Use non-interactive rebase." >&2
  exit 2
fi

if echo "$COMMAND" | grep -qE '\bgit\s+add\b.*\s-i\b|\bgit\s+add\b.*--interactive'; then
  echo "Blocked: git add -i (interactive) is not supported. Add files by name instead." >&2
  exit 2
fi

exit 0
