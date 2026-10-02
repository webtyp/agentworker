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

type workerHandler struct {
	c *core
}

func (h *workerHandler) OnMessage(ctx *context.Context, msg *js.Message) (*js.Message, error) {
	req, err := DecodeRequest(msg.Data)
	if err != nil {
		return nil, err
	}

	evt, err := h.c.handle(req)
	if err != nil {
		return nil, err
	}

	if evt == nil {
		return nil, nil
	}

	data, err := evt.Encode()
	if err != nil {
		return nil, err
	}

	return &js.Message{Data: data}, nil
}

// Serve runs the agent in this Web Worker. Call it from the Worker binary's main; it never
// returns. Requests are answered one at a time; RequestStart reports progress with
// js.PostToPage before its reply.
func Serve(s Setup) {
	fs, err := opfs.Open(s.Dir)
	var c core
	c.setup = s
	c.detect = device.Detect
	c.bench = func() device.Rate { return device.Bench(benchKernel(), BenchBudgetMs) }
	c.post = func(e Event) {
		data, _ := e.Encode()
		js.PostToPage(&js.Message{Data: data})
	}
	c.manifestURL = artifacts.ManifestPath
	c.newDecider = func(w *weights.Artifact, merges []byte, spec DeciderSpec) (decider, error) {
		cfg := qwen.Config{
			Weights:           w,
			Merges:            merges,
			Decoder:           spec.Decoder,
			DecideTemperature: spec.Temperature,
		}
		return qwen.New(cfg)
	}
	c.newWriter = func(w *weights.Artifact, merges []byte, spec WriterSpec) (llm.Client, error) {
		cfg := lfm.Config{
			Weights: w,
			Merges:  merges,
			Decoder: spec.Decoder,
		}
		return lfm.New(cfg)
	}

	if err != nil {
		// Initialization failed (e.g. OPFS error); all requests should fail.
		// We'll set a dummy store/files and override handle to always return error.
		js.ServeWorker(&badWorkerHandler{err: err})
		return
	}

	c.files = fs
	c.store = artifacts.New(fs)

	js.ServeWorker(&workerHandler{c: &c})
}

type badWorkerHandler struct {
	err error
}

func (h *badWorkerHandler) OnMessage(ctx *context.Context, msg *js.Message) (*js.Message, error) {
	e := Event{Kind: EventFailed, Text: h.err.Error()}
	data, err := e.Encode()
	if err != nil {
		return nil, err
	}
	return &js.Message{Data: data}, nil
}
