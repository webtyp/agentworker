//go:build wasm

package tests

import (
	"testing"
	"webtyp.com/agentworker"
)

func TestStart_PlainScriptRequired(t *testing.T) {
	_, err := agentworker.Start(agentworker.Scripts{}, func(e agentworker.Event) {})
	if err == nil {
		t.Fatal("expected error when Scripts.Plain is empty")
	}
	if err.Error() != "agentworker: Scripts.Plain is required" {
		t.Errorf("expected 'Scripts.Plain is required', got %v", err)
	}
}
