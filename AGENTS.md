# AGENTS.md — webtyp/agentworker

Working notes for AI agents operating in this library. End-user docs: [README.md](README.md).

## Mission

`agentworker` runs a `webtyp.com/agent` inside a **Web Worker** of a webtyp application: it checks
the device (`webtyp.com/device`), downloads the model files once with progress
(`webtyp.com/artifacts`, kept in OPFS), opens them (`webtyp.com/weights`, `qwen`, `lfm`), keeps the
decision cache between starts, builds the agent with what the application provides, and answers
the page's requests. The page side (`Start`, `Client`) picks the Worker binary for the device's
tier and turns the Worker's messages into typed events.

It does not draw any UI, does not decide what the agent says (that is the application's
`agent.Config`) and does not define the agent, the model or the file contracts — it wires them.

## The build that decides

This is a **browser library**. It is compiled by TinyGo to WebAssembly in production. A change is
done only when all of these pass:

```bash
go install webtyp.com/devflow/cmd/gotest@latest   # once; needs gotest >= v0.4.108
gotest            # vet + tests + the wasm tests in tests/ run in a headless browser
gotest -tinygo    # also compiles with TinyGo — mandatory
```

`GOOS=js GOARCH=wasm go build` succeeding proves nothing: it uses the **full** standard library and
does not imply TinyGo. `net/http` compiles there and is still forbidden.

## Forbidden imports and their replacement

| Do not import | Use instead |
|---|---|
| `fmt`, `errors`, `strconv`, `strings` | `webtyp.com/fmt` |
| `encoding/json` (adds ~1 MB of wasm under TinyGo) | `webtyp.com/json` |
| `net/http` | `webtyp.com/fetch` |
| `context` | `webtyp.com/context` |
| `time` | `webtyp.com/time`, or `performance.now()` through `syscall/js` |
| `reflect` | nothing — plain structs |
| `map[K]V` (heavy in TinyGo) | a slice of structs scanned linearly, or `fmt.KeyValue` |

Waiting on a JavaScript promise: `webtyp.com/await` (`await.Promise(p)`), never a hand-written
channel + callback pair. Do not invent a port or helper that another `webtyp.com/*` package
already exposes.

## Layout

| File | Build | Role |
|---|---|---|
| `protocol.go` | all | `Request`, `Event` and their JSON encoding (`webtyp.com/json`) — what crosses page ↔ Worker |
| `setup.go` | all | `Setup`, `DeciderSpec`, `WriterSpec`, `Models` — what the application declares |
| `core.go` | all | the Worker's logic over injected dependencies; testable natively |
| `worker.go` | `wasm` | `Serve(Setup)`: glues `core` to `js.ServeWorker`, `js.PostToPage`, `device`, OPFS |
| `page.go` | `wasm` | `Start`, `Client`: the page side |
| `tests/` | — | black-box tests (`package tests`); `core_internal_test.go` at the root for `core` |

`core_internal_test.go` is the one white-box test file, next to the code it needs (unexported
`core`); everything else goes in `tests/`.
