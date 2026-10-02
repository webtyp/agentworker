package agentworker

import (
	"webtyp.com/fmt"
	"webtyp.com/json"
	"webtyp.com/llm"
	"webtyp.com/model"
)

// RequestKind is what the page asks the Worker.
type RequestKind uint8

const (
	RequestStart   RequestKind = iota + 1 // check the device, download, load; then Ready or Asleep
	RequestRun                            // a message from the person
	RequestConfirm                        // confirm the pending calls
	RequestDecline                        // decline them
)

// Request is one message from the page to the Worker.
type Request struct {
	Kind      RequestKind
	SessionID string
	Text      string // RequestRun only
	Persisted bool   // RequestStart only: what the page's device.Persist() answered (D-PWA-9)
}

// EventKind is what the Worker tells the page.
type EventKind uint8

const (
	EventProgress EventKind = iota + 1 // Artifact, Done, Total
	EventReady                         // WriterOff, Persisted
	EventAsleep                        // Artifact, Shortfalls: the device cannot run the assistant
	EventReply                         // SessionID, Text, Pending
	EventFailed                        // Text: what went wrong
)

// Pending is a tool call waiting for the person (from llm.ToolCall).
type Pending struct{ ID, Name, Input string }

// Event is one message from the Worker to the page.
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

const errBadMessage = "agentworker: bad message: %v"

// Encode writes r as JSON.
func (r Request) Encode() ([]byte, error) {
	var out []byte
	err := json.Encode(requestCodec{&r}, &out)
	return out, err
}

// DecodeRequest reads a Request written by Encode.
func DecodeRequest(data []byte) (Request, error) {
	var r Request
	if err := json.Decode(data, requestCodec{&r}); err != nil {
		return Request{}, fmt.Errf(errBadMessage, err)
	}
	if r.Kind < RequestStart || r.Kind > RequestDecline {
		return Request{}, fmt.Errf(errBadMessage, fmt.Sprintf("request kind %d", r.Kind))
	}
	return r, nil
}

// Encode writes e as JSON.
func (e Event) Encode() ([]byte, error) {
	var out []byte
	err := json.Encode(eventCodec{&e}, &out)
	return out, err
}

// DecodeEvent reads an Event written by Encode.
func DecodeEvent(data []byte) (Event, error) {
	var e Event
	if err := json.Decode(data, eventCodec{&e}); err != nil {
		return Event{}, fmt.Errf(errBadMessage, err)
	}
	if e.Kind < EventProgress || e.Kind > EventFailed {
		return Event{}, fmt.Errf(errBadMessage, fmt.Sprintf("event kind %d", e.Kind))
	}
	return e, nil
}

func replyEvent(sessionID string, text string, calls []llm.ToolCall) *Event {
	e := &Event{Kind: EventReply, SessionID: sessionID, Text: text}
	for _, c := range calls {
		e.Pending = append(e.Pending, Pending{ID: c.ID, Name: c.Name, Input: c.Input})
	}
	return e
}

type requestCodec struct{ r *Request }

func (c requestCodec) IsNil() bool { return c.r == nil }
func (c requestCodec) EncodeFields(w model.FieldWriter) {
	w.Int("kind", int64(c.r.Kind))
	if c.r.SessionID != "" {
		w.String("session_id", c.r.SessionID)
	}
	if c.r.Text != "" {
		w.String("text", c.r.Text)
	}
	if c.r.Persisted {
		w.Bool("persisted", true)
	}
}
func (c requestCodec) DecodeFields(fr model.FieldReader) {
	if v, ok := fr.Int("kind"); ok {
		c.r.Kind = RequestKind(v)
	}
	c.r.SessionID, _ = fr.String("session_id")
	c.r.Text, _ = fr.String("text")
	c.r.Persisted, _ = fr.Bool("persisted")
}

type eventCodec struct{ e *Event }

func (c eventCodec) IsNil() bool { return c.e == nil }
func (c eventCodec) EncodeFields(w model.FieldWriter) {
	e := c.e
	w.Int("kind", int64(e.Kind))
	if e.Artifact != "" {
		w.String("artifact", e.Artifact)
	}
	if e.Done != 0 {
		w.Int("done", e.Done)
	}
	if e.Total != 0 {
		w.Int("total", e.Total)
	}
	if e.WriterOff {
		w.Bool("writer_off", true)
	}
	if e.Persisted {
		w.Bool("persisted", true)
	}
	if len(e.Shortfalls) > 0 {
		arr := w.Array("shortfalls", len(e.Shortfalls))
		for _, s := range e.Shortfalls {
			arr.String(s)
		}
		arr.Close()
	}
	if e.SessionID != "" {
		w.String("session_id", e.SessionID)
	}
	if e.Text != "" {
		w.String("text", e.Text)
	}
	if len(e.Pending) > 0 {
		arr := w.Array("pending", len(e.Pending))
		for i := range e.Pending {
			arr.Object(pendingCodec{&e.Pending[i]})
		}
		arr.Close()
	}
}
func (c eventCodec) DecodeFields(fr model.FieldReader) {
	e := c.e
	if v, ok := fr.Int("kind"); ok {
		e.Kind = EventKind(v)
	}
	e.Artifact, _ = fr.String("artifact")
	e.Done, _ = fr.Int("done")
	e.Total, _ = fr.Int("total")
	e.WriterOff, _ = fr.Bool("writer_off")
	e.Persisted, _ = fr.Bool("persisted")
	if arr, ok := fr.Array("shortfalls"); ok {
		e.Shortfalls = make([]string, arr.Len())
		for i := range e.Shortfalls {
			e.Shortfalls[i] = arr.String(i)
		}
	}
	e.SessionID, _ = fr.String("session_id")
	e.Text, _ = fr.String("text")
	if arr, ok := fr.Array("pending"); ok {
		e.Pending = make([]Pending, arr.Len())
		for i := range e.Pending {
			arr.Object(i, pendingCodec{&e.Pending[i]})
		}
	}
}

type pendingCodec struct{ p *Pending }

func (c pendingCodec) IsNil() bool { return c.p == nil }
func (c pendingCodec) EncodeFields(w model.FieldWriter) {
	w.String("id", c.p.ID)
	w.String("name", c.p.Name)
	w.String("input", c.p.Input)
}
func (c pendingCodec) DecodeFields(fr model.FieldReader) {
	c.p.ID, _ = fr.String("id")
	c.p.Name, _ = fr.String("name")
	c.p.Input, _ = fr.String("input")
}
