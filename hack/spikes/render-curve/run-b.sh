#!/usr/bin/env bash
# Run B: per-step isolated benchmark runs with peak-RSS accounting, artifact
# extraction, and haproxy -c timing.
set -uo pipefail
SPIKE="${SPIKE:-$(cd "$(dirname "$0")" && pwd)}"
cd "$SPIKE/repo"

for kind in httproute ingress; do
  for n in 300 1000 3000; do
    test="benchmark-${kind}-${n}"
    echo "########## $test ##########"

    # 3 separate processes, 3 render iterations each -> 9 samples + peak RSS
    for r in 1 2 3; do
      /usr/bin/time -v ./bin/haptic benchmark \
        --file "$SPIKE/configs/${kind}.yaml" \
        --test "$test" \
        --iterations 3 \
        --schema-dir "$SPIKE/repo/tests/schemas" \
        > "$SPIKE/raw/runB-${kind}-${n}-r${r}.txt" \
        2> "$SPIKE/raw/runB-${kind}-${n}-r${r}.time"
      grep -E "Maximum resident" "$SPIKE/raw/runB-${kind}-${n}-r${r}.time"
      tail -1 "$SPIKE/raw/runB-${kind}-${n}-r${r}.txt"
    done

    # Rendered artifacts + haproxy -c
    ./bin/haptic validate \
      --file "$SPIKE/configs/${kind}.yaml" \
      --test "$test" \
      --schema-dir "$SPIKE/repo/tests/schemas" \
      --dump-rendered \
      > "$SPIKE/raw/dump-${kind}-${n}.txt" 2>&1
    rm -rf "/tmp/rc/${kind}-${n}"
    python3 "$SPIKE/extract.py" "$SPIKE/raw/dump-${kind}-${n}.txt" "/tmp/rc/${kind}-${n}" \
      > "$SPIKE/raw/sizes-${kind}-${n}.json"
    python3 -c "import json,sys; d=json.load(open(sys.argv[1])); print('sizes:', {k:v for k,v in d.items() if k!='top_maps'})" \
      "$SPIKE/raw/sizes-${kind}-${n}.json"
    bash "$SPIKE/hc.sh" "/tmp/rc/${kind}-${n}" 5 | tee "$SPIKE/raw/hc-${kind}-${n}.txt"
  done
done
