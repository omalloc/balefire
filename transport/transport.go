package transport

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/multiformats/go-multiaddr"
	"google.golang.org/protobuf/proto"

	api "github.com/omalloc/balefire/api/transport"
	pb "github.com/omalloc/balefire/api/transport/v1"
)

// Note: A libp2p-backed Transport implementation will live in this package,
// hidden behind the Transport interface to avoid leaking dependencies.

var _ api.Transport = (*p2pTransport)(nil)

const (
	defaultProtocol   = "/balefire/1.0.0"
	defaultTag        = "balefire-mdns"
	defaultListenAddr = "/ip4/0.0.0.0/tcp/0"
)

type Option struct {
	Mode         api.Mode
	Identity     string
	CentralPeers []string
	ListenAddrs  []string
}

type p2pTransport struct {
	opt Option

	// p2p host, dht, etc.
	protocol protocol.ID
	host     host.Host
	kdht     *dht.IpfsDHT
	routing  *routing.RoutingDiscovery
	stop     chan struct{}

	privKey crypto.PrivKey
	handler api.Handler
}

func NewP2PTransport(opt Option) (api.Transport, error) {
	tr := &p2pTransport{
		opt:      opt,
		protocol: protocol.ID(defaultProtocol),
		stop:     make(chan struct{}, 1),
	}

	if len(opt.ListenAddrs) == 0 {
		opt.ListenAddrs = []string{defaultListenAddr}
	}

	opts := make([]libp2p.Option, 0, 16)

	if opt.Mode == api.ModeServer {
		opts = append(opts, tr.identity()) // identity() loads key into tr.privKey
	} else if len(opt.Identity) > 0 {
		// Even if not server, if we have identity (e.g. leaf with key), load it for signing
		tr.identity()
		opts = append(opts, libp2p.Identity(tr.privKey))
	}

	opts = append(opts,
		libp2p.ListenAddrStrings(opt.ListenAddrs...),
		libp2p.EnableRelay(),
		libp2p.EnableNATService(),
	)

	host, err := libp2p.New(opts...)
	if err != nil {
		log.Fatalf("failed to create libp2p host: %v", err)
		return nil, err
	}
	tr.host = host

	kadDHT, err := dht.New(context.Background(), host,
		dht.Mode(tr.dhtMode()),                        // 设置DHT模式
		dht.BucketSize(20),                            // 调整桶大小
		dht.RoutingTableRefreshPeriod(10*time.Minute), // 调整路由表刷新周期
	)
	if err != nil {
		log.Fatalf("failed to create DHT: %v", err)
		return nil, err
	}
	tr.kdht = kadDHT

	return tr, nil
}

// Start implements [transport.Transport].
func (p *p2pTransport) Start(ctx context.Context) error {
	log.Infof("transport starting with %s mode", p.opt.Mode)

	hostID := p.host.ID()
	for _, addr := range p.host.Addrs() {
		log.Infof("transport listening on %s/p2p/%s", addr.String(), hostID)
	}

	// if in server mode, listen for incoming connections
	// and serve DHT server
	if err := p.kdht.Bootstrap(ctx); err != nil {
		return err
	}

	if p.opt.Mode == api.ModeServer {
		go p.maintainPingPong(ctx)
	}

	p.host.Network().Notify(&ConnNotifiee{})

	p.host.SetStreamHandler(p.protocol, p.handleStream)

	// connect to central peers if in leaf mode
	if p.opt.Mode == api.ModeLeaf {
		// connect to central peers
		if err := p.connCentralPeers(); err != nil {
			return err
		}

		// start a goroutine to periodically connect to others peer
		go p.maintainClosestPeers(ctx, time.Minute)
	}

	log.Infof("transport started with %s mode", p.opt.Mode)
	return nil
}

// Stop implements [transport.Transport].
func (p *p2pTransport) Stop(ctx context.Context) error {
	p.stop <- struct{}{}
	log.Infof("transport stopped")
	return nil
}

// OnReceive implements [transport.Transport].
func (p *p2pTransport) OnReceive(handler api.Handler) {
	p.handler = handler
}

// Send implements [transport.Transport].
// Send implements [transport.Transport].
func (p *p2pTransport) Send(ctx context.Context, dst string, message *pb.Message) error {
	var targetPeer peer.ID

	// 1. Try parse as Multiaddr first (legacy/direct support)
	if ma, err := multiaddr.NewMultiaddr(dst); err == nil {
		if info, err := peer.AddrInfoFromP2pAddr(ma); err == nil {
			targetPeer = info.ID
			// If we have a direct address, try to connect if not connected
			if len(p.host.Network().ConnsToPeer(targetPeer)) == 0 {
				if err := p.host.Connect(ctx, *info); err != nil {
					log.Warnf("failed to connect to %s directly: %v", dst, err)
					// Fallthrough to DHT lookup
				}
			}
		}
	}

	// 2. If not parsed as multiaddr or fallback, try parse as PeerID
	if targetPeer == "" {
		if pid, err := peer.Decode(dst); err == nil {
			targetPeer = pid
		} else {
			return fmt.Errorf("invalid destination (not multiaddr or peerID): %s", dst)
		}
	}

	// 3. Ensure connection exists
	if len(p.host.Network().ConnsToPeer(targetPeer)) == 0 {
		log.Infof("peer %s not connected, searching in DHT...", targetPeer)
		info, err := p.kdht.FindPeer(ctx, targetPeer)
		if err != nil {
			return fmt.Errorf("dht find peer %s: %w", targetPeer, err)
		}
		if err := p.host.Connect(ctx, info); err != nil {
			return fmt.Errorf("connect to peer %s: %w", targetPeer, err)
		}
	}

	s, err := p.host.NewStream(ctx, targetPeer, p.protocol)
	if err != nil {
		return fmt.Errorf("create stream: %w", err)
	}
	defer s.Close()

	// Sign the message
	if err := p.sign(message); err != nil {
		return fmt.Errorf("sign message: %w", err)
	}

	buf, err := proto.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	n, err := s.Write(buf)
	if err != nil {
		return fmt.Errorf("write to stream: %w", err)
	}

	if n != len(buf) {
		return fmt.Errorf("incomplete write to stream")
	}

	// Close stream for writing to signal EOF to receiver
	if err := s.CloseWrite(); err != nil {
		return fmt.Errorf("close stream write: %w", err)
	}

	// Read reply (blocks until remote closes). Use a goroutine and abort on context.
	ackCh := make(chan []byte, 1)
	errCh := make(chan error, 1)

	go func() {
		b, err := io.ReadAll(s)
		if err != nil {
			select {
			case errCh <- err:
			case <-ctx.Done():
			}
			return
		}
		select {
		case ackCh <- b:
		case <-ctx.Done():
		}
	}()

	select {
	case <-ctx.Done():
		// Abort the stream to unblock reader and return.
		_ = s.Reset()
		return ctx.Err()
	case err := <-errCh:
		return err
	case b := <-ackCh:
		var reply pb.Message
		if err := proto.Unmarshal(b, &reply); err != nil {
			return fmt.Errorf("unmarshal ack: %w", err)
		}
		expectedType := pb.MessageType_ACK
		if message.Type == pb.MessageType_PING {
			expectedType = pb.MessageType_PONG
		}

		if reply.Type != expectedType || reply.Id != message.Id {
			return fmt.Errorf("invalid response type %s (expected %s) or ID %s != %s", reply.Type, expectedType, reply.Id, message.Id)
		}

		// Verify ACK signature
		if err := p.verify(&reply, s.Conn().RemotePublicKey()); err != nil {
			return fmt.Errorf("invalid ACK signature: %w", err)
		}

		// Close current connect stream
		if err := s.Close(); err != nil {
			return fmt.Errorf("close stream read: %w", err)
		}
	}

	return nil
}

// dhtMode returns the DHT mode based on the config transport mode.
func (p *p2pTransport) dhtMode() dht.ModeOpt {
	dhtMode := dht.ModeClient
	if p.opt.Mode == api.ModeServer {
		dhtMode = dht.ModeServer
	}
	return dhtMode
}

func (p *p2pTransport) connCentralPeers() error {

	for _, addr := range p.opt.CentralPeers {
		maddr, _ := multiaddr.NewMultiaddr(addr)
		pi, _ := peer.AddrInfoFromP2pAddr(maddr)
		if pi == nil {
			continue
		}

		log.Infof("connect to central peer %s", pi.String())
		if err := p.host.Connect(context.Background(), *pi); err != nil {
			log.Errorf("failed to connect to central peer %s: %v", addr, err)
		}
	}

	p.routing = routing.NewRoutingDiscovery(p.kdht)

	time.Sleep(time.Second)

	// register self leafnode with DHT
	ttl, err := p.routing.Advertise(context.Background(), defaultTag)
	if err != nil {
		log.Errorf("failed to advertise: %v", err)
		return err
	}
	log.Infof("advertised with ttl: %s", ttl.String())

	return nil
}

func (p *p2pTransport) identity() libp2p.Option {
	decodedIdentity, err := base64.StdEncoding.DecodeString(p.opt.Identity)
	if err != nil {
		log.Fatalf("failed to decode private key: %v", err)
		return nil
	}

	pk, err := crypto.UnmarshalPrivateKey(decodedIdentity)
	if err != nil {
		log.Fatalf("failed to unmarshal private key: %v", err)
		return nil
	}

	tr := p
	tr.privKey = pk
	return libp2p.Identity(pk)
}

func (p *p2pTransport) handleStream(s network.Stream) {
	defer s.Close()

	// Read message
	buf, err := io.ReadAll(s)
	if err != nil {
		log.Errorf("read stream: %v", err)
		return
	}

	var msg pb.Message
	if err := proto.Unmarshal(buf, &msg); err != nil {
		log.Errorf("unmarshal message: %v", err)
		return
	}

	// Verify signature
	if err := p.verify(&msg, s.Conn().RemotePublicKey()); err != nil {
		log.Errorf("verify message from %s: %v. MsgId: %s Type: %s", s.Conn().RemotePeer(), err, msg.Id, msg.Type)
		return
	}

	// Handle PING/PONG
	if msg.Type == pb.MessageType_PING {
		log.Debugf("received PING from %s", s.Conn().RemotePeer())
		pong := &pb.Message{
			Id:      msg.Id,
			Type:    pb.MessageType_PONG,
			Payload: msg.Payload,
		}
		if err := p.sign(pong); err != nil {
			log.Errorf("sign pong: %v", err)
			return
		}
		out, err := proto.Marshal(pong)
		if err != nil {
			log.Errorf("marshal pong: %v", err)
			return
		}
		if _, err := s.Write(out); err != nil {
			log.Errorf("write pong: %v", err)
		}
		return
	}
	if msg.Type == pb.MessageType_PONG {
		log.Debugf("received PONG from %s", s.Conn().RemotePeer())
		return
	}

	// Handle message
	ctx := context.Background() // TODO: inherit or create context
	if p.handler != nil {
		if err := p.handler(ctx, &msg); err != nil {
			log.Errorf("handle message: %v", err)
			// TODO: Send error response?
		}
	} else {
		log.Warnf("no handler registered")
	}

	// Send ACK
	ack := &pb.Message{
		Id:   msg.Id,
		Type: pb.MessageType_ACK,
	}

	if err := p.sign(ack); err != nil {
		log.Errorf("sign ack: %v", err)
		return
	}

	out, err := proto.Marshal(ack)
	if err != nil {
		log.Errorf("marshal ack: %v", err)
		return
	}

	if _, err := s.Write(out); err != nil {
		log.Errorf("write ack: %v", err)
		return
	}
}
