//go:build wasm

package agentworker

import (
	"webtyp.com/device"
	"webtyp.com/fmt"
	"webtyp.com/js"
)

const errPlainRequired = "agentworker: Scripts.Plain is required"

// Scripts are the Worker scripts of one assistant: the same Worker main built twice (D16).
type Scripts struct {
	Plain string // e.g. "/cote.worker.js"
	SIMD  string // e.g. "/cote.simd.worker.js"; "" = only the plain build exists
}

// Client is the page's handle on a started assistant. Its answers arrive as events.
type Client struct{ w *js.Worker }

// Start wakes the assistant. Call it from the click that wakes it (a user gesture): it asks the
// browser to persist storage first (D-PWA-9), picks the SIMD script when the browser supports
// SIMD and one exists, starts the Worker and sends RequestStart. onEvent receives every event,
// progress included, on the page's goroutine. Call it from a goroutine: it blocks on promises.
func Start(s Scripts, onEvent func(Event)) (*Client, error) {
	if s.Plain == "" {
		return nil, fmt.Err(errPlainRequired)
	}
	persisted, _ := device.Persist()
	p, _ := device.Detect()
	script := s.Plain
	if p.SIMD && s.SIMD != "" {
		script = s.SIMD
	}
	c := &Client{w: js.NewWorker(script, func(msg *js.Message, err error) {
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
	})}
	c.post(Request{Kind: RequestStart, Persisted: persisted})
	return c, nil
}

// Run sends a message from the person; the answer arrives as EventReply.
func (c *Client) Run(sessionID, text string) {
	c.post(Request{Kind: RequestRun, SessionID: sessionID, Text: text})
}

// Confirm runs the calls waiting in EventReply.Pending.
func (c *Client) Confirm(sessionID string) {
	c.post(Request{Kind: RequestConfirm, SessionID: sessionID})
}

// Decline cancels them.
func (c *Client) Decline(sessionID string) {
	c.post(Request{Kind: RequestDecline, SessionID: sessionID})
}

// Stop terminates the Worker.
func (c *Client) Stop() { c.w.Terminate() }

func (c *Client) post(r Request) {
	data, _ := r.Encode() // a Request always encodes
	c.w.Post(&js.Message{Data: data})
}
