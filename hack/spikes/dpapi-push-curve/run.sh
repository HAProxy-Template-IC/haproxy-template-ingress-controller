#!/usr/bin/env bash
# Drive the whole spike: for each N, measure HAPTIC's Dataplane API push path
# and the plain "write files + master-socket reload" path against the same
# config, then write results/<source>-<N>.json.
#
#   ./run.sh                      # real chart-rendered configs, N=300,1000,3000
#   SOURCE=synthetic ./run.sh     # synthetic configs instead
#   STEPS="300 1000" RUNS=7 ./run.sh
#
# Real configs come from render-real.sh (see its header for the one-time
# generation step). Synthetic ones from gen_synthetic.py.
set -euo pipefail
SPIKE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STEPS=${STEPS:-"300 1000 3000"}
RUNS=${RUNS:-5}
SOURCE=${SOURCE:-real}
mkdir -p "$SPIKE/results" "$SPIKE/work"

for n in $STEPS; do
  src="$SPIKE/gen/${SOURCE}-${n}"
  if [[ "$SOURCE" == "synthetic" && ! -f "$src/haproxy.cfg" ]]; then
    python3 "$SPIKE/gen_synthetic.py" "$n" "$src" >/dev/null
  fi
  [[ -f "$src/haproxy.cfg" ]] || { echo "missing $src/haproxy.cfg" >&2; exit 1; }

  work="$SPIKE/work/${SOURCE}-${n}"
  pristine="$SPIKE/work/${SOURCE}-${n}-pristine"
  out="$SPIKE/results/${SOURCE}-${n}.json"
  rm -f "$out"
  bash "$SPIKE/clean-workdir.sh" "$work"
  bash "$SPIKE/clean-workdir.sh" "$pristine"
  python3 "$SPIKE/prepare.py" "$src" "$work" >/dev/null
  python3 "$SPIKE/prepare.py" "$src" "$pristine" >/dev/null

  echo "### $SOURCE N=$n — dataplane API phase"
  bash "$SPIKE/container.sh" up "$work" with-dpapi
  python3 "$SPIKE/measure.py" --workdir "$work" --runs "$RUNS" --phase dpapi --out "$out" >/dev/null

  echo "### $SOURCE N=$n — plain write + master-socket reload phase"
  bash "$SPIKE/clean-workdir.sh" "$work"
  python3 "$SPIKE/prepare.py" "$src" "$work" >/dev/null
  bash "$SPIKE/container.sh" up "$work" no-dpapi
  python3 "$SPIKE/measure.py" --workdir "$work" --srcdir "$pristine" --runs "$RUNS" \
      --phase plain --out "$out" >/dev/null
  bash "$SPIKE/container.sh" down
  python3 "$SPIKE/report.py" "$out"
done
