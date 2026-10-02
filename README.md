# agentworker
<img src="docs/img/badges.svg">

The webtyp agent inside a Web Worker: models from OPFS, device check, downloads with progress, a
decision cache kept between starts, and a page-side client with typed events.

It has two halves. The **Worker** binary (written by the application, because its tools, templates
and memory live there) calls `Serve`. The **page** calls `Start` from the click that wakes the
assistant and receives events. Design: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## The Worker main

```go
//go:build wasm

package main

import (
	"webtyp.com/agent"
	"webtyp.com/agentworker"
	"webtyp.com/lfm"
	"webtyp.com/qwen"
)

func main() {
	agentworker.Serve(agentworker.Setup{
		Dir:     "cote", // this module's OPFS directory
		Decider: agentworker.DeciderSpec{Weights: "decider-0.8b", Merges: "decider-0.8b-merges", Decoder: qwen.Qwen35_08B, Temperature: 1.03},
		Writer:  &agentworker.WriterSpec{Weights: "writer-lfm-350m", Merges: "writer-lfm-350m-merges", Decoder: lfm.LFM25_350M},
		AgentConfig: func(m agentworker.Models) (agent.Config, error) {
			// Decider, Writer and Tokens are set by agentworker from m.
			return agent.Config{Texts: texts, Memory: agent.NewMemMemory(), IDGen: ids, ToolIndex: agent.NewMemToolIndex(), LocalTools: tools}, nil
		},
	})
}
```

## The page

```go
// From the wake-up click's goroutine: Start blocks on browser promises.
c, err := agentworker.Start(agentworker.Scripts{Plain: "/cote.worker.js", SIMD: "/cote.simd.worker.js"},
	func(e agentworker.Event) {
		switch e.Kind {
		case agentworker.EventProgress: // e.Artifact, e.Done of e.Total bytes
		case agentworker.EventReady: // e.WriterOff: answers come from templates/data; !e.Persisted: warn the disk may evict the models
		case agentworker.EventAsleep: // e.Shortfalls: "secure", "space", "tier", "speed"
		case agentworker.EventReply: // e.Text; e.Pending → show them, then c.Confirm or c.Decline
		case agentworker.EventFailed: // e.Text
		}
	})
if err != nil {
	return err
}
c.Run("session-1", "¿A qué hora abren?")
```

`Start` asks `device.Persist()` itself: Workers do not have `navigator.storage.persist`, and the
browser wants a user gesture for it.

## The artifacts it expects

The application's `/artifacts.json` (`webtyp.com/artifacts`, written by `sitec` from the project's
`Artifacts()`) lists the decider's weights and merges and, optionally, the writer's. Each artifact's
`needs` decides: when the decider's needs are not met the assistant sleeps; when the writer's are
not met, or both models do not fit, only the writer is dropped.

**`min_rate` is in runs per second of the benchmark kernel**: one `nn.MatVecQ8Block32` over a
3584×1024 int8 matrix, measured for `BenchBudgetMs` (200 ms). Under TinyGo 0.41 on the machine of
`nn/docs/PERFORMANCE.md` it runs ≈ 770 times per second plain and ≈ 1640 with SIMD.

## Testing an application

`Start` needs a built Worker script, so the end-to-end test (page → Worker → reply) belongs to the
application. This library tests the Worker logic natively with fakes (`core_internal_test.go`).
