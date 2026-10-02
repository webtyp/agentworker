//go:build wasm

package agentworker

import (
	"webtyp.com/device"
	"webtyp.com/fmt"
	"webtyp.com/js"
)

// Scripts are the Worker scripts of one assistant: the same Worker main built twice (D16).
type Scripts struct {
	Plain string // e.g. "/cote.worker.js"
	SIMD  string // e.g. "/cote.simd.worker.js"; "" = only the plain build exists
}

type Client struct {
	w *js.Worker
}

// Start wakes the assistant. Call it from the click that wakes it (a user gesture): it asks the
// browser to persist storage first (D-PWA-9), picks the SIMD script when the browser supports
// SIMD and one exists, starts the Worker and sends RequestStart. onEvent receives every event,
// progress included, on the page's goroutine. Call it from a goroutine: it blocks on promises.
func Start(s Scripts, onEvent func(Event)) (*Client, error) {
	if s.Plain == "" {
		return nil, fmt.Errf("agentworker: Scripts.Plain is required")
	}

	persisted, _ := device.Persist()
	p, _ := device.Detect()

	script := s.Plain
	if p.SIMD && s.SIMD != "" {
		script = s.SIMD
	}

	w, err := js.NewWorker(script, func(msg *js.Message, err error) {
		if err != nil {
			onEvent(Event{Kind: EventFailed, Text: err.Error()})
			return
		}

		e, err := DecodeEvent(msg.Data)
		if err != nil {
			onEvent(Event{Kind: EventFailed, Text: err.Error()})
			return
		}

		onEvent(e)
	})
	if err != nil {
		return nil, err
	}

	client := &Client{w: w}

	startReq := Request{Kind: RequestStart, Persisted: persisted}
	startData, _ := startReq.Encode()
	w.Post(&js.Message{Data: startData})

	return client, nil
}

func (c *Client) Run(sessionID, text string) {
	req := Request{Kind: RequestRun, SessionID: sessionID, Text: text}
	data, _ := req.Encode()
	c.w.Post(&js.Message{Data: data})
}

func (c *Client) Confirm(sessionID string) {
	req := Request{Kind: RequestConfirm, SessionID: sessionID}
	data, _ := req.Encode()
	c.w.Post(&js.Message{Data: data})
}

func (c *Client) Decline(sessionID string) {
	req := Request{Kind: RequestDecline, SessionID: sessionID}
	data, _ := req.Encode()
	c.w.Post(&js.Message{Data: data})
}

func (c *Client) Stop() {
	c.w.Terminate()
}
