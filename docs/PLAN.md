---
PLAN: "feat: Serve and Start — the agent in a Web Worker, with device check, downloads, decision cache and typed events"
TAG: v0.1.0
EXECUTOR: jules
REVIEWER: none
---

> This plan is dispatched via the CodeJob workflow. See skill: agents-workflow.

# Plan — `webtyp/agentworker` v0.1.0

Master plans: [PWA_ARTIFACTS_MASTER_PLAN.md](https://github.com/webtyp/app/blob/main/docs/PWA_ARTIFACTS_MASTER_PLAN.md)
(D-PWA-2, 3, 8, 9, 10, 17) and the agent wave
[AGENT_ECOSYSTEM_MASTER_PLAN.md](https://github.com/webtyp/agent/blob/main/docs/AGENT_ECOSYSTEM_MASTER_PLAN.md)
(D10 model files from OPFS, D16 SIMD + plain binaries, D25 tiers, D27 decision cache).
**Read [AGENTS.md](../AGENTS.md) first**: browser library, TinyGo decides, forbidden imports, layout.
This repo is a fresh `gonew`: `agentworker.go` holds a placeholder `Agentworker`/`New()` — **delete it**.

## Why

The in-browser assistant (Cote, in a clinic's CMS) must run its models in a Web Worker so the page
never freezes. Every piece exists and is published; nobody wires them:

| Piece | Published API used here |
|---|---|
| `webtyp.com/js` v0.1.1 | `js.ServeWorker(h)` (Worker main, one message at a time), `js.PostToPage(msg)` (Worker → page outside a reply), `js.NewWorker(scriptURL, onReply)`, `(*Worker).Post`, `(*Worker).Terminate`, `js.Message{Data []byte}`, `js.WebWorkerHandler{OnMessage(ctx, msg) (*Message, error)}` |
| `webtyp.com/device` v0.1.0 | `Detect() (Profile, error)` (page **and** Worker), `Persist() (bool, error)` (**page only**: `navigator.storage.persist` does not exist in Workers), `Bench(work, budgetMs) Rate`, `Requirement.Check(p, rate) []Shortfall`, `Shortfall.Blocking()`, `Shortfall.String()` |
| `webtyp.com/artifacts` v0.1.1 | `ParseManifest`, `Manifest.Find(id)`, `New(files.Store) *Store`, `Has`, `Ensure(a, profile, progress)`, `Read`, `Prune(keep)`, `ErrNoSpace`, `ManifestPath` (`/artifacts.json`) |
| `webtyp.com/opfs` v0.1.2 | `Open(dir) (*FS, error)` — implements `files.Store` |
| `webtyp.com/weights` v0.3.0 | `Open(src []byte) (*Artifact, error)` |
| `webtyp.com/qwen` | `New(qwen.Config{Weights, Merges, Decoder, DecideTemperature}) (*Model, error)`; `*Model` is an `llm.Decider` and an `llm.TokenCounter`; `SaveDecisionCache() ([]byte, bool, error)`, `LoadDecisionCache([]byte) error` |
| `webtyp.com/lfm` | `New(lfm.Config{Weights, Merges, Decoder}) (*Model, error)`; `*Model` is an `llm.Client` |
| `webtyp.com/agent` v1.0.0 | `New(agent.Config) (*Agent, error)`; `Run(ctx, sessionID, text) (Reply, error)`, `Confirm(ctx, sessionID)`, `Decline(ctx, sessionID)`; `Reply{Text string; Pending []llm.ToolCall}`; `Config.Decider` (required), `Config.Writer` (optional), `Config.Tokens` (required) |
| `webtyp.com/nn` | `MatVecQ8Block32(dst, xq, xs, q, scales, rows, cols)` — the benchmark kernel |

## Design gate

1. **Prior art.**
   - **WebLLM** (`CreateWebWorkerMLCEngine` + `WebWorkerMLCEngineHandler`): the page holds a proxy
     engine, the Worker the real one; messages are typed requests; model download progress is
     reported through an `initProgressCallback` while loading. We take: page proxy + typed messages
     + progress events.
   - **transformers.js** in a Worker (its README example): the page posts `{text}`, the Worker
     posts `{status: "progress" | "ready" | "complete", ...}`. We take: the status-tagged event
     stream, typed instead of ad hoc objects.
   - **Comlink** (Google Chrome Labs) makes a Worker look like an object with async methods. We do
     not: our methods are three (`Run`, `Confirm`, `Decline`) and the stream of progress events
     needs explicit kinds anyway.
2. **Novice-name test.** Worker main: `agentworker.Serve(setup)`. Page: `c, err :=
   agentworker.Start(scripts, onEvent)`, `c.Run(session, text)`, `c.Confirm(session)`,
   `c.Decline(session)`, `c.Stop()`. Event kinds: `EventProgress`, `EventReady`, `EventAsleep`,
   `EventReply`, `EventFailed`.
3. **Complexity ledger.** +1 library; an application writes one Worker `main` (≈ 15 lines: `Serve`
   with its `agent.Config` builder) and calls `Start` from the click that wakes the assistant.
   Ways to run the agent in the browser: 1.
4. **Where it belongs.** Its own repo (accepted in the agent wave): it depends on the models
   (`qwen`, `lfm`) that the contracts (`agent`, `llm`) must never import.
5. **What it deletes.** The `gonew` placeholder. Otherwise new capability.

## Stage 1 — `setup.go` (no build tag): what the application declares

```go
// DeciderSpec names the decision model's files in the artifacts manifest and its shape.
type DeciderSpec struct {
	Weights     string         // artifact id of the weights, e.g. "decider-0.8b"
	Merges      string         // artifact id of the companion .merges file
	Decoder     decoder.Config // qwen.Qwen35_08B for decider-0.8b
	Temperature float64        // decision temperature (decider-0.8b: 1.03)
}

// WriterSpec names the optional writer model's files and its shape.
type WriterSpec struct {
	Weights string         // e.g. "writer-lfm-350m"
	Merges  string
	Decoder decoder.Config // lfm.LFM25_350M
}

// Models are what agentworker built; the application's AgentConfig receives them.
type Models struct {
	Decider llm.Decider
	Writer  llm.Client       // nil when the device could not take the writer (D-PWA-2)
	Tokens  llm.TokenCounter // the decider's tokenizer
}

// Setup is everything the Worker binary declares.
type Setup struct {
	Dir     string       // OPFS directory of this module (D-PWA-10), e.g. "cote"
	Decider DeciderSpec  // required
	Writer  *WriterSpec  // optional; dropped when the device cannot hold or run it
	// AgentConfig returns the application's agent configuration (Texts, Templates, Guard, Memory,
	// IDGen, Clock, ToolIndex, LocalTools, MCPServers...). agentworker then sets Decider, Writer
	// and Tokens from m, overriding whatever the function put there.
	AgentConfig func(m Models) (agent.Config, error)
}
```

Constants: `DecisionCacheFile = "decision.cache"` (file name in `Setup.Dir`), `BenchBudgetMs = 200`.

## Stage 2 — `protocol.go` (no build tag): typed messages, `webtyp.com/json`

```go
type RequestKind uint8
const (
	RequestStart RequestKind = iota + 1 // check the device, download, load; then Ready or Asleep
	RequestRun                          // a message from the person
	RequestConfirm                      // confirm the pending calls
	RequestDecline                      // decline them
)

type Request struct {
	Kind      RequestKind
	SessionID string
	Text      string // RequestRun only
	Persisted bool   // RequestStart only: what the page's device.Persist() answered (D-PWA-9)
}

type EventKind uint8
const (
	EventProgress EventKind = iota + 1 // Artifact, Done, Total
	EventReady                         // WriterOff, Persisted
	EventAsleep                        // Artifact, Shortfalls: the device cannot run the assistant
	EventReply                         // SessionID, Text, Pending
	EventFailed                        // Text: what went wrong
)

type Pending struct{ ID, Name, Input string } // from llm.ToolCall

type Event struct {
	Kind       EventKind
	Artifact   string   // EventProgress, EventAsleep: artifact id
	Done       int64    // EventProgress
	Total      int64    // EventProgress
	WriterOff  bool     // EventReady: the writer was dropped (answers come from templates/data)
	Persisted  bool     // EventReady: storage is persistent; false → the UI warns (D-PWA-9)
	Shortfalls []string // EventAsleep: device.Shortfall.String() of every blocking shortfall
	SessionID  string   // EventReply
	Text       string   // EventReply, EventFailed
	Pending    []Pending
}

func (r Request) Encode() ([]byte, error)
func DecodeRequest(data []byte) (Request, error)
func (e Event) Encode() ([]byte, error)
func DecodeEvent(data []byte) (Event, error)
```

Encode with `webtyp.com/json` through `model.Encodable` / `model.Decodable` (`EncodeFields`,
`DecodeFields`, `IsNil` — the pattern of `webtyp.com/artifacts`' `manifest.go` and `encode.go`).
JSON field names: `kind`, `session_id`, `text`, `persisted`, `artifact`, `done`, `total`,
`writer_off`, `shortfalls`, `pending` (`id`, `name`, `input`). A message that does not decode is an
error `agentworker: bad message: %v`. **No `encoding/json`.**

## Stage 3 — `core.go` (no build tag): the Worker's logic, testable natively

```go
type core struct {
	setup   Setup
	store   *artifacts.Store
	files   files.Store                       // same directory: the decision cache lives here
	detect  func() (device.Profile, error)
	bench   func() device.Rate                // device.Bench(benchKernel, BenchBudgetMs) in the Worker
	post    func(Event)                       // js.PostToPage in the Worker; a slice in tests
	manifestURL string                        // artifacts.ManifestPath
	newDecider func(w *weights.Artifact, merges []byte, s DeciderSpec) (decider, error)
	newWriter  func(w *weights.Artifact, merges []byte, s WriterSpec) (llm.Client, error)
	agent   *agent.Agent                      // nil until ready
	decider decider
	cacheSaved bool
}

// decider is what agentworker needs from the decision model (qwen.Model satisfies it).
type decider interface {
	llm.Decider
	llm.TokenCounter
	SaveDecisionCache() ([]byte, bool, error)
	LoadDecisionCache([]byte) error
}
```

`func (c *core) handle(r Request) (*Event, error)` — the reply for one request:

**`RequestStart`** (runs the steps below; each failure posts `EventFailed` with the error text and
returns it):
1. `p, err := c.detect()`; `rate := c.bench()`.
2. Fetch `c.manifestURL` with `webtyp.com/fetch` (`fetch.Get(url).Send(cb)` + a channel, as
   `artifacts` does) → `artifacts.ParseManifest`.
3. Find the decider's weights and merges (`Find`; missing → error
   `agentworker: the manifest has no artifact %q`). For the weights, `Needs.Check(p, rate)`: any
   **blocking** shortfall → post nothing else and return `&Event{Kind: EventAsleep, Artifact: id,
   Shortfalls: names}` (D-PWA-2: the assistant sleeps with a clear reason).
4. Writer (when `setup.Writer != nil`): find its artifacts; it is **dropped** (`WriterOff = true`,
   no error) when its weights' `Check` has a blocking shortfall, or when `p.Free()` is smaller than
   what both models still need to download (`Size` of every not-yet-`Has` artifact of both).
5. `Ensure` each needed artifact (decider weights, decider merges, then writer's), posting
   `EventProgress{Artifact, Done, Total}` from the progress callback — at most one event per 1 %
   of `Total` (keep the last percentage posted) plus the final one. `artifacts.ErrNoSpace` on a
   writer file → drop the writer (step 4's rule) and continue; on a decider file → `EventAsleep`
   with `Shortfalls: ["space"]`.
6. `Read` + `weights.Open` + `newDecider` / `newWriter`.
7. Decision cache (D27): read `DecisionCacheFile` from `c.files`; when present, `LoadDecisionCache`;
   if that errors, `RemoveFile` it and go on (it belongs to other weights).
8. `cfg, err := setup.AgentConfig(Models{...})`; set `cfg.Decider`, `cfg.Writer`, `cfg.Tokens`;
   `agent.New(cfg)`.
9. `store.Prune(keep)` with the artifacts in use (D-PWA-8: older versions go only now).
10. Return `&Event{Kind: EventReady, WriterOff: ..., Persisted: r.Persisted}`.

A second `RequestStart` after ready returns `EventReady` again without redoing anything.

**`RequestRun` / `RequestConfirm` / `RequestDecline`** before ready → error
`agentworker: not ready; send RequestStart first`. Otherwise call `agent.Run/Confirm/Decline` and
return `EventReply{SessionID, Text, Pending}`. After the **first** reply of this Worker start, when
the cache was not loaded in step 7: `SaveDecisionCache()`; when `ok`, `WriteFile(DecisionCacheFile,
data)`; set `cacheSaved` (never more than once per start: the tool-list prefix does not change while
the Worker lives). A save error is not fatal: post `EventFailed` with it and keep the reply.

## Stage 4 — `worker.go` (`//go:build wasm`): `Serve`

```go
// Serve runs the agent in this Web Worker. Call it from the Worker binary's main; it never
// returns. Requests are answered one at a time; RequestStart reports progress with
// js.PostToPage before its reply.
func Serve(s Setup)
```

Builds `core` with: `opfs.Open(s.Dir)` (error → every request answers `EventFailed`),
`artifacts.New(fs)`, `detect: device.Detect`, `bench: func() device.Rate { return
device.Bench(benchKernel(), BenchBudgetMs) }`, `post: func(e Event) { data, _ := e.Encode();
js.PostToPage(&js.Message{Data: data}) }`, `manifestURL: artifacts.ManifestPath`, `newDecider` →
`qwen.New(qwen.Config{Weights, Merges, Decoder: s.Decoder, DecideTemperature: s.Temperature})`,
`newWriter` → `lfm.New(lfm.Config{...})`. Then `js.ServeWorker(handler)` where the handler decodes
the request, calls `core.handle`, and encodes the returned event (nil → nil reply).

`benchKernel()` (unexported, in `bench.go`, no build tag) returns a `func()` that runs
`nn.MatVecQ8Block32` once on a fixed 3584×1024 int8 matrix (deterministic fill, allocated once) — the
shape measured in `nn/docs/PERFORMANCE.md` (1.30 ms plain, 0.61 ms SIMD under TinyGo 0.41). Document
on `BenchBudgetMs` and in the README: **a manifest's `min_rate` is in runs per second of this
kernel** (≈ 770 plain, ≈ 1640 SIMD on that machine).

## Stage 5 — `page.go` (`//go:build wasm`): `Start`, `Client`

```go
// Scripts are the Worker scripts of one assistant: the same Worker main built twice (D16).
type Scripts struct {
	Plain string // e.g. "/cote.worker.js"
	SIMD  string // e.g. "/cote.simd.worker.js"; "" = only the plain build exists
}

// Start wakes the assistant. Call it from the click that wakes it (a user gesture): it asks the
// browser to persist storage first (D-PWA-9), picks the SIMD script when the browser supports
// SIMD and one exists, starts the Worker and sends RequestStart. onEvent receives every event,
// progress included, on the page's goroutine. Call it from a goroutine: it blocks on promises.
func Start(s Scripts, onEvent func(Event)) (*Client, error)

type Client struct{ /* *js.Worker */ }
func (c *Client) Run(sessionID, text string)
func (c *Client) Confirm(sessionID string)
func (c *Client) Decline(sessionID string)
func (c *Client) Stop() // terminates the Worker
```

`Start`: `persisted, _ := device.Persist()`; `p, _ := device.Detect()`; script = `s.SIMD` when
`p.SIMD && s.SIMD != ""`, else `s.Plain` (`s.Plain == ""` → error `agentworker: Scripts.Plain is
required`); `js.NewWorker(script, onReply)` where `onReply` decodes the bytes into an `Event` (a
handler error → `EventFailed{Text: err.Error()}`; undecodable → `EventFailed` with the decode error)
and calls `onEvent`; then post `RequestStart{Persisted: persisted}`.

## Stage 6 — tests

`core_internal_test.go` (root, `package agentworker`, native) with a fake decider (counts calls;
`SaveDecisionCache` returns fixed bytes), a fake writer, `files/mem` as both store and files, an
`httptest.Server` serving a manifest and small artifact files with `http.ServeContent` (Range), and a
`post` that appends to a slice:

| Test | Proves |
|---|---|
| `TestStart_DownloadsLoadsAndIsReady` | manifest with decider weights + merges and writer weights + merges → progress events for each artifact ending at `Done == Total`, then `EventReady{WriterOff: false}`; files present in the store |
| `TestStart_AsleepWhenDeviceShort` | decider `needs.min_tier: "simd"`, profile without SIMD → `EventAsleep{Artifact: decider, Shortfalls: ["tier"]}`, zero artifact requests |
| `TestStart_InsecureContextSleeps` | `Profile{Secure: false}` → `EventAsleep` with `"secure"` |
| `TestStart_DropsWriterWhenNoSpaceForBoth` | free space fits the decider only → `EventReady{WriterOff: true}`, writer never downloaded, `Models.Writer == nil` given to `AgentConfig` |
| `TestStart_DropsWriterOnItsShortfall` | writer needs a rate the bench does not reach → `WriterOff: true` |
| `TestStart_LoadsDecisionCache` | `decision.cache` present → `LoadDecisionCache` called with its bytes; first Run does **not** save |
| `TestStart_BadDecisionCacheRemoved` | `LoadDecisionCache` errors → file removed, still ready |
| `TestRun_SavesDecisionCacheOnce` | no cache file → after the first Run the file holds `SaveDecisionCache`'s bytes; a second Run does not write again |
| `TestRun_BeforeStartFails` | `RequestRun` first → the not-ready error |
| `TestStart_PrunesOldVersions` | an older version of the decider stored before → removed after ready |
| `TestStart_ProgressThrottled` | a 10 MiB artifact → at most 101 progress events for it |

`tests/protocol_test.go` (native, black-box): round trip of every `Request`/`Event` kind through
`Encode`/`Decode*`, including `Pending` and `Shortfalls`; a malformed message → the error text.

`tests/page_test.go` (`//go:build wasm`, headless browser): `Start(Scripts{}, ...)` → the
`Scripts.Plain is required` error. (A real Worker needs a built Worker binary and script; that
end-to-end test belongs to the application, see README.)

## Stage 7 — docs

- `README.md`: what it is; the two halves with one example each — the Worker `main` (≈ 15 lines:
  `agentworker.Serve(agentworker.Setup{Dir: "cote", Decider: ..., Writer: ..., AgentConfig: ...})`)
  and the page (`Start` from the wake-up click, a `switch e.Kind` over events, `c.Run`); the
  manifest artifacts it expects (decider weights + merges, optional writer weights + merges);
  the unit of `min_rate`; that `device.Persist()` runs on the page because Workers do not have it.
- `docs/ARCHITECTURE.md`: a mermaid sequence diagram page ↔ Worker (Start → progress… → Ready |
  Asleep; Run → Reply; Confirm/Decline), the degrade order (writer dropped → asleep, D-PWA-2), the
  decision cache rule (D27, saved once per start), pruning after ready (D-PWA-8), and why the
  application builds its own Worker binary (its in-process tools and memory live there).

## Acceptance

- `gotest` and `gotest -tinygo` green. Never run `gopush` or `codejob`.
- `grep -rn '"encoding/json"\|"net/http"\|map\[' --include=*.go . | grep -v _test` → empty.
- `grep -rn "type Agentworker\|func New()" --include=*.go .` → empty.

| Stage | Files | Done when |
|---|---|---|
| 1 | `setup.go` | declaration types |
| 2 | `protocol.go` | typed messages + JSON |
| 3 | `core.go`, `bench.go` | Worker logic over injected dependencies |
| 4 | `worker.go` | `Serve` |
| 5 | `page.go` | `Start`, `Client` |
| 6 | `core_internal_test.go`, `tests/*` | tables green |
| 7 | `README.md`, `docs/ARCHITECTURE.md` | written |
