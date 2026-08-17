#!/bin/bash
# Cross-version matrix of the master-CLI capabilities the plan depends on.
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-a30.cfg" "$W/a30.cfg"
for VER in 3.0 3.1 3.2 3.3 3.4; do
  start hapF "$VER" a30.cfg || continue
  echo "############ $VER ############"
  printf '  %-28s %s\n' "master '@@1' session mode:"  "$(mc "@@1" | head -1 | tr -d '\n')"
  printf '  %-28s %s\n' "'@1 echo x':"                "$(mc "@1 echo x" | head -1 | tr -d '\n')"
  printf '  %-28s %s\n' "master 'echo x':"            "$(mc "echo x" | head -1 | tr -d '\n')"
  printf '  %-28s %s\n' "'@1 add backend':"           "$(mc "@1 add backend zzz from be-http" | head -1 | tr -d '\n')"
  printf '  %-28s %s\n' "'@1 publish backend':"       "$(mc "@1 publish backend dynA" | head -1 | tr -d '\n')"
  printf '  %-28s %s\n' "'@1 add server help':"       "$(mc "@1 add server help" | head -1 | tr -d '\n')"
  printf '  %-28s %s\n' "wait conditions:"            "$(mc "@1 wait -h" | grep -oE '(srv-removable|be-removable|srv-unused)' | tr '\n' ' ')"
  printf '  %-28s %s\n' "master 'show info':"         "$(mc "show info" | head -1 | cut -c1-40)"
  printf '  %-28s %s\n' "master 'show proc' workers:" "$(mc "show proc" | awk '/^# workers/{f=1;next} f&&NF{print $1" "$2; exit}')"
  printf '  %-28s %s\n' "'@1 show info' Version:"     "$(mc "@1 show info" | grep '^Version' | tr -d '\n')"
  printf '  %-28s %s\n' "'@1 experimental-mode':"     "$(mc "@1 experimental-mode" | head -1 | tr -d '\n')"
  echo "  reload window on the master socket:"
  cx "( sleep 0.05; echo reload | socat -t30 stdio unix-connect:$MSOCK >/dev/null 2>&1 ) &
      f=0; ff=; lf=
      for i in \$(seq 1 500); do
        if echo 'show version' | socat -t1 stdio unix-connect:$MSOCK >/dev/null 2>&1; then :; else
          f=\$((f+1)); t=\$(date +%s%3N); [ -z \"\$ff\" ] && ff=\$t; lf=\$t; fi
      done
      echo \"    failed_connects=\$f  unavailable_window=\$((lf-ff))ms\"
      wait"
  stop
done
