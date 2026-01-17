package transport

import (
	"testing"
)

func TestMessageMarshalUnmarshal(t *testing.T) {
	original := NewPingMessage("test-id1")

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("failed to marshal message: %v", err)
	}
	t.Logf("data=%x", data)

	newMsg := NewEmptyMessage()
	if err := newMsg.Unmarshal(data); err != nil {
		t.Fatalf("failed to unmarshal message: %v", err)
	}

	if newMsg.GetID() != original.GetID() {
		t.Errorf("expected ID %s, got %s", original.GetID(), newMsg.GetID())
	}
	if newMsg.GetKind() != original.GetKind() {
		t.Errorf("expected Kind %v, got %v", original.GetKind(), newMsg.GetKind())
	}
	if string(newMsg.GetPayload()) != string(original.GetPayload()) {
		t.Errorf("expected Payload %s, got %s", string(original.GetPayload()), string(newMsg.GetPayload()))
	}
}
