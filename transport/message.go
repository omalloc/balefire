package transport

import (
	"github.com/fxamacker/cbor/v2"

	"github.com/omalloc/balefire/api/transport"
)

var _ transport.Message = (*TaskMessage)(nil)

type TaskMessage struct {
	ID      string
	Kind    transport.Kind
	Payload []byte
}

func NewMessage(id string, kind transport.Kind, payload []byte) transport.Message {
	return &TaskMessage{
		ID:      id,
		Kind:    transport.KindData,
		Payload: payload,
	}
}

func NewPingMessage(id string) transport.Message {
	return &TaskMessage{
		ID:      id,
		Kind:    transport.KindPing,
		Payload: nil,
	}
}

func NewPongMessage(id string) transport.Message {
	return &TaskMessage{
		ID:      id,
		Kind:    transport.KindPong,
		Payload: nil,
	}
}

func NewACK(id string) transport.Message {
	return &TaskMessage{
		ID:      id,
		Kind:    transport.KindACK,
		Payload: nil,
	}
}

func NewEmptyMessage() transport.Message {
	return &TaskMessage{}
}

// GetID implements transport.Message.
func (t *TaskMessage) GetID() string {
	return t.ID
}

// GetKind implements transport.Message.
func (t *TaskMessage) GetKind() transport.Kind {
	return t.Kind
}

// GetPayload implements transport.Message.
func (t *TaskMessage) GetPayload() []byte {
	return t.Payload
}

// Marshal implements transport.Message.
func (t *TaskMessage) Marshal() ([]byte, error) {
	return cbor.Marshal(t)
}

// Unmarshal implements transport.Message.
func (t *TaskMessage) Unmarshal(data []byte) error {
	return cbor.Unmarshal(data, t)
}
