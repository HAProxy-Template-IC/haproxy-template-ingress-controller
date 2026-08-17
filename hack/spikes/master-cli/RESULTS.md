# HAProxy master-socket spike — results

Every container is started exactly like the shipped HAPTIC pod
(`charts/haptic/templates/haproxy-deployment.yaml`), with **no worker `stats socket`**:

```
haproxy -dr -W -db -S /etc/haproxy/haproxy-master.sock,level,admin -- /cfg/<config>
```

All CLI traffic in this document goes through `/etc/haproxy/haproxy-master.sock`.
Images: `haproxytech/haproxy-debian:{3.0.26, 3.1.17, 3.2.22, 3.3.13, 3.4.3}`.

Scripts and raw outputs live next to this file; `out/<script>-<version>.txt` is the
verbatim log of each run.

| script | question |
|---|---|
| `a_framing.sh`, `a2_framing_deep.sh`, `a3_response_pairing.sh`, `a4_pairing_and_readiness.sh`, `a5_v30.sh` | A |
| `b_inheritance.sh` | B |
| `c_defaults_warning.sh` | C |
| `d_server_keywords.sh`, `d2_initstate_timing.sh`, `summarize-d.sh` | D |
| `e4_deferred_delete_final.sh`, `e2_wait_semantics.sh`, `e3_wait_needs_open_conn.sh` | E |
| `f_version_matrix.sh`, `g_publish.sh` | cross-cutting |

Run any of them with `bash run.sh <script.sh> <version>` (tees to `out/`).
`gen-certs.sh` builds the CA/cert/CRL that question D needs, `verify-quotes.sh` asserts
that every string quoted below appears verbatim in `out/` (it passes), and `cleanup.sh`
removes the containers.

---

## A. Framing

### A.1 What the master CLI itself can do

`out/a_framing-3.4.txt`, section A0 — `printf 'help\n' | socat -t5 stdio unix-connect:$MSOCK`:

```
The following commands are valid at this level:
  @!<pid>                                 : send a command to the <pid> process
  @<relative pid>                         : send a command to the <relative pid> process
  @master                                 : send a command to the master process
  echo <text>                             : print text to the output
  hard-reload                             : achieve a hard-reload (-st) of haproxy
  operator                                : lower the level of the current CLI session to operator
  reload                                  : achieve a soft-reload (-sf) of haproxy
  show cli level                          : display the level of the current CLI session
  show cli sockets                        : dump list of cli sockets
  show env [var]                          : dump environment variables known to the process
  show proc                               : show processes status
  show startup-logs                       : report logs emitted during HAProxy startup
  show version                            : show version of the current process
  user                                    : lower the level of the current CLI session to user
  help [<command>]                        : list matching or all commands
  prompt [help | n | i | p | timed ]*     : toggle interactive mode with prompt
  quit                                    : disconnect
```

`show info` is **not** a master command:

```
$ printf 'show info\n' | socat -t5 stdio unix-connect:/etc/haproxy/haproxy-master.sock
Unknown command: 'show', but maybe one of the following ones is a better match:
  show cli level                          : display the level of the current CLI session
  ...
```

`@1 show info` works:

```
$ printf '@1 show info\n' | socat -t5 stdio unix-connect:/etc/haproxy/haproxy-master.sock
Name: HAProxy
Version: 3.4.3-80ea565fd
Release_date: 2026/07/29
Nbthread: 16
Nbproc: 1
Process_num: 1
Pid: 14
Uptime: 0d 0h00m00s
```

`add server` / `add backend` addressed to the master (no `@1`) is rejected:

```
$ printf 'add server x/y 1.2.3.4:80\n' | socat …
Unknown command: 'add', but maybe one of the following ones is a better match: …
```

### A.2 The four framings, measured

`out/a_framing-3.4.txt` (A1–A4) and `out/a2_framing_deep-3.4.txt` (A10a–A10e).

| # | framing | multi-command sequence | session state (`experimental-mode`, `set severity-output`) |
|---|---|---|---|
| 1 | one connection per command, each `@1 <cmd>` | works | **lost between commands** |
| 2 | `@@1\n` + **one** line, commands joined by semicolon-space | works | **kept for the whole line** |
| 3 | one connection, one line `@1 c1; c2; c3` | **BROKEN** | – |
| 4 | one connection, `@1 <cmd>` per line | works | **lost between lines** |
| 5 | `@@1\n` + newline-separated commands | **only the first line runs** | – |
| 6 | `@@1\nprompt\n` + newline-separated commands | works | kept |

**Framing 3 is the trap.** `@1` binds only to the *first* `;`-separated command; the
rest are executed by the **master**:

```
$ printf '@1 echo MARK-1; echo MARK-2; add backend dynC from be-http mode http guid be:dynC\n' | socat …
MARK-1

MARK-2

Unknown command: 'add', but maybe one of the following ones is a better match:
  @master                                 : send a command to the master process
  echo <text>                             : print text to the output
  hard-reload                             : achieve a hard-reload (-st) of haproxy
  …
```

(`show backend` afterwards does not list `dynC`, and `curl -H 'x-be: dynC'` returns 503.)

**Framing 5 silently truncates.** `@@1` followed by newline-separated commands runs
only the first one:

```
$ printf '@@1\necho LINE-1\necho LINE-2\necho LINE-3\n' | socat …
LINE-1
```

Adding `prompt` fixes it (framing 6), at the cost of a "> " prompt prefix on every response:

```
$ printf '@@1\nprompt\necho LINE-1\necho LINE-2\necho LINE-3\nquit\n' | socat …

> LINE-1

> LINE-2

> LINE-3

>
```

**Framing 2 is the one to use.** The full create sequence in a single connection:

```
$ printf '@@1\nexperimental-mode on; add backend dynB from be-http mode http guid be:dynB; add server dynB/s1 127.0.0.1:9000 check init-state up; enable health dynB/s1; enable server dynB/s1; publish backend dynB\n' | socat -t20 …

New backend registered.

New server registered.



Backend published.
```

`curl -H 'x-be: dynB'` → `200`. Whole round trip: **40 ms**.

The delete sequence, same framing (markers inserted with `echo`):

```
$ printf '@@1\necho M1; unpublish backend dynQ; echo M2; disable server dynQ/s1; echo M3; shutdown sessions server dynQ/s1; echo M4; wait 3s srv-removable dynQ/s1; echo M5; del server dynQ/s1; echo M6; wait 3s be-removable dynQ; echo M7; del backend dynQ; echo M8\n' | socat -t20 …
M1
Backend unpublished.
M2
                                  <- disable server: empty response
M3
                                  <- shutdown sessions server: empty response
M4
Done.                             <- wait 3s srv-removable
M5
Server deleted.
M6
Done.                             <- wait 3s be-removable
M7
Server dynQ/s1 is going DOWN for maintenance. 0 active and 0 backup servers left. 0 sessions active, 0 requeued, 0 remaining in queue.
backend 'dynQ' has no server available!
Server deleted.
Backend deleted.
M8
```

Whole round trip: **38 ms**.

**Response-pairing hazard.** As the markers show, `del backend` answers with a
**four-line block**, and three of those lines are about a server that `del server`
already reported deleted. Confirmed unambiguously with one command per connection
(`out/a4_pairing_and_readiness-3.4.txt`, A19):

```
--- @1 del server dynP/s1
Server deleted.$
--- @1 wait 3s be-removable dynP
Done.$
--- @1 del backend dynP
Server dynP/s1 is going DOWN for maintenance. 0 active and 0 backup servers left. 0 sessions active, 0 requeued, 0 remaining in queue.$
backend 'dynP' has no server available!$
Server deleted.$
Backend deleted.$
```

A client that assumes "one line of response per command" desynchronises here. Key on
the *last* line of each command's response, or use `echo` markers.

### A.3 `experimental-mode` — per session, and there is no session under `@1`

The probe is the state query itself (`out/a_framing-3.4.txt`, A5):

```
(a) two separate connections: '@1 experimental-mode on' then '@1 experimental-mode'
    -> experimental-mode is OFF
(b) ONE connection, '@1' per line: 'on' then query
    -> experimental-mode is OFF
(d) ONE connection, '@@1' then 'experimental-mode on; experimental-mode'
    -> experimental-mode is ON
(e) fresh connection afterwards
    -> experimental-mode is OFF
```

So: **per session**, and a session only exists under `@@1`. Under `@1` every command —
even consecutive `@1` lines in the same TCP connection — is its own worker CLI session.

On **3.4 none of the commands in the plan need experimental-mode at all**. `@1 add
backend …` succeeds from a connection that never sent `experimental-mode on`, and the
worker's `help` does not flag `add backend` / `del backend` / `add server` /
`del server` as experimental (`out/help-worker-3.4.txt`).

### A.4 `wait` — it only waits under `@@1` with the connection held open

This is the sharpest finding of the spike (`out/e3_wait_needs_open_conn-3.4.txt`,
`out/e4_deferred_delete_final-3.4.txt`).

`wait` is aborted as soon as the CLI client closes its write side. The
`printf … | socat` idiom does exactly that, so `wait` degrades into an *instantaneous
probe*:

```
V1. '@1 wait 3s', write side closed         -> Interrupted. (null)      after 2 ms
V2. '@1 wait 3s', write side held open      -> Interrupted. (null)
V3. '@@1' + 'wait 3s', write side held open -> Done.                    after 3 s
```

Under `@1` it never waits, not even with the connection held open
(`out/e4_deferred_delete_final-3.4.txt`, E-j):

```
E-j  --- at1open: wait 10s srv-removable j/s1
     t+7ms  [Interrupted. Server still has connections attached to it, cannot remove it.]
```

Under `@@1` with the write side held open it really waits
(E-d, in-flight request against an upstream that sleeps 5 s):

```
E-d  --- at2open: wait 10s srv-removable d/s1
     t+4640ms  [Done.]
```

and it really expires (E-e, same scenario with a 2 s budget):

```
E-e  --- at2open: wait 2s srv-removable e/s1
     t+2009ms  [Wait delay expired. Server still has connections attached to it, cannot remove it.]
```

**On 3.0 and 3.1 `@@1` does not exist**, so through the master socket `wait` can
*never* actually wait on those versions — it is only ever a one-shot readiness probe.

`wait -h` is out of date: it lists only `srv-removable` on every version including 3.4,
yet `be-removable` works there (`out/g_publish-3.4.txt`, G3 — `wait 2s be-removable
pubX` → `Failed. Backend must be unpublished prior to its deletion.`, i.e. parsed and
evaluated). An unknown condition prints the usage block instead
(`wait 2s bogus-condition pubX`).

### A.5 Exact strings

Success (3.4, all verbatim):

| command | response |
|---|---|
| `add backend <b> from <def>` | `New backend registered.` |
| `add server <b>/<s> <addr>` | `New server registered.` |
| `enable health`, `enable server`, `disable server`, `shutdown sessions server`, `set server … health up`, `set server … state ready\|maint` | *(empty line)* |
| `publish backend <b>` | `Backend published.` |
| `unpublish backend <b>` | `Backend unpublished.` |
| `wait <n> srv-removable <b>/<s>` (met) | `Done.` |
| `wait <n> be-removable <b>` (met) | `Done.` |
| `del server <b>/<s>` | `Server deleted.` |
| `del backend <b>` | 4 lines ending in `Backend deleted.` (see A.2) |
| `reload` (master) | `Success=1` + `--` + the new worker's startup log |

Failure:

| situation | response |
|---|---|
| duplicate backend name | `Error during backend creation : name is already used by other proxy 'backend dynA'.` |
| backend name clashes with a frontend | `Error during backend creation : name is already used by other proxy 'frontend fe'.` |
| duplicate `guid` | `Error during backend creation : GUID insertion : duplicate entry with backend dynA.` |
| `add backend` without `from` | `Usage: add backend <name> from <defproxy>.` |
| duplicate server name | `Already exists a server with the same name in backend.` |
| `add server` into an unknown backend | `No such backend.` |
| `add server` with only `<b>/<s>` | `'server' expects <name> and <addr>[:<port>] as arguments.` |
| `add server` bare | `Require 'backend/server'.` |
| unknown server keyword | `unknown keyword 'no-such-keyword-here'.` |
| `del server` on a live server | `Only servers in maintenance mode can be deleted.` |
| `del server` while a stream is attached | `Server still has connections attached to it, cannot remove it.` |
| `del backend` still published | `Backend must be unpublished prior to its deletion.` |
| `del backend` that still has servers | `Only a backend without server can be deleted.` |
| `wait … srv-removable` on a live server | `Failed. Only servers in maintenance mode can be deleted.` |
| `wait … srv-removable`, stream attached, budget exhausted | `Wait delay expired. Server still has connections attached to it, cannot remove it.` |
| `wait …`, client closed the write side | `Interrupted. <reason>` (or `Interrupted. (null)` for a bare `wait <delay>`) |
| `wait … be-removable` still published | `Failed. Backend must be unpublished prior to its deletion.` |
| `wait … be-removable` with servers left | `Failed. Only a backend without server can be deleted.` |
| unknown worker index | `Can't find the target PID matching the prefix '@9'` |
| `@@1` on 3.0/3.1 | `Can't find the target PID matching the prefix '@@1'` |
| line longer than `tune.bufsize` | `The command line is too big for the buffer size. Please change 'tune.bufsize' in the configuration to use a bigger command.` |
| unknown command | `Unknown command: '<x>', but maybe one of the following ones is a better match:` + a suggestion block |

### A.6 `set severity-output`

Only meaningful under `@@1` (framing 2), because under `@1` it does not survive to the
next command. `out/a2_framing_deep-3.4.txt`, A11 — same four commands, three settings
(`cat -A`, so `$` marks end of line):

```
--- BEFORE (no severity-output):
New backend registered.$
$
Error during backend creation : name is already used by other proxy 'backend sevA'.$
$
New server registered.$
$
$
plain-echo$
$

--- AFTER ('set severity-output number' first in the same session):
$
[6]: New backend registered.$
$
[3]: Error during backend creation : name is already used by other proxy 'backend sevB'.$
$
[6]: New server registered.$
$
$
[6]: plain-echo$
$

--- AFTER ('set severity-output string'):
$
[info]: New backend registered.$
$
[err]: Error during backend creation : name is already used by other proxy 'backend sevC'.$
$
[info]: New server registered.$
$
$
[info]: plain-echo$
$
```

So every non-empty response line gains a "[<n>]: " / "[<level>]: " prefix — including
successes (`[6]`/`[info]`) and `echo` output. **Empty responses stay empty** (no
prefix), and multi-line dumps are not prefixed line-by-line:

```
--- 'show backend' with severity-output number:
$
# name$
nope$
sevA$
…
```

Client-native's habit of prefixing `set severity-output number; <cmd>` on one line is
**incompatible with `@1` framing** — the `<cmd>` half would be executed by the master.
It must become `@@1\nset severity-output number; <cmd>`.

### A.7 Readiness

`show proc` works on the master and is the natural probe:

```
$ printf 'show proc\n' | socat …
#<PID>          <type>          <reloads>       <uptime>        <version>
1               master          0 [failed: 0]   0d00h00m01s     3.4.3-80ea565fd
# workers
14              worker          0               0d00h00m01s     3.4.3-80ea565fd
```

But it is **not sufficient**. Right after start, the master socket accepts connections
and `show proc` already lists the worker while `@1` still fails
(`out/a4_pairing_and_readiness-3.4.txt`, A18):

```
[1] show_proc:worker=14   @1_show_info_first_line:'Can't find the target PID matching the prefix '@1''
[2] show_proc:worker=14   @1_show_info_first_line:'Name: HAProxy'
```

Use `@1 show info` returning `Name: HAProxy` as the gate; that proves the worker CLI is
actually serving.

### A.8 Reload

`reload` is **synchronous** on the master socket. It returns only after the new worker
has loaded, and its response embeds the whole startup log (elapsed 134 ms on 3.4):

```
$ printf 'reload\n' | socat -t30 …
Success=1
--
[NOTICE]   (1) : Initializing new worker (403)
[NOTICE]   (403) : Automatically setting global.maxconn to 524251.
[NOTICE]   (403) : haproxy version is 3.4.3-80ea565fd
[NOTICE]   (403) : path to executable is /usr/local/sbin/haproxy
[WARNING]  (403) : [haproxy.main()] HAProxy was started as root without any 'chroot' directive. …
[NOTICE]   (1) : Loading success.
```

Immediately afterwards `@1 show info` works and reports the **new** worker PID.

**During** the reload the master socket is refused, because the master re-execs and
recreates the unix socket. Connect attempts in a tight loop
(`out/a3_response_pairing-3.4.txt` A17, `out/f_version_matrix-all.txt`):

| version | failed connects | unavailable window |
|---|---|---|
| 3.0 | 19–55 of 400–500 | 134 ms … 529 ms |
| 3.1 | 18 | 131 ms |
| 3.2 | 18 | 43 ms |
| 3.3 | 21 | 52 ms |
| 3.4 | 19 | 42 ms |

The failure is a plain refusal, not a hang:

```
[2] rc=1  first-line='2026/08/17 17:26:08 socat[212] E connect(, AF=1 "/etc/haproxy/haproxy-master.sock", 34): Connection refused'
```

**Runtime state pushed while a reload is in flight is silently lost.** In
`out/a3_response_pairing-3.4.txt` A16, fifteen `add backend rN; publish backend rN`
sequences were fired during a reload; the first four hit `Connection refused`, and
`r5..r9` reported `New backend registered.` / `Backend published.` but were gone
afterwards — only `r10..r15` survived, because the earlier ones landed in the worker
that the reload replaced. Data-plane pushes must therefore be fenced against reloads.

### A.9 Command-line length ceiling

One `@@1` line holds roughly `tune.bufsize` (16 KiB default) bytes
(`out/a2_framing_deep-3.4.txt`, A13):

```
N=10    line=535     bytes  registered=10    saw_end=1
N=50    line=2655    bytes  registered=50    saw_end=1
N=100   line=5407    bytes  registered=100   saw_end=1
N=200   line=10907   bytes  registered=200   saw_end=1
N=400   line=21907   bytes  registered=0     saw_end=0  other=The command line is too big for the buffer size. Please change 'tune.bufsize' in the configuration to use a bigger command.
N=800   line=43907   bytes  registered=0     saw_end=0  (same message)
N=1600  line=90109   bytes  registered=0     saw_end=0  (same message)
```

It is **all-or-nothing** — at 400 commands *nothing* was applied. A batching client must
chunk to well under 16 KiB per line (about 200 `add server` commands), or raise
`tune.bufsize`.

### A.10 3.0 differences (`out/a5_v30-3.0.txt`, `out/f_version_matrix-all.txt`)

* `@@<n>` does **not** exist (3.0 and 3.1) → `Can't find the target PID matching the prefix '@@1'`.
  Consequently: no multi-command session, no `experimental-mode` carry-over, no
  `set severity-output` carry-over, and `wait` can never wait.
* `echo` does not exist on 3.0 at all (master or worker) → no marker trick. It exists from 3.1.
* No `add backend` / `del backend` / `publish backend` / `unpublish backend`.
* `wait -h` lists only `srv-removable`; `be-removable` is rejected with the usage text.
* `wait 3s srv-removable <missing>` answers a bare `Failed.` (3.4 adds the reason).

### A — one-line answer

Use **`@@1` + one `;`-joined line** (3.2+); it is the only framing that carries a
multi-command sequence *and* session state (`experimental-mode` is per-session, and
under `@1` every command is its own session), `wait` only really waits under `@@1` with
the connection's write side held open, `show info` is worker-only (`@1 show info`, which
is also the correct readiness gate — `show proc` alone is racy), the master socket is
*refused* for ~42 ms (3.4) to ~500 ms (3.0) during a reload and runtime state pushed to
the outgoing worker is silently lost — and on 3.0/3.1 `@@` does not exist at all, so
there only one-command-per-connection `@1` works.

---

## B. Inheritance of a named `defaults` by a runtime-created backend

Config (`cfg-b.cfg`):

```
defaults plain
    mode http
    timeout connect 5s
    timeout client 30s
    timeout server 20s

defaults be-http from plain
    http-request set-header X-Prof yes
    http-request set-timeout server 2s
    http-request return status 503 content-type text/plain string denied-by-profile if { var(txn.deny) -m found }

frontend fe from plain
    bind :8080
    http-request set-var(txn.be) req.hdr(x-be)
    http-request set-var(txn.deny) req.hdr(x-deny) if { req.hdr(x-deny) -m found }
    use_backend %[var(txn.be)] if { var(txn.be) -m found }
```

Two upstreams: `:9000` echoes the request's `X-Prof` back as the response header
`x-prof-seen`; `:9100` is a socat/`sleep 5` HTTP server.

Runtime creation (`out/b_inheritance-3.4.txt`, B2):

```
$ printf '@@1\nadd backend dynFast from be-http mode http guid be:dynFast; add server dynFast/fast 127.0.0.1:9000 check init-state up; enable health dynFast/fast; enable server dynFast/fast; publish backend dynFast\n' | socat …
New backend registered.
New server registered.
Backend published.
```

Verified with curl, not with command success:

| backend | how defined | status | `x-prof-seen` | elapsed |
|---|---|---|---|---|
| `fileFast` | file, `from be-http` | `HTTP/1.1 200 OK` | `yes` | 47 ms |
| `ctrlFast` | file, `from plain` | `HTTP/1.1 200 OK` | *(empty)* | 45 ms |
| **`dynFast`** | **runtime, `from be-http`** | **`HTTP/1.1 200 OK`** | **`yes`** | 47 ms |
| `fileSlow` | file, `from be-http` | `HTTP/1.1 504 Gateway Time-out` | – | **2048 ms** |
| `ctrlSlow` | file, `from plain` | `HTTP/1.1 200 OK` | – | **5047 ms** |
| **`dynSlow`** | **runtime, `from be-http`** | **`HTTP/1.1 504 Gateway Time-out`** | – | **2056 ms** |
| `fileFast` + `x-deny: 1` | file, `from be-http` | `HTTP/1.1 503 Service Unavailable`, body `denied-by-profile` | – | 42 ms |
| `ctrlFast` + `x-deny: 1` | file, `from plain` | `HTTP/1.1 200 OK` | – | 43 ms |
| **`dynFast` + `x-deny: 1`** | **runtime, `from be-http`** | **`HTTP/1.1 503 Service Unavailable`, body `denied-by-profile`** | – | 46 ms |

All three rule kinds are inherited, and the runtime-created backend behaves
identically to the file-defined one that uses the same `from`.

Two side findings:

* **`from` is mandatory** for `add backend` on 3.4:
  `add backend dynBare mode http` → `Usage: add backend <name> from <defproxy>.`
  A runtime backend therefore *always* inherits some defaults section.
* `publish backend` is the traffic gate, not a formality (`out/g_publish-3.4.txt`):
  created + healthy + unpublished → `curl` returns **503**; after `publish backend` →
  **200**; after `unpublish backend` → **503** again. This applies to *file-defined*
  backends too (`filebe`: 200 → unpublish → 503 → publish → 200), and `show stat`
  marks them: `pubX status=UP (UNPUB)`.

### The frontend fallback path does not exist

`http-request set-timeout server` is **rejected** in a frontend
(`out/b_inheritance-3.4.txt`, B5):

```
$ haproxy -dr -c -f /cfg/fe-timeout.cfg
[ALERT]    (232) : config : parsing [/cfg/fe-timeout.cfg:8] : error detected in frontend 'fe' while parsing 'http-request set-timeout' rule : 'fe' has no backend capability.
[ALERT]    (232) : config : Error(s) found in configuration file : /cfg/fe-timeout.cfg
rc=1
```

Putting it in a `defaults` section that a **frontend** uses passes `haproxy -c`
(`rc=0`) but is a **silent no-op** at runtime (B6): the request against the 5 s
upstream returned `code=200` after **5082 ms**, not 2 s. So there is no frontend-side
fallback — the rule has to live on the backend, which is exactly what inheritance
already gives.

### B — one-line answer

**Yes** — a backend created with `add backend dynX from be-http` inherits the named
defaults' `http-request` rules in full (verified by curl: `X-Prof` echoed back, 504 at
2.0 s from `set-timeout server 2s` versus 5.0 s without it, and the conditional
`denied-by-profile` 503), identically to a file-defined backend using the same `from`;
`from` is in fact mandatory, and the frontend fallback is not available at all
(`set-timeout server` is a parse error in a frontend, and a silent no-op when smuggled
in through a frontend's defaults section).

---

## C. `defaults haptic-implicit from haptic-base` as the last, never-referenced section

Four configs, `haproxy -dr -c` on 3.0.26 and 3.4.3 (`out/c_defaults_warning-3.{0,4}.txt`).

**c1 — the HAPTIC pattern** (`haptic-base` explicitly referenced; `haptic-implicit from
haptic-base` last and never referenced; proxies with and without `from` after it):

```
=== c1.cfg  ->  haproxy -dr -c ===
rc=0
--- warnings only:
(none)
```

Identical on 3.0 and 3.4. **c4** (same shape, no proxies without `from` at all) is also
`rc=0`, zero warnings.

**c2 — negative control**, last defaults is `haptic-base` itself, both referenced and
implicitly used:

```
[WARNING]  (39) : config : parsing [/cfg/c2.cfg:14] : defaults section 'haptic-base' (declared at /cfg/c2.cfg:4) is explicitly referenced by another proxy and implicitly used here. To avoid any ambiguity don't mix both usage. Add a last defaults section not explicitly used or always use explicit references.
Warnings were found.
rc=0
```

**c3 — negative control**, a `from`-less proxy placed *before* `haptic-implicit`: the
same warning, once per offending proxy (lines 13 and 15).

Byte-identical warning text on 3.0.26 and 3.4.3 — the check is live on both, so c1's
silence is a real pass, not a version that lacks the check.

### C — one-line answer

**Yes, warning-free on both 3.0 and 3.4** (`rc=0`, no output at all); the
"explicitly referenced … implicitly used" warning fires only in the negative controls
(last defaults referenced by `from`, or a `from`-less proxy placed before the trailing
section), with byte-identical wording on both versions — which is exactly what
HAProxy's own remedy text tells you to do ("Add a last defaults section not explicitly
used").

---

## D. `add server` keywords, balance algorithms, and time-to-traffic

### D.1 Self-documentation

`add server help` exists **from 3.2** (3.0/3.1 answer `Require 'backend/server'.`) and
prints the authoritative list — `out/d_server_keywords-3.4.txt`, D0. HAProxy prints one
keyword per line; reflowed here, keeping its three groups:

```
List of keywords supported for dynamic server:
allow-0rtt alpn ca-file check-alpn check-sni check-ssl ciphers ciphersuites client-sigalgs
crl-file curves crt force-sslv3 force-tlsv10 force-tlsv11 force-tlsv12 force-tlsv13 ktls
no-check-sni-auto no-renegotiate no-sni-auto no-ssl-reuse no-sslv3 no-tlsv10 no-tlsv11
no-tlsv12 no-tlsv13 no-tls-tickets npn renegotiate send-proxy-v2-ssl send-proxy-v2-ssl-cn
sigalgs sni ssl ssl-min-ver ssl-max-ver tls-tickets verify verifyhost
backup cookie disabled error-limit guid ws hash-key id idle-ping init-state maxconn maxqueue
minconn no-backup no-send-proxy no-send-proxy-v2 no-tfo observe on-error on-marked-down
on-marked-up pool-conn-name pool-low-conn pool-max-conn pool-purge-delay proto
proxy-v2-options send-proxy send-proxy-v2 set-proxy-v2-tlv-fmt shard slowstart source
strict-maxconn tfo track usesrc weight
healthcheck addr agent-addr agent-check agent-inter agent-port agent-send check
check-pool-conn-name check-proto check-reuse-pool check-send-proxy check-via-socks4
rise fall inter fastinter downinter port
```

Rejection strings: `add server` → `Require 'backend/server'.`; `add server kw/h1` →
`'server' expects <name> and <addr>[:<port>] as arguments.`; bad keyword →
`unknown keyword 'no-such-keyword-here'.`

### D.2 The requested keyword list, tested one `add server` per keyword

`out/D-MATRIX.txt` (generated by `summarize-d.sh` from the five per-version logs):

```
KEYWORD          3.0      3.1      3.2      3.3      3.4
check            ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
inter            ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
fastinter        ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
downinter        ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
rise             ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
fall             ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
port             ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
addr             ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
weight           ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
maxconn          ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
maxqueue         ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
minconn          ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
backup           ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
cookie           ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
guid             ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
ssl              ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
sni              ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
verify           ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
verifyhost       ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED   (*)
ca-file          ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED   (*)
crt              ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED   (*)
crl-file         ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED   (*)
alpn             ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
ciphers          ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
ciphersuites     ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
ssl-min-ver      ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
ssl-max-ver      ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
proto            ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
send-proxy       ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
send-proxy-v2    ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
slowstart        ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
agent-check      ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
agent-port       ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
agent-inter      ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
agent-send       ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
on-marked-down   ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
observe          ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
init-state       UNKNOWN  ACCEPTED ACCEPTED ACCEPTED ACCEPTED
disabled         ACCEPTED ACCEPTED ACCEPTED ACCEPTED ACCEPTED
enabled          OTHER    OTHER    OTHER    OTHER    OTHER
no-check         OTHER    OTHER    OTHER    OTHER    OTHER
```

Verbatim non-acceptances, identical on every version except `init-state`:

```
init-state       unknown keyword 'init-state'.                          (3.0 only)
enabled          'enabled' option is not accepted for dynamic server
no-check         'no-check' option is not accepted for dynamic server
```

**(\*) The SSL-file keywords only work for files the running config already loaded.**
With a CA/cert/CRL that no proxy in the config references
(`out/d_server_keywords-3.4.txt`, D1c — same on all five versions):

```
  new_cafile       -> 'ca-file' : unable to load /cfg/tls/ca2.crt.
  new_crt          -> unable to stat SSL certificate '/cfg/tls/client2.pem'.
  new_crl          -> 'ca-file' : unable to load /cfg/tls/ca2.crt.
```

With the identical files preloaded by a `backend sslpre` in the config file (D1b):

```
  pre_cafile       -> New server registered.
  pre_crt          -> New server registered.
  pre_crl          -> New server registered.
  pre_verifyhost   -> New server registered.
```

So `ca-file` / `crt` / `crl-file` / `verifyhost` on a dynamic server are usable only
against material already in HAProxy's store (config-loaded, or pushed with
`add ssl ca-file` / `set ssl cert`) — a runtime `add server` cannot pull a new file off
disk.

### D.3 Balance algorithms

`out/D-MATRIX.txt`, D2. Same verdicts on all five versions; the message text changed in
3.4.

| `balance` | `add server` |
|---|---|
| `roundrobin` | `New server registered.` |
| `leastconn` | `New server registered.` |
| `first` | `New server registered.` |
| `random` | `New server registered.` |
| `static-rr` | **rejected** |
| `source` (map-based, default) | **rejected** |
| `source` + `hash-type consistent` | `New server registered.` |
| `uri` (map-based, default) | **rejected** |
| `uri` + `hash-type consistent` | `New server registered.` |
| `hdr(host)` (map-based, default) | **rejected** |
| `hdr(host)` + `hash-type consistent` | `New server registered.` |

Rejection text:

```
3.0 – 3.3 : Backend must use a dynamic load balancing to support dynamic servers.
3.4       : Backend does not support dynamic servers : backend 'bal_static_rr' uses a non dynamic load balancing method.
```

So `balance first` **does** accept `add server`; the blocker is map-based hashing, and
`hash-type consistent` lifts it for every hash algorithm.

### D.4 Time-to-traffic without `init-state` (3.0), measured inside the container

`out/d2_initstate_timing-3.0.txt`. Baseline curl round trip against an already-up
backend: `200 in 0.000498s / 0.000264s / 0.000296s`. `op_state` 0 = DOWN, 2 = UP;
`admin_state` 1 = MAINT, 0 = ready.

```
--- p1: check inter 300ms rise 1, NO init-state, then enable health + enable server
state right after add:    op_state=0 admin_state=1 check_status=1 check_health=0
state right after enable: op_state=2 admin_state=0 check_status=1 check_health=1   (cli calls took 4ms)
FIRST 200 at t+22ms after the enable pair started

--- p2: same + 'set server health up' right after the enable pair
FIRST 200 at t+16ms

--- p3: 'set server health up' ONLY (never 'enable server')
state after: op_state=0 admin_state=1 check_status=1 check_health=1
never served 200 (last code 503) after 1985ms

--- p4: check inter 5s rise 1 (slow checks)
state right after enable: op_state=2 admin_state=0 check_health=1
FIRST 200 at t+12ms

--- p5: check inter 5s rise 2
state right after enable: op_state=2 admin_state=0 check_health=2
FIRST 200 at t+12ms

--- p7: no 'check' at all, enable health + enable server
FIRST 200 at t+13ms

--- p8: init-state up
add: unknown keyword 'init-state'.

--- p9: added and nothing else
op_state=0 admin_state=1 — never served 200 after 1950ms
```

The 3.4 run (`out/d2_initstate_timing-3.4.txt`) matches line for line, and p8
(`init-state up`) there gives `state right after add: op_state=0 admin_state=1
check_status=1 check_health=2` → `FIRST 200 at t+14ms`.

So: `enable health` + `enable server` moves the server straight to `op_state=2` — the
`rise` counter is set to its threshold immediately, and **no health check is waited
for** (p4/p5 with `inter 5s` behave the same as `inter 300ms`). The only latency is the
CLI round trip (~4–7 ms) plus one request (~12–22 ms end to end).
`set server … health up` is redundant, and on its own (without `enable server`) it does
nothing, because the server stays in MAINT.

### D — one-line answer

`add server` accepts every requested keyword on 3.0–3.4 except `init-state`
(`unknown keyword 'init-state'.` on 3.0 only; 3.1+ fine), `enabled` and `no-check`
(`'<kw>' option is not accepted for dynamic server` on all five), with `ca-file`/`crt`/
`crl-file`/`verifyhost` further limited to material the config already loaded; `balance
first`, `leastconn`, `random` and `roundrobin` accept `add server` while map-based
`static-rr`/`source`/`uri`/`hdr` reject it (`Backend does not support dynamic servers :
… uses a non dynamic load balancing method.` on 3.4, `Backend must use a dynamic load
balancing to support dynamic servers.` on 3.0–3.3) unless `hash-type consistent` is
set; and on 3.0 a server added without `init-state` takes traffic **immediately** after
`enable health` + `enable server` (op_state flips to UP with no health check awaited —
first 200 at t+22 ms, even with `check inter 5s`), so `set server … health up` adds
nothing and, without `enable server`, does nothing at all.

---

## E. Deferred deletes with an idle keep-alive client

Config: `http-reuse safe`, default `pool-purge-delay` (confirmed as **5000 ms** in the
`show servers conn` dump), one `add server`-created server per case, a client that
issues one request and then holds the keep-alive connection open, all framing through
the master socket. `out/e4_deferred_delete_final-3.4.txt` /
`out/e4_deferred_delete_final-3.0.txt`. Timestamps are relative to the first teardown
command.

### 3.4 (framing `@@1`, write side held open — the only way `wait` really waits)

```
E-a  idle keep-alive client, 'disable server' ONLY
     conns before:          … purge_delay 5000 … idle_cur=1
     conns after teardown:  … purge_delay 5000 … idle_cur=0
     wait 2s srv-removable  ->  t+7ms   [Done.]
     del server             ->  [Server deleted.]

E-b  idle keep-alive client, 'disable server' + 'shutdown sessions server'
     wait 2s srv-removable  ->  t+7ms   [Done.]
     del server             ->  [Server deleted.]

E-c  idle keep-alive client, NO teardown (control)
     wait 2s srv-removable  ->  t+5ms   [Failed. Only servers in maintenance mode can be deleted.]
     del server             ->  [Only servers in maintenance mode can be deleted.]

E-i  no client at all, only a pooled idle SERVER connection, 'disable server' ONLY
     wait 2s srv-removable  ->  t+6ms   [Done.]
```

`disable server` alone is enough: the `show servers conn` line shows the idle pool
being emptied by it (`idle_cur` 1 → 0, `unsafe_nb` 1 → 0) — the 5 s `pool-purge-delay`
is **not** waited out.

The case where `shutdown sessions server` actually matters is a request still **in
flight** (upstream sleeping 5 s):

```
E-d  in flight, 'disable server' ONLY,      wait 10s  ->  t+4640ms  [Done.]
E-e  in flight, 'disable server' ONLY,      wait 2s   ->  t+2009ms  [Wait delay expired. Server still has connections attached to it, cannot remove it.]
E-f  in flight, 'disable' + 'shutdown sessions', wait 2s -> t+7ms   [Done.]
```

and the framing decides whether the wait happens at all:

```
E-g  in flight, disable only, '@@1' with the write side CLOSED
     wait 10s  ->  t+7ms  [Interrupted. Server still has connections attached to it, cannot remove it.]
     del server -> [Server still has connections attached to it, cannot remove it.]
E-h  in flight, disable only, '@1' one-shot
     wait 10s  ->  t+7ms  [Interrupted. …]     del server -> fails
E-j  in flight, disable only, '@1' with the write side HELD OPEN
     wait 10s  ->  t+7ms  [Interrupted. …]     del server -> (succeeded only because 12s of hold had elapsed)
```

### 3.0 (no `@@1`, so only `@1` one-shot is available)

```
E-k  idle keep-alive client, 'disable server' ONLY, '@1 wait 2s srv-removable'
     conns before:         … 5000 0 1 1 1 0 -1 1 … (idle conn present)
     conns after teardown: … 5000 0 1 1 0 0 -1 0 … (pool emptied by 'disable server')
     wait 2s srv-removable -> t+8ms  [Done.]
     del server            -> [Server deleted.]

E-l  same + 'shutdown sessions server'
     wait 2s srv-removable -> t+10ms [Done.]
     del server            -> [Server deleted.]

E-h  in flight, disable only
     wait 10s srv-removable -> t+8ms [Interrupted.]      (no reason string on 3.0)
     del server             -> [Server still has connections attached to it, cannot remove it.]
```

3.4 with the same `@1` framing gives the identical `Done.` at t+7 ms for E-k/E-l.

### E — one-line answer

**Yes to both, and there is no measurable difference**: with a client holding an idle
keep-alive connection under `http-reuse safe` and the default 5 s `pool-purge-delay`,
`wait 2s srv-removable` returns `Done.` in **7–10 ms** after `disable server` alone (on
3.0 and 3.4), because `disable server` itself empties the idle pool (`idle_cur` 1 → 0)
— `shutdown sessions server` changes nothing here (also `Done.` in 7–10 ms) and only
matters for a request still **in flight**, where `disable` alone makes `wait 10s` block
until the stream ends (`Done.` at t+4640 ms) or expire (`Wait delay expired. Server
still has connections attached to it, cannot remove it.` at t+2009 ms for a 2 s budget)
while `disable` + `shutdown sessions` returns `Done.` at t+7 ms — with the crucial
caveat that the wait only happens under `@@1` with the connection's write side held
open; under `@1` (the only option on 3.0/3.1) it returns `Interrupted. …` in ~7 ms and
`del server` then fails.
