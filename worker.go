//go:build wasm

package agentworker

import (
	"webtyp.com/artifacts"
	"webtyp.com/context"
	"webtyp.com/device"
	"webtyp.com/js"
	"webtyp.com/lfm"
	"webtyp.com/llm"
	"webtyp.com/opfs"
	"webtyp.com/qwen"
	"webtyp.com/weights"
)

// Serve runs the agent in this Web Worker. Call it from the Worker binary's main; it never
// returns. Requests are answered one at a time; RequestStart reports progress with
// js.PostToPage before its reply.
func Serve(s Setup) {
	fs, err := opfs.Open(s.Dir)
	if err != nil {
		js.ServeWorker(failing{err})
		return
	}
	c := &core{
		setup:  s,
		store:  artifacts.New(fs),
		files:  fs,
		detect: device.Detect,
		bench:  func() device.Rate { return device.Bench(benchKernel(), BenchBudgetMs) },
		post: func(e Event) {
			if data, err := e.Encode(); err == nil {
				js.PostToPage(&js.Message{Data: data})
			}
		},
		manifestURL: artifacts.ManifestPath,
		newDecider: func(w *weights.Artifact, merges []byte, d DeciderSpec) (decider, error) {
			return qwen.New(qwen.Config{Weights: w, Merges: merges, Decoder: d.Decoder, DecideTemperature: d.Temperature})
		},
		newWriter: func(w *weights.Artifact, merges []byte, ws WriterSpec) (llm.Client, error) {
			return lfm.New(lfm.Config{Weights: w, Merges: merges, Decoder: ws.Decoder})
		},
	}
	js.ServeWorker(handler{c})
}

type handler struct{ c *core }

func (h handler) OnMessage(ctx *context.Context, msg *js.Message) (*js.Message, error) {
	r, err := DecodeRequest(msg.Data)
	if err != nil {
		return nil, err
	}
	e, err := h.c.handle(ctx, r)
	if err != nil || e == nil {
		return nil, err
	}
	data, err := e.Encode()
	if err != nil {
		return nil, err
	}
	return &js.Message{Data: data}, nil
}

// failing answers every request with the error that kept the Worker from starting.
type failing struct{ err error }

func (f failing) OnMessage(*context.Context, *js.Message) (*js.Message, error) {
	return nil, f.err
}
