#!/bin/bash
D="$(cd "$(dirname "$0")" && pwd)"
for v in 3.0 3.4; do bash "$D/run.sh" e_deferred_delete.sh "$v"; done
for v in 3.0 3.4; do bash "$D/run.sh" e2_wait_semantics.sh "$v"; done
