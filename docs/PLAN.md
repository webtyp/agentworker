---
PLAN: "feat: EventReply carries the turn's timings — every decision and the whole turn, in ms"
EXECUTOR: jules
REVIEWER: none
STATUS: running
SESSION: 12012621688757113334
---

> Read AGENTS.md at the repo root before any change.

# Plan — `agentworker`: how long each decision took

The first real use of Cote must answer "where does the time go?" (≈ 4 min the first answer, ≈ 18 s
later ones, with no breakdown). The decision model is built inside the Worker (`core.start`,
`core.go:142`) and the application cannot wrap it (`Setup.AgentConfig` doc: agentworker overrides
`Decider`). So agentworker measures it: every `Decide` call and the whole turn, sent with the reply.

## Tools

```bash
go install webtyp.com/devflow/cmd/gotest@latest
```

## Ecosystem search

`agent` logs only tool durations (`MemoryStore.LogToolCall(..., durationMS)`, `agent/turn.go:195`),
not decisions, and lives in the Worker. `llm.Decider` is a one-method interface and `agent` never
type-asserts it (checked: no `.(` on the decider in `agent/*.go`), so a wrapper is safe. Clock:
`webtyp.com/time` `NowNano() Nano`. Nothing to reuse for "timings across page ↔ Worker" → new
fields on the existing `Event`.

## Design gate

1. **Prior art.** OpenTelemetry spans (name + duration per step); the Chrome DevTools performance
   panel (per-task durations); llama.cpp's `timings` object in every `/completion` response
   (`prompt_ms`, `predicted_ms`) — the closest: timings travel *with the answer*. We follow
   llama.cpp: two plain fields on the reply, no tracing system.
2. **Novice names.** `Event.DecideMs` ("milliseconds of each decision"), `Event.TurnMs`
   ("milliseconds of the whole turn").
3. **Ledger.** Concepts +2 fields / −0; consumers that ignore them change nothing.
4. **Where.** agentworker owns page ↔ Worker messages (`protocol.go`) and builds the decider.
5. **Deletes.** Nothing: new capability.

## APIs used

| Symbol | Signature | Tag | File |
|---|---|---|---|
| `llm.Decider` | `interface { Decide(ctx *context.Context, q Question) (Decision, error) }` | llm v0.2.3 | `decide.go:10` |
| `time.NowNano` | `func NowNano() Nano`; `type Nano int64` (nanoseconds) | time v0.6.2 | `api.go:38` |
| `model.ArrayWriter.Int` / `ArrayReader.Int` | `Int(val int64)` / `Int(i int) int64` | model v0.2.4 | `codec.go:22-57` |

If a signature in the published tag differs from this table, stop and name the symbol in the PR;
never adapt it by guessing.

## Stage 1 — clock and the wrapper (`core.go`, `timing.go` new)

`go get webtyp.com/time@v0.6.2` (today indirect) and `go mod tidy`.

`core` gets two fields: `now func() time.Nano` and `decides []int64`. `worker.go:26` sets
`now: time.NowNano`.

```go
// timing.go
const nanosPerMilli = 1_000_000

// timedDecider measures every decision of the agent's turn into c.decides.
type timedDecider struct {
	llm.Decider
	c *core
}

func (t timedDecider) Decide(ctx *context.Context, q llm.Question) (llm.Decision, error) {
	start := t.c.now()
	d, err := t.Decider.Decide(ctx, q)
	t.c.decides = append(t.c.decides, int64(t.c.now()-start)/nanosPerMilli)
	return d, err
}
```

In `start` (`core.go`, the line `cfg.Decider, cfg.Writer, cfg.Tokens = d, wr, d`):
`cfg.Decider = timedDecider{Decider: d, c: c}`. Keep `c.decider = d` (the cache calls stay on the
real model).

## Stage 2 — the turn (`core.handle`)

For `RequestRun`, `RequestConfirm`, `RequestDecline`: `c.decides = c.decides[:0]` and
`start := c.now()` before the switch; after it, build the reply event with
`replyEvent(r.SessionID, reply.Text, reply.Pending)` then set `e.DecideMs` to a **copy** of
`c.decides` (`append([]int64(nil), c.decides...)`) and `e.TurnMs = int64(c.now()-start)/nanosPerMilli`.
`saveDecisionCache()` stays after the event is built and is not timed.

## Stage 3 — protocol (`protocol.go`)

`Event` gains, after `Pending`:

```go
DecideMs []int64 // EventReply: each question to the decision model, in order, in ms
TurnMs   int64   // EventReply: the whole turn (decisions, tools, writer), in ms
```

Codec, same style as `shortfalls`: encode `decide_ms` (array of ints, only when non-empty) and
`turn_ms` (only when non-zero); decode both. Update the `EventReply` comment on the const
(`protocol.go:35`) to `// SessionID, Text, Pending, DecideMs, TurnMs`.

## Stage 4 — tests

`core_internal_test.go`: the harness sets `now` to a fake clock that advances 7 ms per call:

```go
var tick time.Nano
h.c.now = func() time.Nano { tick += 7 * nanosPerMilli; return tick }
```

| Setup | Call | Expect |
|---|---|---|
| started harness (`h.start()`), `fakeDecider` counting its `Decide` calls in a new field `decides int` | `e := h.run("hi")` | `len(e.DecideMs) == h.dec.decides`, every value `7`, `e.TurnMs == 7*(2*h.dec.decides+1)` |
| same, two turns | `h.run("a")`, `e := h.run("b")` | `e.DecideMs` holds only the second turn's decisions |

`tests/protocol_test.go`: an `EventReply` with `DecideMs: []int64{5, 9}, TurnMs: 40` round-trips
through `Encode`/`DecodeEvent` unchanged; one without them encodes no `decide_ms`/`turn_ms` key.

## Stage 5 — docs

`README.md:48` comment becomes `// e.Text; e.Pending → …; e.DecideMs, e.TurnMs → where the time
went`. `docs/ARCHITECTURE.md`: one paragraph "Timings" (what is measured, that the decision cache
save is not, that writer + tools = `TurnMs − sum(DecideMs)`).

## Delivery check

```bash
GOFLAGS=-mod=mod GOPRIVATE=webtyp.com/* go build ./...
gotest
gotest -tinygo
```

A PR that does not compile, or whose tests could not run, is a failed delivery. Never pause to ask
permission or report progress: when the check passes, commit, push and open the PR. Stuck for 30
minutes → open the PR anyway, listing what fails.

## Stages

| # | What | Files |
|---|---|---|
| 1 | clock, wrapper | `go.mod`, `go.sum`, `timing.go` (new), `core.go`, `worker.go` |
| 2 | timed turn | `core.go` |
| 3 | protocol fields + codec | `protocol.go` |
| 4 | tests | `core_internal_test.go`, `tests/protocol_test.go` |
| 5 | docs | `README.md`, `docs/ARCHITECTURE.md` |
