#!/usr/bin/env python3
"""Send stdin verbatim to a HAProxy CLI unix socket, print the full reply.

Full-duplex: writing and reading are interleaved with select(), so a large
payload cannot deadlock against HAProxy's reply filling the socket buffer.
That deadlock is a property of the *client*, and it would otherwise be
indistinguishable from HAProxy truncating the payload.

Usage: hapcli.py <socket-path> [idle-timeout-seconds]
"""
import os
import select
import socket
import sys

path = sys.argv[1] if len(sys.argv) > 1 else os.environ["HAPSOCK"]
idle = float(sys.argv[2]) if len(sys.argv) > 2 else 15.0
data = sys.stdin.buffer.read()

s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.connect(path)
s.setblocking(False)

out = sys.stdout.buffer
sent = 0
wr_done = False
while True:
    rlist = [s]
    wlist = [] if wr_done else [s]
    r, w, _ = select.select(rlist, wlist, [], idle)
    if not r and not w:
        sys.stderr.write("hapcli: idle timeout after %ss (sent %d/%d bytes)\n"
                         % (idle, sent, len(data)))
        break
    if w:
        try:
            sent += s.send(data[sent:sent + (1 << 16)])
        except BlockingIOError:
            pass
        except (BrokenPipeError, ConnectionResetError):
            wr_done = True
        if sent >= len(data) and not wr_done:
            try:
                s.shutdown(socket.SHUT_WR)
            except OSError:
                pass
            wr_done = True
    if r:
        try:
            chunk = s.recv(1 << 16)
        except (ConnectionResetError, BlockingIOError):
            break
        if not chunk:
            break
        out.write(chunk)
out.flush()
s.close()
