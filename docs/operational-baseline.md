# WLTurbo failure baseline (Task 1 Step 1)

Evidence ledger for `github.com/bnema/wlturbo` at the start of `phase1/operational-health`. It records what the current checkout *does*; it is not a claim that the package is operational. Nothing was fixed.

## 1. Run metadata

- Repo: `/home/brice/dev/projects/wlturbo` — `git rev-parse HEAD` = `111649b1f3def6ff216191aa04d00eecbe14b435`, branch `phase1/operational-health`.
- Date of run: 2026-09-13T08:10:00+02:00. `go version` = `go version go1.27.1-X:nodwarf5 linux/amd64`. Platform `Linux x86_64`.
- Module `github.com/bnema/wlturbo`, `go 1.24`; only dependency `golang.org/x/sys v0.28.0`. `git status --porcelain` was empty at capture.
- File hashes at capture: `client.go` 25622cd3…, `core.go` 9507202e…, `scm_linux.go` 0a2344a5…, `client_test.go` a60e484a….
- A concurrent rewrite of framing/FD handling was expected; the tree was clean at the commit above when these commands ran, so line numbers are pinned there and may shift.

## 2. Command evidence (verbatim, trimmed)

### 2.1 `go test ./...` → exit 1

```
# github.com/bnema/wlturbo [github.com/bnema/wlturbo.test]
./client_test.go:50:8: event.ProxyId undefined (type *Event has no field or method ProxyId, but does have field ProxyID)
./client_test.go:63:49: event2.ProxyId undefined (type *Event has no field or method ProxyId, but does have field ProxyID)
./client_test.go:75:12: event.ProxyId undefined (type *Event has no field or method ProxyId, but does have field ProxyID)
./client_test.go:76:80: event.ProxyId undefined (type *Event has no field or method ProxyId, but does have field ProxyID)
FAIL	github.com/bnema/wlturbo [build failed]
?   	github.com/bnema/wlturbo/wl	[no test files]
FAIL
```

### 2.2 `go test -race ./...` → exit 1

Output is byte-identical to 2.1; the compiler lines are:

```
./client_test.go:50:8: event.ProxyId undefined (type *Event has no field or method ProxyId, but does have field ProxyID)
./client_test.go:63:49: event2.ProxyId undefined (type *Event has no field or method ProxyId, but does have field ProxyID)
./client_test.go:75:12: event.ProxyId undefined (type *Event has no field or method ProxyId, but does have field ProxyID)
./client_test.go:76:80: event.ProxyId undefined (type *Event has no field or method ProxyId, but does have field ProxyID)
FAIL	github.com/bnema/wlturbo [build failed]
```

### 2.3 `go vet ./...` → exit 1

```
# github.com/bnema/wlturbo
# [github.com/bnema/wlturbo]
vet: ./client_test.go:50:8: event.ProxyId undefined (type *Event has no field or method ProxyId, but does have field ProxyID)
```

Reproduction confirmed: this is a **build failure**, so no test body runs (with or without `-race`). The real field is `ProxyID` (`core.go:35`), and `client_test.go` still uses the old name `ProxyId` at four sites.

## 3. Transport inspection (`client.go`)

### 3.1 Message read path — `Display.Dispatch` (`client.go:345`)

- `client.go:346-347`: holds `d.recvMu` for the whole read (mutual exclusion between concurrent dispatchers).
- `client.go:350`: **single** `recvmsgWithFDs(d.headerBuf[:])` for the 8-byte header (`headerBuf [8]byte`, `client.go:71`).
- `client.go:353-355`: `n < 8` → `incomplete header: got %d bytes`; a short read is an error, never retried.
- `client.go:358-362`: `objectID` little-endian, `size = sizeOpcode >> 16`, `opcode = sizeOpcode & 0xffff`.
- `client.go:367-369`: `if opcode > 0xFFFF` → error. **Unreachable**: `opcode` was just masked with `0xffff`.
- `client.go:372-397`: if `size > 8`, a **second** `recvmsgWithFDs(body)` reads `size-8` bytes; if still short, `io.ReadFull(d.conn, remaining)` (`client.go:392`) is the only path that guarantees a full body.
- FDs from both reads are appended to a local slice (`client.go:396`); that slice is then **never used** in `Dispatch`.

### 3.2 Header validation present / missing

Present: the short-read check (`client.go:353`) and the unreachable opcode check (`client.go:367`). Missing, by inspection of `Dispatch`:

- no **maximum message size** check; `size` is 16-bit so ≤ 65535, but nothing states or enforces that bound;
- no **4-byte alignment** check on `size`;
- no **unknown object ID** rejection — `client.go:431` only logs `WARNING: No object found for ID %d` and falls through to `d.dispatcher.Dispatch` (`client.go:439`);
- no **unknown opcode** rejection per object.

### 3.3 Buffering

`headerBuf [8]byte` (`client.go:71`) and `eventBodyBuf [4096]byte` (`client.go:74`) live on the `Display` and are reused for every event (`client.go:378-379`); larger bodies are allocated fresh (`client.go:381`). So the receive buffers are connection-local, but the small-body path returns a sub-slice of `d.eventBodyBuf` with no copy — a handler retaining the body sees it overwritten by the next `Dispatch`. `Event`s are likewise allocated fresh at `client.go:418` although a pool exists (`event_pool.go:12`).

### 3.4 Lifecycle

- `Display.Close` (`client.go:213-215`) is a bare `return d.conn.Close()` with no closed flag, so it is **not idempotent**. External probe (socketpair + `net.FileConn`, run outside the repo): `first Close: <nil>`, `second Close: "close unix @->@: use of closed network connection"`.
- `Dispatch` after close: `recvmsgWithFDs` → `ReadMsgUnix` returns the closed error (`scm_linux.go:97`), wrapped as `failed to read header: %w` (`client.go:351`). Same probe: `ReadMsgUnix after close: "read unix @->@: use of closed network connection"`. It errors rather than panics, but there is no guard or sentinel.
- `Connect` (`client.go:145`) calls `conn.(*net.UnixConn).File()` (`client.go:170`), stores the fd (`client.go:175`) and closes the `*os.File` immediately (`client.go:177`); `d.fd` is **never read again** anywhere (verified by grep).

### 3.5 Hot-path `log.Printf`

Unconditional per event / per allocation, using the default logger to stderr: `client.go:408` (every non-registry event), `:413` (server-created object), `:425` (proxied dispatch), `:431` (unknown object), `:230` (every `allocateID()`), `:795` and `:836` (`OutputHead.Dispatch`, `OutputMode.Dispatch`).

## 4. File-descriptor handling (`scm_linux.go`, `core.go`)

### 4.1 `globalFDQueue`

- Package-global `globalFDQueue = &fdQueue{}` (`scm_linux.go:29`), shared by every `Display` in the process.
- `items [256]atomic.Pointer[fdItem]` with monotonic `head`/`tail` counters; slot index is `counter & 255` (`scm_linux.go:52`, `:76`). The wraparound risk is capacity, not the counters: a producer 256 descriptors ahead of the consumer revisits an occupied slot.
- `enqueueFD` (`scm_linux.go:47`) spins on `q.items[next].CompareAndSwap(nil, item)`; when the slot is occupied it **spins forever** — no bound, no lossy path.
- `dequeueFD` (`scm_linux.go:65`) is single-consumer; when the slot loads `nil` despite `head < tail` it `continue`s (unbounded busy spin). On success it CASes `head`, reads `item.fd`, clears the slot and returns the item to `fdItemPool`.

### 4.2 Queueing — `recvmsgWithFDs` (`scm_linux.go:91`)

- The pooled control buffer is `unix.CmsgSpace(4*4)` — **space for only 4 FDs** (`scm_linux.go:41`); `ReadMsgUnix`'s flags are discarded (`scm_linux.go:97`), so `MSG_CTRUNC` is never detected and excess FDs are lost.
- On `SCM_RIGHTS` (`scm_linux.go:112-123`) every FD is **enqueued into the global queue** (`:119`) *and* also returned in the `fds` slice (`:122`); `Dispatch` discards the returned copy. Neither copy is closed or accounted for.
- Parse errors (`scm_linux.go:104`, `:110`) return without closing any FD already parsed from the same control message.
- `d` is otherwise unused for FDs: `sendmsgWithFDs` (`scm_linux.go:131`) writes via `d.conn`, and `d.fd` is never consulted.

### 4.3 Consumption & lifetime — `Event.Fd` (`core.go:224`)

- `Event.Fd()` ignores `e.data` and pops the **global** queue via `GetNextFD()` (`core.go:227`, `scm_linux.go:150`); only on an empty queue does it read a placeholder `uint32` and return `0` (`core.go:232`). The descriptor is therefore not tied to this event, this `Display`, or even this connection — only to global FIFO order.
- No path closes received FDs: not `Close` (`client.go:213`), not `Dispatch` error paths, not `recvmsgWithFDs` errors. The only `unix.Close` calls found are for SHM buffers (`shm.go:49`) and inside `CreateAnonymousFile` (`scm_linux.go`).
- `d.fd` (from `conn.(*net.UnixConn).File()`, `client.go:170-177`) is **dead**: the `*os.File` is closed immediately, so the stored int is a stale, unowned descriptor number; the read/send paths use `d.conn`.

## 5. Test coverage inventory (`client_test.go`)

Covered (10 functions: 8 tests + 2 benchmarks; none execute today): `TestFixed` (11), `TestEventPool` (41, uses the old name `ProxyId`), `TestEventDispatcher` (69), `TestEventDispatcherMultipleHandlers` (91), `TestMessageMarshalingBasic` (121), `TestMessageHeaderParsing` (178 — re-implements the header arithmetic locally and does **not** call `Display.Dispatch`), `TestAllocateID` (227), `TestRegistryGlobalStorage` (248), `BenchmarkEventDispatch` (292), `BenchmarkFixedConversion` (310).

Untested (no test touches these): `Dispatch` framing (single header read, second body read, `io.ReadFull` fallback, short reads); `recvmsgWithFDs` / `sendmsgWithFDs` / `SCM_RIGHTS` / `globalFDQueue` / `Event.Fd` and FD lifetime; lifecycle (`Connect`, `Close`, double-close, use-after-close); concurrency (`recvMu`, `sendMu`, parallel `Dispatch`, `-race` paths); `handleDisplayEvent`, `handleServerObject`, `Roundtrip`.

Live compositor: **no** test needs one — `client_test.go:8` states this, and a grep for `Connect`/`Dial`/`WAYLAND_DISPLAY`/`SCM_RIGHTS` in the file finds nothing. The blocker is purely the compile error.

## 6. Classification

**Reproduced (executed):** (1) `go test ./...` build failure on `ProxyId`, lines 50/63/75/76 — §2.1, exit 1; (2) `go test -race ./...` identical — §2.2, exit 1; (3) `go vet ./...` same symbol — §2.3, exit 1; (4) `Close` is not idempotent — §3.4, external probe result `close unix @->@: use of closed network connection`.

**Verified (read from code, with `file:line`):** `Event.ProxyID` exists (`core.go:35`); header read is one `ReadMsgUnix`, body is a second read plus `io.ReadFull` fallback (`client.go:350`, `:385`, `:392`); the `opcode > 0xFFFF` check is unreachable (`client.go:362` vs `:367`); unknown object IDs are only logged (`client.go:431`, `:439`); no max-size or alignment check exists in `Dispatch`; receive buffers are per-`Display` and reused (`client.go:71`, `:74`, `:379`); `d.fd` is set and never used (`client.go:170-177`); `globalFDQueue` is package-global (`scm_linux.go:29`); `enqueueFD`/`dequeueFD` can spin unbounded (`scm_linux.go:47-88`); control buffer holds 4 FDs and truncation flags are ignored (`scm_linux.go:41`, `:97`); hot-path logs (`client.go:230`, `:408`, `:413`, `:425`, `:431`, `:795`, `:836`); fresh `&Event{}` bypasses the pool (`client.go:418` vs `event_pool.go:12`); no test covers framing/SCM/lifecycle/concurrency and none needs a compositor (§5).

**Suspected (derived from code, not executed because the package does not build):** the retained body sub-slice is clobbered by the next `Dispatch` (`client.go:379`); received FDs leak on parse error or shutdown (no `close` on the queue path — `scm_linux.go:104-122`, `core.go:227`); `Event.Fd` can return a descriptor belonging to another event or connection because the queue is global (`core.go:227`); FD loss on `MSG_CTRUNC` (`scm_linux.go:41`, `:97`); `Dispatch` after close returns a wrapped error with no sentinel (`client.go:351`).
