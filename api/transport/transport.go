package transport

import (
	"context"

	"github.com/go-kratos/kratos/v2/transport"

	transportv1 "github.com/omalloc/balefire/api/transport/v1"
)

// Handler processes incoming messages.
type Handler func(ctx context.Context, message *transportv1.Message) error

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
	Send(ctx context.Context, dst string, message *transportv1.Message) error
	// OnReceive registers a handler for incoming messages.
	OnReceive(handler Handler)
}
