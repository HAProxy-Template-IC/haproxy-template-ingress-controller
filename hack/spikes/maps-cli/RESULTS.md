# HAProxy runtime spike: maps, CLI limits, certificates

Scope: runtime map manipulation (A), CLI line/payload limits (B), and runtime
certificate management (C), on **3.0.26** and **3.4.3**
(`haproxytech/haproxy-debian:3.0` / `:3.4`).

Every container is started exactly like the shipped pod:

```
haproxy -W -db -S /etc/haproxy/run/master.sock,level,admin -f /etc/haproxy/haproxy.cfg
```

and driven through the **master** socket. A worker `stats socket` is also
configured so master-relay effects can be separated from worker behaviour.

## How to reproduce

```
bash gen_certs.sh                  # certificate fixtures for section C
bash test_a.sh     3.4             # A: values, duplicates, ordering, versions, set map
bash test_b.sh     3.4             # B: coarse line/payload/multi-command limits
bash test_line.sh  3.4             # B1/B5: exact command-line cut-offs
bash test_b3.sh    3.4             # B2/B3: payload sweep by entries landed
bash test_b5.sh    3.0 [bufsize]   # B2: payload cap and its tune.bufsize linkage
bash test_chunk.sh 3.0 600 3000    # B2: chunked replace into one generation
bash test_c.sh     3.4             # C: certificates, crt-lists, ca-files
bash probe_master.sh 3.0           # which master-CLI form carries a payload
bash diffa.sh ; bash diffc.sh      # normalised 3.4-vs-3.0 diffs of A and C
```

Each script takes the image tag as `$1` and writes to stdout; `out/` holds the
captured transcripts every number below cites. `lib.sh` is the shared harness
(container lifecycle, master/worker sockets, payload helpers); `hapcli.py` is a
**full-duplex** CLI client. `test_a4.sh` is `test_a.sh` against a second fixture
directory so 3.0 and 3.4 can run concurrently.

> **Methodology warning, learned the hard way.** A half-duplex client (send
> everything, then read) deadlocks against HAProxy's reply on large payloads and
> looks exactly like HAProxy truncating the payload — an early run "measured" a
> 3000-entry payload landing 1238 entries. It was the client. Likewise, *absence
> of an error string is not proof of acceptance*: HAProxy can close the
> connection before the client reads the error, so a size bisect keyed on the
> string "too big" reports limits that are 4-16x too high. **Only `entry_cnt`
> from `show map` is trustworthy.** Every number below is entry-count-based.

---

## Cross-cutting: how to reach a worker payload command from the master

| form | 3.0 | 3.4 |
|---|---|---|
| `@1 <cmd> <<` + body | works | works |
| `@1` alone on a line (session switch), then `<cmd> <<` + body | works | works |
| `@@1` session, then `<cmd> <<` + body | **fails** — `Can't find the target PID matching the prefix '@@1'` | works |
| worker `stats socket` directly | works | works |

**`@@1` does not exist on 3.0.** The portable form is `@1 <cmd> <<`, or a bare
`@1` line to switch the session.

**`@1 cmd1; cmd2; cmd3` relays only `cmd1` to the worker** — `cmd2..N` execute on
the *master*, which reports `Unknown command: 'set'`. Evidence (`out/B-3.4.log`
B4): 2 commands → 1 error line, 10 commands → 9 error lines, 100 → 99. To send
several commands to a worker on one line, switch the session first (`@1` alone,
or `@@1` on 3.1+).

---

## A. Runtime map values and ordering

Fixture: one frontend with `map_str`, `map_reg`, `map_beg`, `map_sub`, `map_dom`
lookups whose results are echoed into the response body and an `x-spec` header,
so every value is verified twice — through `show map` *and* through a real
request.

Both versions were run through the identical script; `diffa.sh` diffs the two
transcripts with pointers and timings normalised. **A1, A2, A3, A5 and A7 are
byte-identical between 3.0.26 and 3.4.3.** The only behavioural divergences are
**A4** (`del map` on duplicate keys), **A6b** (`clear map @<ver>`) and the
payload size limit that A6e runs into — each called out below.

## A1-A3. Special characters: line form vs payload form

**One-line answer: the line form mangles values (space, `;`, TAB truncate; `\`
is an escape that gets eaten); the payload form takes the rest of the line
verbatim and preserves every character tested. Use the payload form, always.**

`add map <path> <key> <value>` (line form), read back with `show map | cat -A`
and by request (`out/A-3.4.log`, A1/A3):

| value sent | stored | via request |
|---|---|---|
| `a b` | `a` | `[a]` |
| `a;b` | `a` (+ `Unknown command: 'b'`) | `[a]` |
| `a%b` | `a%b` | `[a%b]` |
| `a+b` | `a+b` | `[a+b]` |
| `a\|b` | `a\|b` | `[a\|b]` |
| `a"b` | `a"b` | `[a"b]` |
| `a\b` | `ab` | `[ab]` |
| `a\\b` | `a\b` | `[a\b]` |
| `a#b` | `a#b` | `[a#b]` |
| `a<TAB>b` | `a` | `[a]` |
| `a\ b` | `a b` | `[a b]` |
| `"a b"` | `"a` | `["a]` |

`add map <path> <<` + `<key> <value>` (payload form) — **every one of the same
values round-trips byte-exact**, including embedded spaces, `;`, `%`, `+`, `|`,
`"`, `\`, `#`, TAB and *trailing* spaces (`/P_trailsp` → "trailing␣␣␣" (three trailing spaces)). There is
no escape processing at all: `a\ b` stays `a\ b`, `a\b` stays `a\b`.

- **Does `;` terminate the command in the line form? Yes.** `add map <p> /L_semicmd VAL; show info`
  stores `VAL` and then executes `show info` as a second command. A value carrying
  a `;` is therefore a **command-injection vector** in the line form.
- **Does the payload form keep embedded spaces? Yes**, and it does not split on
  `;`: `/P_semicmd VAL; add map <p> /P_injected INJ` stores the *whole* string
  `VAL; add map /etc/haproxy/maps/spec.map /P_injected INJ` as the value, and
  `/P_injected` is `found=no`.
- The **key** is always the first whitespace-delimited token, in both forms.
  A key containing a space is impossible: payload line `a b VALUE` yields
  `key="a", value="b VALUE"`.
- One payload can carry many `key value` lines; newline is the record separator.

## A4. `del map` with duplicate keys

**One-line answer: THIS DIFFERS BY VERSION — 3.0 deletes ALL entries with the key
in one call, 3.4 deletes exactly ONE per call — so a controller must not assume
either; `del map <path> #<id>` is the unambiguous form and the `#` is mandatory.**

`str.map` ships `/dup dupA` and `/dup dupB`; a third (`dupC`) was added at
runtime, then `del map <path> /dup` was issued three times.

**3.4** (`out/A-3.4.log`, A4) — one entry per call, oldest first:

```
before:  /s1 v1 | /dup dupA | /dup dupB | /z zfile | /dup dupC
del #1   ->  /s1 v1 | /dup dupB | /z zfile | /dup dupC     (lookup: dupB)
del #2   ->  /s1 v1 | /z zfile | /dup dupC
del #3   ->  /s1 v1 | /z zfile                             (lookup: NOSTR)
```

**3.0** (`out/A-3.0.log`, A4) — all three gone in one call:

```
before:  /s1 v1 | /dup dupA | /dup dupB | /z zfile | /dup dupC
del #1   ->  /s1 v1 | /z zfile                             (lookup: NOSTR)
del #2   ->  Key not found.
del #3   ->  Key not found.
```

Lookups always return the **first inserted** duplicate on both versions. By id
(both versions): `del map <path> #0x7f…` → deleted exactly that entry; the same
id **without** `#` → `Key not found.`

## A5. Ordering: file order vs runtime-append order

**One-line answer: append order is observable and usually LOSES — `map_reg`,
`map_sub` and `map_dom` are first-match-in-list-order, so a more specific pattern
added at runtime never wins; only `map_beg` (longest-prefix tree) lets a runtime
append take effect.**

`get map` reports the index type, which explains all of it
(`out/A-3.4.log`, A5):

| match | index | rule | runtime append of a more specific pattern |
|---|---|---|---|
| `map_str` | `idx=tree` | duplicates allowed; **first inserted wins** | loses |
| `map_beg` | `idx=tree` | **longest prefix wins**, ties → first inserted | **wins** |
| `map_reg` | `idx=list` | **first match in list order** | loses |
| `map_sub` | `idx=list` | **first match in list order** | loses |
| `map_dom` | `idx=list` | **first match in list order** | loses |

Evidence:

- `map_reg` file order `^/a/b.* B`, `^/a.* A`. `GET /a/b/c` → `B`. Adding
  `^/a/b/c.* C` at runtime (appended last) leaves `GET /a/b/c` → **`B`**.
  A newly appended regex is only reachable when nothing earlier matches
  (`^/zz.* Z` → `GET /zz1` → `Z`).
- `map_beg` file order `/p/q BQ`, `/p P`. `GET /p/q/r` → `BQ`. Adding `/p/q/r RUNTIME`
  → `GET /p/q/r` → **`RUNTIME`** and `GET /p/q/z` → `BQ` (longest prefix wins,
  order-independent). Adding a *duplicate* `/p SHORTRUNTIME` does **not** displace
  the file's `/p`: `GET /pZ` → `P`.
- `map_sub` file order `foobar FOOBAR`, `foo FOO`. `GET /xfoobarx` → `FOOBAR`;
  after adding `oba RUNTIMESUB`, still `FOOBAR` (but `GET /xobax` → `RUNTIMESUB`).
- `map_dom` file order `sub.example.com SUB`, `example.com EX`. Adding the more
  specific `a.sub.example.com RUNTIMEDOM` last: `Host: a.sub.example.com` → **`SUB`**,
  unchanged.
- `map_str` duplicates: `add /ord first` then `add /ord second` → `GET /ord` → `first`.

**Consequence for a controller:** you cannot patch a list-indexed map
(`reg`/`sub`/`dom`) incrementally and expect precedence to come out right. Order
is only controllable by rewriting the whole map — which is exactly what the
`prepare`/`commit` generation switch does.

## A6. `prepare` / `add @ver` / `commit` / `clear`

**One-line answer: the generation switch is genuinely atomic — 4508 requests
across the switch saw only the old value then the new one, never a miss — and a
3000-entry replace costs ~50 ms end to end.**

Command outputs (`out/A-3.4.log`, A6):

```
prepare map <path>        -> "New version created: 1"
add map @1 <path> <<      -> silent success; entries go to the PENDING generation
show map <path>           -> still the LIVE generation
show map @1 <path>        -> the PENDING generation
show map                  -> "... curr_ver=0 next_ver=1 entry_cnt=4"   (live+pending)
commit map @1 <path>      -> silent success
show map                  -> "... curr_ver=1 next_ver=1 entry_cnt=3"
```

- Lookups during the uncommitted window return the **old** value (`/v` → `OLD`).
- After commit, `/v` → `NEW`, `/v2` → `NEW2`.
- `commit map @<already-committed>` and `commit map @9999` → `Version number out of range.`
- `add map @9999 <path>` → `Version number in the future, please use 'prepare map' before.`

**Atomicity (A6e):** a tight `curl` loop ran for 20 s across
`prepare` + 3000-entry payload `add` + `commit`.
Result on 3.4: **4508 samples, 0 empty/failed**, exactly two distinct values and
exactly one transition:

```
   4050 [NEWGEN]
    458 [OLD]
transitions: [OLD] -> [NEWGEN]
```

No half state, no fallback to the map's default value. 3.0 shows the same clean
single transition (`out/A-3.0.log`; there the payload was over 3.0's smaller
limit so the new generation was empty, but the *switch* was still atomic:
1797 samples, 0 failures, `[OLD]` → `[NOVER]` with no intermediate state).

**Cost of a 3000-entry replace (3.4, `out/A-3.4.log` A6e/A6f):**

| step | time |
|---|---|
| `prepare map` | 0.0177 s |
| `add map @v <path> <<` (3000 entries, 42775 B, one payload) | 0.0166 s |
| `commit map @v` | 0.0165 s |
| **total** | **0.0508 s** |
| non-atomic `clear map` + `add map <<` for comparison | 0.0313 s |

All 3000 entries land on 3.4 (`entry_cnt=3000`; `/v`, `/k1500`, `/k2998` all
resolve). **On 3.0 the same single 42 775-byte payload is rejected** — see B2 —
so the portable form is several chunks against one prepared version
(3000 entries in 5 chunks: 0.21 s on 3.0, 0.18 s on 3.4; still atomic).

**`clear map @<ver> <path>` behaves differently on 3.0 and 3.4 — do not rely on
it.** Sequence: `prepare` → `add @2` (two entries) → `clear map @2` → `commit @2`.

| | `show map @2` after clear | live map after committing @2 |
|---|---|---|
| **3.0** | empty — the pending generation *was* cleared | **empty**, `/v` → `NOVER`, `entry_cnt=0` |
| **3.4** | still lists `/v ABANDONED`, `/zz ABANDONED2` | **the abandoned entries are installed**, `/v` → `ABANDONED` |

On 3.4 `clear map @<ver>` is effectively a no-op against the pending generation
(`out/A-3.4.log` A6b vs `out/A-3.0.log` A6b). There is no `abort map` command; to
discard a prepared generation, just never commit it — the next `prepare` creates
a fresh version number and `commit` of the stale one returns
`Version number out of range.`

**`clear map <path>` (no version) wipes the LIVE map immediately** and
non-atomically — lookups fall through to the map's default value
(`/v` → `NOVER`). Never use it as the first half of a replace on a live map.

**Oversize note:** a 30 000-entry / 487 773-byte versioned payload lands
**zero** entries under default limits — see section B.

## A7. `set map`

**One-line answer: `set map` changes the value in place, keeping the entry's
reference id and its position — but it has no payload form, so its value is
subject to the same line-form mangling, and on a duplicate key it changes ALL
matching entries.**

Evidence (`out/A-3.4.log`, A7):

- `set map <path> /b BBB-CHANGED`: `show map` shows the same id `0x7f…95a60` in
  the same row → position and id preserved.
- `set map <path> #<id> <value>` works; the `#` is mandatory.
- Missing key → `entry not found.`
- `set map <path> /a x y;z%w` → stores `x`, then tries to run `z%w` as a command.
- **There is no payload form**: `set map <path> <<` → `'set map' expects three
  parameters: map identifier, key and value.` To set a value containing spaces
  you must `del` + `add` via a payload, or replace the generation.
- Duplicate key `/d` with values `D1` and `D2`: `set map <path> /d DSET` changed
  **both** rows to `DSET`.

---

## B. CLI limits

## B1. `tune.bufsize` and the maximum command line

**One-line answer: `tune.bufsize` is 16384 on both 3.0 and 3.4; a single command
line must fit in it — ~16 KiB on the worker socket, ~1 KiB less through the
master relay, and the master's over-limit error is a misleading help dump instead
of the real message.**

`haproxy -vv` on both images:

```
Default settings :
  bufsize = 16384, maxrewrite = 1024, maxpollevents = 200
```

Exact cut-offs, sweeping the total line length in 10-byte steps and checking
whether the entry landed (`out/LINE-3.4.log`, `out/LINE-3.0.log`) — **identical
on 3.0 and 3.4**:

| path | largest line accepted | first rejected | equals |
|---|---|---|---|
| master relay `@1 <cmd>` | **15 360 B** | 15 370 B | `tune.bufsize - maxrewrite` = 16384 - 1024 |
| worker `stats socket` | **16 380 B** | 16 384 B (3.0) / 16 400 B (3.4) | `tune.bufsize` - 4 |

Coarser confirmation with `add map <path> <key> <VALUE>` of N bytes
(`out/B-3.4.log`, B1/B1b): 15 000 → OK on both; 16 000 → rejected via master,
accepted (16 000 bytes read back intact) on the worker socket.

- Worker socket over-limit error (3.4): `The command line is too big for the buffer size. Please change 'tune.bufsize' in the configuration to use a bigger buffer size.`
- Same on 3.0, worded slightly differently: `The command is too big for the buffer size. Please change tune.bufsize …`
- **Master relay over-limit "error" is `The following commands are valid at this level:` + the full help dump.** A controller parsing responses will not recognise this as a size failure — it looks like an unknown command, not an overflow.

## B2. `tune.cli.max-payload-size` — and the big 3.0/3.4 divergence

**One-line answer: 3.4 caps payloads at `tune.cli.max-payload-size`, default
131 072 B (~128 KiB); 3.0 has no such setting and caps payloads at
`tune.bufsize` — 16 KiB by default, 8x smaller — and in both cases an oversize
payload lands ZERO entries, never a partial apply.**

Measured cap, bisecting on entries actually landed (`out/B5-*.log`):

| version | `tune.bufsize` | largest accepted payload | smallest rejected | cap tracks |
|---|---|---|---|---|
| **3.0.26** | 16384 (default) | 778 lines = **16 338 B** | 779 lines = 16 359 B | `tune.bufsize` |
| **3.0.26** | 65536 | 3118 lines = **65 478 B** | 3119 lines = 65 499 B | `tune.bufsize` |
| **3.4.3** | 16384 (default) | 5868 lines = **123 228 B** | 5869 lines = 123 249 B | `tune.cli.max-payload-size` |
| **3.4.3** | 65536 | 6241 lines = **131 061 B** | 6242 lines = 131 082 B | `tune.cli.max-payload-size` = 2^17 |

So on **3.0 the payload ceiling IS `tune.bufsize`** (16 338 B ≈ 16384 minus the
command line; raising bufsize to 64 KiB moves it to 65 478 B, one-for-one).
On **3.4 the ceiling is the separate `tune.cli.max-payload-size`, default
131 072 B**; with the default 16 KiB bufsize the practical cutoff lands a little
lower (123 228 B) because of buffer-chunk accounting.

Coarse sweep confirming the same numbers across all three transports — master
`@1`, master `@@1` session, worker socket (`out/B3-3.4.log`, `out/B3-3.0.log`):

| payload | 3.4 landed | 3.0 landed |
|---|---|---|
| 84 000 B (4000 lines) | 4000 | **0** |
| 210 000 B and above | 0 | 0 |

- Over-limit response (3.4): `The payload is too big. Please change 'tune.cli.max-payload-size' in the configuration to use a bigger payload.`
- Over-limit response (3.0): `The command is too big for the buffer size. Please change tune.bufsize in the configuration to use a bigger command.`
- **Nothing is applied** — `entry_cnt` stays 0. All-or-nothing, no silent truncation.
- The response string is **not reliable**: on some sizes HAProxy closes before the
  client reads it and the reply is empty (or, via `@@1`, `Can't connect to the
  target CLI!`). Gate on state, not on the message.
- The CLI recovers fine; a fresh connection right after works
  (`is the CLI session still usable afterwards? -> 1`).
- `tune.cli.max-payload-size 2097152` on 3.4 raises it: a 1 140 000-byte /
  60 000-line payload then lands **all 60 000** entries (`out/B-3.4.log`, B2c).
- **On 3.0 the keyword does not exist**: `unknown keyword 'tune.cli.max-payload-size' in 'global' section; did you mean 'tune.vars.proc-max-size' maybe ?` and HAProxy refuses to start. The only lever on 3.0 is `tune.bufsize`, which also changes every HTTP buffer.

**Consequence:** the portable chunk budget is **≤ 15 KiB per payload** (safe under
3.0's 16 338 B) unless the chart raises `tune.bufsize`. This is exactly what bit
the A6e run on 3.0: a single 42 775-byte payload for 3000 entries was rejected
and the committed generation was empty (`entry_cnt=0`, `/v` → `NOVER`).

### The chunked-replace pattern is safe (verified)

`prepare` once → **many** `add map @<ver> <path> <<` payloads → `commit` once
(`out/CHUNK-3.0.log`, `out/CHUNK-3.4.log`): 3000 entries in 5 chunks of 600 lines
(12 600 B each), under a tight `curl` loop.

| | 3.0.26 | 3.4.3 |
|---|---|---|
| entries after commit | 3001 / 3001 | 3001 / 3001 |
| push (5 chunks) | 0.188 s | 0.158 s |
| commit | 0.022 s | 0.024 s |
| **total** | **0.210 s** | **0.182 s** |
| lookups during the push | old value throughout | old value throughout |
| loop samples / failures | 3599 / **0** | 3599 / **0** |
| transitions observed | `[OLD]` → `[CHUNKGEN]` only | same |

**The generation switch stays atomic across multiple `add` calls**, and lookups
see the old generation for the whole push window. This is the pattern to use.

## B3. A payload command must be last on its line

**One-line answer: yes — and the failure mode differs by path, so never put
anything after `<<`.**

`out/B3-3.4.log` / `out/B3-3.0.log` (B3z) and `out/B-3.4.log` (B3):

| sent | 3.4 | 3.0 |
|---|---|---|
| master `@1 add map <p> <<; show version` + body | rest of the line is swallowed **into the payload**: a junk entry (empty key, value `show version`) alongside `/x3 X3` — **2 entries** | same — **2 entries** |
| master `@1 show version; add map <p> <<` + body | only `show version` reaches the worker; `add map` runs on the **master** → `Unknown command: 'add'` — **0 entries** | same — **0 entries** |
| session (`@1` alone) `show version; add map <p> <<` + body | works — **1 entry**. A payload command may be *preceded* by other `;` commands in session mode | same — **1 entry** |
| session (`@1` alone) `add map <p> <<; show version` + body | junk entry again — **2 entries** | same — **2 entries** |
| worker socket `add map <p> <<; show version` + body | **rejected**: `'add map' expects three parameters … or one parameter and a payload`; **0 entries**; the body lines are then executed as CLI commands | **accepted as a payload** — junk entry + `/x7 X7`, **2 entries** |

Every path either loses data or invents a junk entry. **Nothing may follow `<<`
on the line.**

**Custom terminator (`<<END`) does not skip blank lines.** A blank line inside
the body is parsed as a record and produces a garbage entry
(`out/B-3.4.log`, B3c). Strip blank lines before sending.

## B4. Multi-command lines

**One-line answer: bounded by `tune.bufsize` — about 500 `set server` commands
(~15.8 KB) fit on one line; 1000 (~31.6 KB) is rejected — and through the `@1`
prefix only the first command reaches the worker at all.**

Worker socket, 3.4 (`out/B-3.4.log`, B4b):

| commands | line bytes | result |
|---|---|---|
| 500 | 15 782 | OK |
| 1000 | 31 566 | `The command line is too big for the buffer size…` |
| 2000 / 4000 | 63 134 / 126 270 | rejected |

There is no per-command count limit — the bound is purely the line length, and
that bound is identical on 3.0 (16 380 B on the worker socket, 15 360 B via the
relay; `out/LINE-3.0.log`).

Through `@1 cmd1; cmd2; …` (B4): every command after the first runs on the
**master** — `cmds=2` → 1 error, `cmds=10` → 9 errors, `cmds=100` → 99 errors.
Confirmed on 3.0 too (`out/B3-3.0.log`: `@1 show version; add map …` →
`Unknown command: 'add'` from the master's command set).

## B5. Does the master relay change the limits?

**One-line answer: yes for command lines (~1 KiB less headroom and a misleading
error), no for payload size, and the relay's multi-command and session syntax
differ between 3.0 and 3.4.**

- Command line: the relay costs exactly `maxrewrite` (1024 B) of headroom —
  **15 360 B via `@1` vs 16 380 B on the worker socket**, on both versions.
- Payload size: identical across master `@1`, master `@@1` and the worker socket
  (B2z sweep above).
- Payload delivery: `@1 <cmd> <<` works everywhere; `@@1` is 3.1+ only.
- `;`-chaining: `@1 <cmd1>; <cmd2>` relays only `cmd1`.

---

## C. Certificates

Fixture: `bind :8443 ssl crt /etc/haproxy/ssl/a.pem crt-list /etc/haproxy/ssl/list.txt`
and `bind :8444 ssl crt …/a.pem ca-file …/ca.crt verify optional`. Leaf certs
carry an `OU=gen1` / `OU=gen2` tag so a rotation is visible in
`openssl s_client`. **3.0 and 3.4 behave identically** apart from cosmetic
differences in `show ssl cert` output — see `diffc.sh` and the diff below.

## C6. How the runtime identifies a certificate

**One-line answer: by the exact path string as written in the config — `show ssl
cert` lists those strings, and any other spelling of the same file is rejected.**

```
# filename
/etc/haproxy/ssl/a.pem
/etc/haproxy/ssl/b.pem
```

`show ssl cert /etc/haproxy/ssl/a.pem` (3.4):

```
Filename: /etc/haproxy/ssl/a.pem
Crt filename: /etc/haproxy/ssl/a.pem
Key filename: /etc/haproxy/ssl/a.pem
OCSP filename: /etc/haproxy/ssl/a.pem.ocsp
Option: ocsp-update off
Option: jwt off
Status: Used
Serial: 26042F369CDFD8E35B5BC1AAF229E8E452814E33
notBefore: Aug 17 17:52:27 2026 GMT
notAfter: Aug 14 17:52:27 2036 GMT
Subject Alternative Name: DNS:a.test
Algorithm: RSA2048
SHA1 FingerPrint: CDAAECF40DB2AF942554F53AEA4BD70C8DECA154
Subject: /CN=a.test/OU=gen1
```

On 3.0 the same command omits `Crt filename` / `Key filename` / `OCSP filename` /
`Option:` lines and adds `Issuer:` and `OCSP Response Key:`.

Name matching is a **literal string compare**, not a path resolution:

| asked for | answer |
|---|---|
| `show ssl cert a.pem` | `Can't display the certificate: Not found or the certificate is a bundle!` |
| `show ssl cert /etc/haproxy/ssl/../ssl/a.pem` | same error |
| `show ssl cert /etc/haproxy/ssl/a.pem` | the certificate |
| `show ssl crt-list -n list.txt` | `didn't find the specified filename` |

A pending transaction is addressed with a leading `*`:
`show ssl cert */etc/haproxy/ssl/a.pem` → `Status: Uncommitted` (3.4) /
`Status: Unused` (3.0) with the *new* serial.

## C1. `set ssl cert` + `commit ssl cert`

**One-line answer: `set` stages, `commit` swaps — the served certificate does not
change until commit, and one commit updates every bind referencing that path,
whether it came from `bind … crt` or from a crt-list.**

```
set ssl cert /etc/haproxy/ssl/a.pem <<
<PEM: key + cert>
<blank line>
                          -> Transaction created for certificate /etc/haproxy/ssl/a.pem!
commit ssl cert /etc/haproxy/ssl/a.pem
                          -> Committing /etc/haproxy/ssl/a.pem..
                             Success!
```

- Before commit: `s_client` still returns `OU=gen1`; `show ssl cert *<path>`
  shows `Status: Uncommitted` and the new serial.
- After commit: `OU=gen2` on port **8443 and 8444** — both binds referencing
  `a.pem` picked it up from the single commit.
- A crt-list-only certificate (`b.pem`) behaves identically: `set` + `commit`,
  and SNI `b.test` immediately serves `OU=gen2`.
- No reload, no connection drop.

## C2. `abort ssl cert`, and a second `set` before commit

**One-line answer: a second `set` replaces the pending copy (`Transaction
updated`, not an error), `abort` discards it, and a bad payload can only fail at
commit — the live certificate is never touched.**

| action | response |
|---|---|
| second `set` while one is pending | `Transaction updated for certificate /etc/haproxy/ssl/a.pem!` (the pending copy becomes the newer one) |
| `abort ssl cert <path>` | `Transaction aborted for certificate '/etc/haproxy/ssl/a.pem'!` |
| `abort` again | `No ongoing transaction!` |
| `commit` after `abort` | `No ongoing transaction! !` / `Can't commit /etc/haproxy/ssl/a.pem!` |
| `show ssl cert *<path>` after abort | `Can't display the certificate: Not found or the certificate is a bundle!` |

Broken payloads: a **certificate without its private key** is accepted by `set`
(`Transaction created`) and only rejected at commit —

- 3.4: `Committing …` / `unable to load SSL private key into SSL Context '…': passed a null parameter.` / `Failed!`
- 3.0: `The transaction must contain at least a certificate and a private key!` / `Can't commit …`

In both cases the served certificate is unchanged. **Always `commit` and check
the response; a successful `set` proves nothing.**

## C3. Bringing a new SNI certificate online without a reload

**One-line answer: `new ssl cert` → `set ssl cert` → `commit ssl cert` →
`add ssl crt-list`; the certificate is not served until the crt-list entry
exists, and the file need not exist on disk at all.**

```
new ssl cert /etc/haproxy/ssl/c.pem        -> New empty certificate store '/etc/haproxy/ssl/c.pem'!
set ssl cert /etc/haproxy/ssl/c.pem <<     -> Transaction created …
<PEM>
<blank line>
commit ssl cert /etc/haproxy/ssl/c.pem     -> Committing … / Success!
   ... SNI c.test still serves the bind default here ...
add ssl crt-list /etc/haproxy/ssl/list.txt /etc/haproxy/ssl/c.pem
                                           -> Inserting certificate '…c.pem' in crt-list '…list.txt'. / Success!
   ... SNI c.test now serves CN=c.test ...
```

- **`new ssl cert` works for a name with no file on disk** (`ghost.pem`): the
  store entry is created, filled and served purely from memory. The "path" is
  just an identifier.
- **`add ssl crt-list` resolves against the runtime store, not the filesystem.**
  `e.pem` existed on disk (2827 bytes) and was never loaded →
  `Can't edit the crt-list: certificate '/etc/haproxy/ssl/e.pem' does not exist!`
  After `new` + `set` + `commit`, the same `add` succeeds and `e.test` is served.
  There is **no runtime command that loads a certificate from disk** — the
  controller must always ship the PEM bytes over the CLI.

## C4. `add` / `del ssl crt-list`

**One-line answer: `add ssl crt-list <list> <cert> [ssl options] [sni filters]`
takes effect immediately, but the ssl options and SNI filters are only honoured
in the PAYLOAD form — the line form silently drops them and still reports
Success.**

Payload form (options and filters honoured):

```
add ssl crt-list /etc/haproxy/ssl/list.txt <<
/etc/haproxy/ssl/d.pem [alpn h2,http/1.1] alias.test *.wild.test
<blank line>
      -> Inserting certificate '/etc/haproxy/ssl/d.pem' in crt-list '…'. / Success!
```

`show ssl crt-list -n /etc/haproxy/ssl/list.txt`:

```
# /etc/haproxy/ssl/list.txt
/etc/haproxy/ssl/b.pem:1
/etc/haproxy/ssl/c.pem:2
/etc/haproxy/ssl/ghost.pem:3
/etc/haproxy/ssl/e.pem:4
/etc/haproxy/ssl/d.pem:5 [alpn h2,http/1.1 ] alias.test *.wild.test
```

Effective immediately: SNI `alias.test` and `x.wild.test` both serve `CN=d.test`.

**Line form drops the extras.** `add ssl crt-list <list> …/e.pem [alpn http/1.1] e2.test`
returns `Success!`, but the entry is stored bare (`/etc/haproxy/ssl/e.pem:6`, no
options, no filters) and SNI `e2.test` falls back to the bind default. Same for
`*.neg.test !bad.neg.test`. **Use the payload form for anything beyond a plain
path.**

`show ssl crt-list` variants:

- `show ssl crt-list` (no args) → the list of crt-list files.
- `show ssl crt-list <list>` → entries **without** line numbers.
- `show ssl crt-list -n <list>` → entries **with** `:<line>` numbers and the
  options/filters, i.e. the form you can diff against desired state.

Deletion:

| command | response |
|---|---|
| `del ssl crt-list <list> <cert>` | `Entry '<cert>' deleted in crtlist '<list>'!` — effective immediately, SNI falls back to the bind default |
| `del ssl crt-list <list> <cert>` (already gone) | `Can't delete the entry: certificate '<cert>' can't be found in crt-list '<list>'!` |
| `del ssl crt-list <list> <unknown>` | `Can't delete the entry: certificate '<cert>' does not exist!` |
| `del ssl cert <path>` while referenced | `Can't remove the certificate: certificate '<path>' in use, can't be deleted!` |
| `del ssl cert <path>` once unreferenced | `Certificate '<path>' deleted!` |

Removing the last crt-list entry is allowed and simply leaves the bind's own
`crt` as the only certificate.

## C5. `ca-file` on 3.0 and 3.4

**One-line answer: `show` / `new` / `set` / `add` / `commit` / `abort ssl ca-file`
all work on 3.0 and 3.4; `set` REPLACES the file, `add` APPENDS, and both need a
`commit` before they take effect.**

Verified against real mTLS (`bind :8444 … ca-file ca.crt verify optional`) with
one client cert per CA:

```
show ssl ca-file
   # filename
   /etc/haproxy/ssl/ca.crt - 1 certificate(s)
   @system-ca - 150 certificate(s)

                            client1 (ca)   client2 (ca2)
initial                     accepted       tlsv1 alert unknown ca
set ssl ca-file ca.crt <<   accepted       unknown ca          <- staged, not active
  (payload = ca2.crt)
commit ssl ca-file ca.crt   unknown ca     accepted            <- set REPLACED the file
add ssl ca-file ca.crt <<   unknown ca     accepted            <- staged, not active
  (payload = ca.crt)
commit ssl ca-file ca.crt   accepted       accepted            <- add APPENDED
```

- `set ssl ca-file <f> <<` → `transaction created for CA /etc/haproxy/ssl/ca.crt!`
- `commit ssl ca-file <f>` → `Committing /etc/haproxy/ssl/ca.crt.` / `Success!`
- `abort ssl ca-file <f>` → `Transaction aborted for certificate '…'!`
- `new ssl ca-file <f>` → `New CA file created '/etc/haproxy/ssl/newca.crt'!`
  (then listed with `0 certificate(s)` until a `set`+`commit`)
- `show ssl ca-file <f>` dumps every certificate in the file with Serial /
  notBefore / notAfter / Subject / Issuer — after the append it correctly showed
  `Certificate #1: /CN=ca2` and `Certificate #2: /CN=ca`.

---

## What this means for a controller

1. **Always use the payload form** (`<cmd> <<`) for map writes. The line form
   truncates on space/TAB and splits on `;`, which is both a data-loss and a
   command-injection bug.
2. **Reach the worker with `@1 <cmd> <<`** — `@@1` breaks on 3.0. Never chain
   `;` commands behind an `@1 <cmd>` prefix; only the first is relayed.
3. **Chunk payloads to ≤ 15 KiB** to stay portable: 3.0's cap is `tune.bufsize`
   (16 338 B usable), 3.4's is `tune.cli.max-payload-size` (131 072 B, keyword
   absent on 3.0). Over the cap **nothing** lands, so a controller that assumes
   3.4's headroom silently installs an empty map on 3.0.
4. **Keep command lines under 15 360 B** through the master relay
   (16 380 B direct) — the relay costs exactly `maxrewrite`. Do not rely on the
   master's over-limit message; it is a help dump, indistinguishable from an
   unknown command.
5. **Replace list-indexed maps (`reg`/`sub`/`dom`) wholesale**, never
   incrementally: append order decides precedence and appends always land last.
6. **Use `prepare` → N × `add map @<ver> <<` → `commit` for every map replace.**
   Verified atomic across multiple chunks on both versions (0 failures in 3599
   samples, one clean transition), ~0.2 s for 3000 entries. `clear map <path>`
   is not an alternative — it exposes a window where lookups miss. And
   `clear map @<ver>` is **not** an abort: it clears the pending generation on
   3.0 but is a no-op on 3.4, where committing afterwards installs the entries
   you meant to discard. To abandon a generation, simply never commit it.
7. **Do not assume `del map <key>` semantics** — 3.0 removes all duplicates,
   3.4 removes one. Use `del map <path> #<id>`, or replace the generation.
8. **Certificates are keyed by the exact config path string.** Ship PEM bytes
   with `new`/`set`/`commit`; there is no load-from-disk command, and
   `add ssl crt-list` resolves against the runtime store, not the filesystem.
   Check the `commit` response — `set` succeeds even on a key-less PEM.
9. **crt-list options and SNI filters require the payload form**; the line form
   accepts them, reports `Success!`, and silently discards them.
10. **Never trust a CLI response string as a success signal.** HAProxy can close
    before the reply is read; read back state (`show map` `entry_cnt`,
    `show ssl cert`) instead.
