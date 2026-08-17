#!/bin/bash
D="$(cd "$(dirname "$0")" && pwd)"
for v in 3.0 3.1 3.2 3.3 3.4; do bash "$D/run.sh" d_server_keywords.sh "$v"; done
for v in 3.0 3.4; do bash "$D/run.sh" d2_initstate_timing.sh "$v"; done
