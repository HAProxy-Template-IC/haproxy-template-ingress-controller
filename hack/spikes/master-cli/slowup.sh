#!/bin/sh
# 5-second-slow HTTP upstream on :9100 (one process per connection).
exec socat -T120 TCP-LISTEN:9100,reuseaddr,fork EXEC:/cfg/slowresp.sh
