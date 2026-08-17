#!/bin/bash
# Question A, part 4:
#  - one command per connection => unambiguous pairing of the teardown messages
#  - startup race: master socket up, worker not yet registered (readiness probe)
set -u
. "$(dirname "$0")/lib.sh"
cp "$SPIKE_DIR/cfg-a.cfg" "$W/a.cfg"
VER="${1:-3.4}"

hr "A18. STARTUP RACE — connect to the master socket as early as possible"
C=hapA4
docker rm -f $C >/dev/null 2>&1
docker run -d --name $C -v "$W:/cfg" --entrypoint haproxy "haproxytech/haproxy-debian:$VER" \
  -dr -W -db -S "$MSOCK,level,admin" -- /cfg/a.cfg >/dev/null
cx "for i in \$(seq 1 60); do
  o=\$( (echo '@1 show info' | socat -t1 stdio unix-connect:$MSOCK) 2>&1 | head -1 )
  p=\$( (echo 'show proc' | socat -t1 stdio unix-connect:$MSOCK) 2>&1 | awk '/^# workers/{f=1;next} f&&NF{print \"worker=\"\$1; exit} END{if(!f) print \"noproc\"}' )
  echo \"[\$i] show_proc:\$p   @1_show_info_first_line:'\$o'\"
  case \"\$o\" in Name:*) break;; esac
  sleep 0.05
done"

hr "A18b. 'show proc' output while the worker is NOT yet there (from the loop above)"
echo "(see [1].. lines: 'noproc' means the '# workers' section was absent/empty)"

# make sure it is fully up now
for _ in $(seq 1 50); do mc "@1 show info" | grep -q '^Name:' && break; sleep 0.1; done

hr "A19. teardown, ONE command per connection — unambiguous message pairing"
mc "@1 add backend dynP from be-http mode http"
mc "@1 add server dynP/s1 127.0.0.1:9000 check init-state up"
mc "@1 enable health dynP/s1"
mc "@1 enable server dynP/s1"
mc "@1 publish backend dynP"
cx "curl -s -o /dev/null -H 'x-be: dynP' http://127.0.0.1:8080/"
for c in "unpublish backend dynP" "disable server dynP/s1" "shutdown sessions server dynP/s1" \
         "wait 3s srv-removable dynP/s1" "del server dynP/s1" "wait 3s be-removable dynP" "del backend dynP"; do
  echo "--- @1 $c"
  MC_T=10 mc "@1 $c" | cat -A
done

hr "A19b. same but the backend still HAS a server when 'del backend' runs"
mc "@1 add backend dynS from be-http mode http"
mc "@1 add server dynS/s1 127.0.0.1:9000 check init-state up"
mc "@1 enable health dynS/s1"
mc "@1 enable server dynS/s1"
mc "@1 publish backend dynS"
for c in "unpublish backend dynS" "wait 3s be-removable dynS" "del backend dynS"; do
  echo "--- @1 $c"
  MC_T=10 mc "@1 $c" | cat -A
done
echo "--- @1 show backend"; mc "@1 show backend"

stop
