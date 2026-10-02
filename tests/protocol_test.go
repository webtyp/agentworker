package tests

import (
	"strings"
	"testing"

	"webtyp.com/agentworker"
)

func TestRequest_RoundTrip(t *testing.T) {
	for _, r := range []agentworker.Request{
		{Kind: agentworker.RequestStart, Persisted: true},
		{Kind: agentworker.RequestRun, SessionID: "s1", Text: "¿a qué hora abren?"},
		{Kind: agentworker.RequestConfirm, SessionID: "s1"},
		{Kind: agentworker.RequestDecline, SessionID: "s1"},
	} {
		data, err := r.Encode()
		if err != nil {
			t.Fatal(err)
		}
		got, err := agentworker.DecodeRequest(data)
		if err != nil {
			t.Fatalf("%s: %v", data, err)
		}
		if got != r {
			t.Errorf("got %+v, want %+v", got, r)
		}
	}
}

func TestEvent_RoundTrip(t *testing.T) {
	for _, e := range []agentworker.Event{
		{Kind: agentworker.EventProgress, Artifact: "decider", Done: 8 << 20, Total: 470 << 20},
		{Kind: agentworker.EventReady, WriterOff: true, Persisted: true},
		{Kind: agentworker.EventAsleep, Artifact: "decider", Shortfalls: []string{"tier", "space"}},
		{Kind: agentworker.EventReply, SessionID: "s1", Text: "Confirma", Pending: []agentworker.Pending{
			{ID: "c1", Name: "book", Input: `{"day":"lunes"}`},
			{ID: "c2", Name: "cancel", Input: `{}`},
		}},
		{Kind: agentworker.EventFailed, Text: "boom"},
	} {
		data, err := e.Encode()
		if err != nil {
			t.Fatal(err)
		}
		got, err := agentworker.DecodeEvent(data)
		if err != nil {
			t.Fatalf("%s: %v", data, err)
		}
		if !sameEvent(got, e) {
			t.Errorf("got %+v, want %+v", got, e)
		}
	}
}

func sameEvent(a, b agentworker.Event) bool {
	if a.Kind != b.Kind || a.Artifact != b.Artifact || a.Done != b.Done || a.Total != b.Total ||
		a.WriterOff != b.WriterOff || a.Persisted != b.Persisted || a.SessionID != b.SessionID ||
		a.Text != b.Text || strings.Join(a.Shortfalls, ",") != strings.Join(b.Shortfalls, ",") ||
		len(a.Pending) != len(b.Pending) {
		return false
	}
	for i := range a.Pending {
		if a.Pending[i] != b.Pending[i] {
			return false
		}
	}
	return true
}

func TestDecode_Malformed(t *testing.T) {
	for _, data := range []string{"not json", `{"kind":0}`, `{"kind":9}`} {
		if _, err := agentworker.DecodeRequest([]byte(data)); err == nil || !strings.HasPrefix(err.Error(), "agentworker: bad message: ") {
			t.Errorf("DecodeRequest(%s) = %v, want a bad message error", data, err)
		}
		if _, err := agentworker.DecodeEvent([]byte(data)); err == nil || !strings.HasPrefix(err.Error(), "agentworker: bad message: ") {
			t.Errorf("DecodeEvent(%s) = %v, want a bad message error", data, err)
		}
	}
}
