#!/bin/sh
# Runs a command up to four times with a growing pause. For registry access in
# CI: container registries reset connections transiently.
# Usage: scripts/retry.sh <command> [args...]
attempt=1
until "$@"; do
	if [ "$attempt" -ge 4 ]; then
		echo "retry.sh: '$1 $2' failed $attempt times" >&2
		exit 1
	fi
	echo "retry.sh: '$1 $2' failed (attempt $attempt), retrying in $((attempt * 10))s" >&2
	sleep $((attempt * 10))
	attempt=$((attempt + 1))
done
