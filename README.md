# agentworker
<img src="docs/img/badges.svg">

The webtyp agent inside a Web Worker: models from OPFS, device check, downloads with progress, and a page-side client.

## What it is

This library provides a Web Worker runtime for a `webtyp.com/agent`. It handles checking the device profile (SIMD, WebGPU, memory), downloading the required model files (decider and optionally a writer) via `webtyp.com/artifacts`, and persisting the decision cache.

## Usage

### The Worker Main

The application defines a single Web Worker entry point, usually about 15 lines of code:

```go
//go:build wasm

package main

import (
	"webtyp.com/agentworker"
	"webtyp.com/agent"
)

func main() {
	agentworker.Serve(agentworker.Setup{
		Dir: "cote",
		Decider: agentworker.DeciderSpec{...},
		Writer:  &agentworker.WriterSpec{...},
		AgentConfig: func(m agentworker.Models) (agent.Config, error) {
			// application agent config here
			return agent.Config{}, nil
		},
	})
}
```

### The Page

On the page side, wake up the assistant via a user gesture. This triggers persistent storage requests because Workers cannot call `navigator.storage.persist()`.

```go
//go:build wasm

package main

import (
	"webtyp.com/agentworker"
)

func wakeUp() {
	c, err := agentworker.Start(agentworker.Scripts{
		Plain: "/cote.worker.js",
		SIMD:  "/cote.simd.worker.js",
	}, func(e agentworker.Event) {
		switch e.Kind {
		case agentworker.EventProgress:
			// Show progress bar
		case agentworker.EventReady:
			// Agent is ready to use
		case agentworker.EventAsleep:
			// Device isn't capable of running the assistant
		case agentworker.EventReply:
			// Display the agent's reply
		}
	})

	if err == nil {
		c.Run("session-1", "Hello assistant!")
	}
}
```

## min_rate

The manifest's `min_rate` represents the number of runs per second of the benchmark kernel. The kernel performs a `nn.MatVecQ8Block32` on a fixed 3584x1024 int8 matrix.

## Persistence

`device.Persist()` is intentionally run on the page since Web Workers do not have access to it.
