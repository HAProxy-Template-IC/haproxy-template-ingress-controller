#!/usr/bin/env bash
# Does the Dataplane API store the bytes it was handed? Push a config, then
# compare what landed on disk with what was pushed.
set -euo pipefail
SPIKE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
work="$SPIKE/work/roundtrip"
bash "$SPIKE/clean-workdir.sh" "$work"
python3 "$SPIKE/prepare.py" "$SPIKE/gen/real-300" "$work" >/dev/null
cp "$work/haproxy.cfg" "$SPIKE/work/roundtrip-pushed.cfg"
bash "$SPIKE/container.sh" up "$work" with-dpapi >/dev/null
v=$(curl -su admin:admin http://127.0.0.1:5555/v3/services/haproxy/configuration/version)
curl -su admin:admin -X POST \
  "http://127.0.0.1:5555/v3/services/haproxy/configuration/raw?skip_reload=true&version=$v" \
  -H 'Content-Type: text/plain' --data-binary "@$SPIKE/work/roundtrip-pushed.cfg" -o /dev/null -w 'push: %{http_code}\n'
echo "pushed : $(stat -c%s "$SPIKE/work/roundtrip-pushed.cfg") bytes, $(wc -l < "$SPIKE/work/roundtrip-pushed.cfg") lines"
echo "on disk: $(stat -c%s "$work/haproxy.cfg") bytes, $(wc -l < "$work/haproxy.cfg") lines"
echo "--- first 5 differing lines ---"
diff <(grep -v '^# _version=' "$work/haproxy.cfg") "$SPIKE/work/roundtrip-pushed.cfg" | head -12 || true
bash "$SPIKE/container.sh" down >/dev/null
