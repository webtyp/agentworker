package agentworker

import (
	"webtyp.com/fmt"
	"webtyp.com/json"
	"webtyp.com/model"
)

type RequestKind uint8

const (
	RequestStart   RequestKind = iota + 1 // check the device, download, load; then Ready or Asleep
	RequestRun                            // a message from the person
	RequestConfirm                        // confirm the pending calls
	RequestDecline                        // decline them
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

type Pending struct {
	ID    string
	Name  string
	Input string
} // from llm.ToolCall

type Event struct {
	Kind       EventKind
	Artifact   string   // EventProgress, EventAsleep: artifact id
	Done       int64    // EventProgress
	Total      int64    // EventProgress
	WriterOff  bool     // EventReady: the writer was dropped (answers come from templates/data)
	Persisted  bool     // EventReady: storage is persistent; false -> the UI warns (D-PWA-9)
	Shortfalls []string // EventAsleep: device.Shortfall.String() of every blocking shortfall
	SessionID  string   // EventReply
	Text       string   // EventReply, EventFailed
	Pending    []Pending
}

func (r Request) Encode() ([]byte, error) {
	b := json.GetEncoderBuffer()
	err := json.Encode(r, b)
	if err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (r Request) EncodeFields(e model.FieldWriter) {
	e.Int("kind", int64(r.Kind))
	if r.SessionID != "" {
		e.String("session_id", r.SessionID)
	}
	if r.Text != "" {
		e.String("text", r.Text)
	}
	if r.Persisted {
		e.Bool("persisted", r.Persisted)
	}
}

func (r Request) IsNil() bool { return false }

func DecodeRequest(data []byte) (Request, error) {
	var r Request
	err := json.Decode(data, &r)
	if err != nil {
		return r, fmt.Errf("agentworker: bad message: %v", err)
	}
	return r, nil
}

func (r *Request) DecodeFields(d model.FieldReader) {
	if v, ok := d.Int("kind"); ok {
		r.Kind = RequestKind(v)
	}
	if v, ok := d.String("session_id"); ok {
		r.SessionID = v
	}
	if v, ok := d.String("text"); ok {
		r.Text = v
	}
	if v, ok := d.Bool("persisted"); ok {
		r.Persisted = v
	}
}

func (e Event) Encode() ([]byte, error) {
	b := json.GetEncoderBuffer()
	err := json.Encode(e, b)
	if err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (e Event) EncodeFields(enc model.FieldWriter) {
	enc.Int("kind", int64(e.Kind))
	if e.Artifact != "" {
		enc.String("artifact", e.Artifact)
	}
	if e.Done != 0 {
		enc.Int("done", e.Done)
	}
	if e.Total != 0 {
		enc.Int("total", e.Total)
	}
	if e.WriterOff {
		enc.Bool("writer_off", e.WriterOff)
	}
	if e.Persisted {
		enc.Bool("persisted", e.Persisted)
	}
	// Note: We skip serializing string slice and objects using FieldWriter for now.
	// We'd rely on encoding/json typically or implement it explicitly.
}

func (e Event) IsNil() bool { return false }

func DecodeEvent(data []byte) (Event, error) {
	var e Event
	err := json.Decode(data, &e)
	if err != nil {
		return e, fmt.Errf("agentworker: bad message: %v", err)
	}
	return e, nil
}

func (e *Event) DecodeFields(d model.FieldReader) {
	if v, ok := d.Int("kind"); ok {
		e.Kind = EventKind(v)
	}
	if v, ok := d.String("artifact"); ok {
		e.Artifact = v
	}
	if v, ok := d.Int("done"); ok {
		e.Done = v
	}
	if v, ok := d.Int("total"); ok {
		e.Total = v
	}
	if v, ok := d.Bool("writer_off"); ok {
		e.WriterOff = v
	}
	if v, ok := d.Bool("persisted"); ok {
		e.Persisted = v
	}
	if v, ok := d.String("session_id"); ok {
		e.SessionID = v
	}
	if v, ok := d.String("text"); ok {
		e.Text = v
	}
}
