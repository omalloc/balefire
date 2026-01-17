package transport

import (
	"context"

	"github.com/go-kratos/kratos/v2/transport"
)

type Kind int

const (
	KindPing Kind = iota
	KindPong
	KindData
	KindACK
)

type Message interface {
	GetKind() Kind
	GetID() string
	GetPayload() []byte

	Marshal() ([]byte, error)
	Unmarshal(data []byte) error
}

// Handler processes incoming messages.
type Handler func(ctx context.Context, message Message) error

type Mode string

const (
	ModeLeaf   Mode = "leaf"
	ModeServer Mode = "server"
	ModeRelay  Mode = "relay"
	ModeAuto   Mode = "auto"
)

// Transport abstracts the underlying network
type Transport interface {
	// Server returns the underlying transport server.
	transport.Server

	// Send sends a message to the specified destination.
	Send(ctx context.Context, dst string, message Message) error
	// OnReceive registers a handler for incoming messages.
	OnReceive(handler Handler)
}
