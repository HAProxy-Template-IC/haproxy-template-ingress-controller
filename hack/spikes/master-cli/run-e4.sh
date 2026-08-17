#!/bin/bash
D="$(cd "$(dirname "$0")" && pwd)"
for v in 3.4 3.0; do bash "$D/run.sh" e4_deferred_delete_final.sh "$v"; done
