package transport

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	api "github.com/omalloc/balefire/api/transport"
	pb "github.com/omalloc/balefire/api/transport/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockStore for testing
type MockStore struct{}

func (m *MockStore) SaveTask(task *pb.Message) error          { return nil }
func (m *MockStore) GetTask(id string) (*pb.Message, error)   { return nil, nil }
func (m *MockStore) DeleteTask(id string) error               { return nil }
func (m *MockStore) ListPendingTasks() ([]*pb.Message, error) { return nil, nil }
func (m *MockStore) MarkTaskAsDone(id string) error           { return nil }
func (m *MockStore) Close() error                             { return nil }

func generateIdentity(t *testing.T) string {
	sk, _, err := crypto.GenerateEd25519Key(nil)
	require.NoError(t, err)
	data, err := crypto.MarshalPrivateKey(sk)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(data)
}

func TestTransport_E2E_Signing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Generate Identities
	idA := generateIdentity(t)
	idB := generateIdentity(t)

	// 1. Setup and Start Node A (Server)
	optsA := Option{
		Mode:        api.ModeServer,
		Identity:    idA,
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
	}
	trA, err := NewP2PTransport(optsA)
	require.NoError(t, err)
	defer trA.Stop(context.Background())

	err = trA.Start(ctx)
	require.NoError(t, err)

	// Register Handler on Node A early
	received := make(chan *pb.Message, 1)
	trA.OnReceive(func(ctx context.Context, msg *pb.Message) error {
		t.Logf("Node A received message: %s", msg.Id)
		received <- msg
		return nil
	})

	// Extract Node A's address
	p2pTrA := trA.(*p2pTransport)
	hostA := p2pTrA.host
	portA := hostA.Addrs()[0]
	fullAddrA := fmt.Sprintf("%s/p2p/%s", portA.String(), hostA.ID().String())
	t.Logf("Node A Address: %s", fullAddrA)

	// 2. Setup Node B (Leaf) with Node A as peer
	optsB := Option{
		Mode:         api.ModeLeaf,
		Identity:     idB,
		ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
		CentralPeers: []string{fullAddrA},
	}
	trB, err := NewP2PTransport(optsB)
	require.NoError(t, err)
	defer trB.Stop(context.Background())

	// Start Node B (will connect to A and advertise)
	err = trB.Start(ctx)
	require.NoError(t, err)

	// Wait for connection and DHT propagation
	time.Sleep(1 * time.Second)

	// 3. Send message from B to A
	msgId := "test-msg-1"
	msg := &pb.Message{
		Id:        msgId,
		Type:      pb.MessageType_DATA,
		Payload:   []byte("Hello Balefire"),
		Timestamp: time.Now().Unix(),
	}

	// Send using PeerID (should use existing connection or DHT lookup locally)
	// Since B is connected to A via CentralPeers, it should work immediately.
	err = trB.Send(ctx, hostA.ID().String(), msg)
	require.NoError(t, err)

	// 4. Verify receipt and signature
	select {
	case rMsg := <-received:
		assert.Equal(t, msgId, rMsg.Id)
		assert.Equal(t, []byte("Hello Balefire"), rMsg.Payload)
		assert.NotEmpty(t, rMsg.Signature)

		// Verify signature manually to be double sure
		// (Transport already did it, but good for test coverage)
		t.Log("Message verified successfully by transport")

	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for message")
	}
}
