package transport

import "context"

type Kind int

const (
	KindPing Kind = iota
	KindPong
	KindData
	KindACK
)

type Message interface {
	Kind() Kind
	ID() string
	Payload() []byte
}

// Handler processes incoming messages.
type Handler func(ctx context.Context, msg Message) error

// Transport abstracts the underlying network
type Transport interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Send(ctx context.Context, dst string, msg Message) error
	OnReceive(h Handler)
}
