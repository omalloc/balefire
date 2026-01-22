package transport

import (
	"bufio"
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
	dutil "github.com/libp2p/go-libp2p/p2p/discovery/util"
	"github.com/multiformats/go-multiaddr"

	transportv1 "github.com/omalloc/balefire/api/transport"
)

// Note: A libp2p-backed Transport implementation will live in this package,
// hidden behind the Transport interface to avoid leaking dependencies.

var _ transportv1.Transport = (*p2pTransport)(nil)

const (
	defaultProtocol   = "/balefire/1.0.0"
	defaultNamespace  = "rendezvous"
	defaultListenAddr = "/ip4/0.0.0.0/tcp/0"
)

type Option struct {
	Mode         transportv1.Mode
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
}

func NewP2PTransport(opt Option) (transportv1.Transport, error) {
	tr := &p2pTransport{
		opt:      opt,
		protocol: protocol.ID(defaultProtocol),
		stop:     make(chan struct{}, 1),
	}

	if len(opt.ListenAddrs) == 0 {
		opt.ListenAddrs = []string{defaultListenAddr}
	}

	opts := make([]libp2p.Option, 0, 16)

	if opt.Mode == transportv1.ModeServer {
		opts = append(opts, tr.identity())
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

	host.Network().Notify(&ConnNotifiee{})

	return tr, nil
}

// Start implements [transportv1.Transport].
func (p *p2pTransport) Start(ctx context.Context) error {
	log.Infof("transport starting with %s mode", p.opt.Mode)

	hostID := p.host.ID()
	for _, addr := range p.host.Addrs() {
		log.Infof("transport listening on %s/p2p/%s", addr.String(), hostID)
	}

	bootstrapPeers := make([]peer.AddrInfo, 0, len(p.opt.CentralPeers))
	for _, addr := range p.opt.CentralPeers {
		maddr, _ := multiaddr.NewMultiaddr(addr)
		pi, _ := peer.AddrInfoFromP2pAddr(maddr)
		if pi == nil {
			continue
		}
		bootstrapPeers = append(bootstrapPeers, *pi)
	}

	kadDHT, err := dht.New(context.Background(), p.host,
		dht.Mode(p.dhtMode()),                         // 设置DHT模式
		dht.BucketSize(20),                            // 调整桶大小
		dht.RoutingTableRefreshPeriod(10*time.Minute), // 调整路由表刷新周期
		dht.BootstrapPeers(bootstrapPeers...),
	)
	if err != nil {
		log.Fatalf("failed to create DHT: %v", err)
		return err
	}
	p.kdht = kadDHT

	// if in server mode, listen for incoming connections
	// and serve DHT server
	if err := p.kdht.Bootstrap(ctx); err != nil {
		return err
	}

	// Wait a bit to let bootstrapping finish (really bootstrap should block until it's ready, but that isn't the case yet.)
	time.Sleep(time.Second)

	// advertise self
	p.advertise()

	p.host.SetStreamHandler(p.protocol, p.handleStream)

	// send messages to connected leaf nodes
	go p.maintainLeafPeers(ctx, time.Minute)

	// connect to central peers if in leaf mode
	if p.opt.Mode == transportv1.ModeLeaf {
		// start a goroutine to periodically connect to others peer
		go p.maintainClosestPeers(ctx, time.Minute)
	}

	log.Infof("transport started with %s mode", p.opt.Mode)
	return nil
}

// Stop implements [transportv1.Transport].
func (p *p2pTransport) Stop(ctx context.Context) error {
	p.stop <- struct{}{}
	log.Infof("transport stopped")
	return nil
}

// OnReceive implements [transportv1.Transport].
func (p *p2pTransport) OnReceive(handler transportv1.Handler) {
	panic("unimplemented")
}

// Send implements [transportv1.Transport].
func (p *p2pTransport) Send(ctx context.Context, dst string, message transportv1.Message) error {
	maddr, err := multiaddr.NewMultiaddr(dst)
	if err != nil {
		return fmt.Errorf("parse multiaddr: %w", err)
	}
	peerId, err := peer.AddrInfoFromP2pAddr(maddr)
	if err != nil {
		return fmt.Errorf("addr info: %w", err)
	}

	if len(p.host.Network().ConnsToPeer(peerId.ID)) <= 0 {
		return fmt.Errorf("not connected to peer %s", peerId.ID.String())
	}

	s, err := p.host.NewStream(ctx, peerId.ID, p.protocol)
	if err != nil {
		return fmt.Errorf("create stream: %w", err)
	}
	defer s.Close()

	buf, err := message.Marshal()
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

	// Read reply (blocks until remote closes). Use a goroutine and abort on context.
	ackCh := make(chan []byte, 1)
	errCh := make(chan error, 1)

	go func() {
		b, err := io.ReadAll(s)
		if err != nil {
			select {
			case errCh <- err:
			default:
			}
			return
		}
		select {
		case ackCh <- b:
		default:
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
		var reply transportv1.Message = NewEmptyMessage()
		if err := reply.Unmarshal(b); err != nil {
			return fmt.Errorf("unmarshal ack: %w", err)
		}
		if reply.GetKind() != transportv1.KindACK || reply.GetID() != message.GetID() {
			return fmt.Errorf("invalid ACK")
		}
	}

	return nil
}

// dhtMode returns the DHT mode based on the config transport mode.
func (p *p2pTransport) dhtMode() dht.ModeOpt {
	dhtMode := dht.ModeClient
	if p.opt.Mode == transportv1.ModeServer {
		dhtMode = dht.ModeServer
	}
	return dhtMode
}

func (p *p2pTransport) advertise() error {

	p.routing = routing.NewRoutingDiscovery(p.kdht)

	// register self leafnode with DHT
	dutil.Advertise(context.Background(), p.routing, defaultNamespace)

	log.Infof("advertised self successfully announced with tag %s", defaultNamespace)
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

	return libp2p.Identity(pk)
}

func (p *p2pTransport) handleStream(stream network.Stream) {
	log.Infof("new stream from %s", stream.Conn().RemotePeer().String())

	// Create a buffer stream for non blocking read and write.
	rw := bufio.NewReadWriter(bufio.NewReader(stream), bufio.NewWriter(stream))

	go p.read(stream.Conn().RemotePeer(), rw)

	go func(w *bufio.ReadWriter) {
		w.Write([]byte("ok"))
		w.Flush()
	}(rw)

}

func (p *p2pTransport) read(id peer.ID, rw *bufio.ReadWriter) {
	for {

		str, err := rw.ReadString('\n')
		if err != nil {
			log.Errorf("Error reading from buffer: %v", err)
			return
		}

		if str == "" {
			return
		}

		if str != "\n" {

			// Green console colour: 	\x1b[32m
			// Reset console colour: 	\x1b[0m
			fmt.Printf("\x1b[32m[%s] From %s\x1b[0m > %s\n", time.Now().Format(time.TimeOnly), id.ShortString(), str)
		}

	}
}
