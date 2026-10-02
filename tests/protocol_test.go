package tests

import (
	"reflect"
	"testing"

	"webtyp.com/agentworker"
)

func TestRequest_EncodeDecode(t *testing.T) {
	req := agentworker.Request{
		Kind:      agentworker.RequestStart,
		SessionID: "session123",
		Text:      "hello world",
		Persisted: true,
	}

	data, err := req.Encode()
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := agentworker.DecodeRequest(data)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(req, decoded) {
		t.Errorf("expected %+v, got %+v", req, decoded)
	}
}

func TestEvent_EncodeDecode(t *testing.T) {
	evt := agentworker.Event{
		Kind:       agentworker.EventReady,
		Artifact:   "art-123",
		Done:       50,
		Total:      100,
		WriterOff:  true,
		Persisted:  true,
		Shortfalls: []string{"tier", "space"},
		SessionID:  "session123",
		Text:       "reply text",
		Pending: []agentworker.Pending{
			{ID: "p1", Name: "tool1", Input: "input1"},
		},
	}

	data, err := evt.Encode()
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := agentworker.DecodeEvent(data)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(evt, decoded) {
		t.Errorf("expected %+v, got %+v", evt, decoded)
	}
}

func TestDecodeRequest_Malformed(t *testing.T) {
	_, err := agentworker.DecodeRequest([]byte("malformed json"))
	if err == nil {
		t.Errorf("expected error for malformed json")
	}
}
