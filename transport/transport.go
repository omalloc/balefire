package transport

import (
	"context"
	"encoding/base64"
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

	"github.com/omalloc/balefire/api/transport"
)

// Note: A libp2p-backed Transport implementation will live in this package,
// hidden behind the Transport interface to avoid leaking dependencies.

var _ transport.Transport = (*p2pTransport)(nil)

const (
	defaultProtocol   = "/balefire/1.0.0"
	defaultTag        = "balefire-mdns"
	defaultListenAddr = "/ip4/0.0.0.0/tcp/0"
)

type Option struct {
	Mode         string
	Identity     string
	CentralPeers []string
	ListenAddrs  []string
}

type p2pTransport struct {
	mode         string
	centralPeers []string

	// p2p host, dht, etc.
	protocol protocol.ID
	host     host.Host
	kadDHT   *dht.IpfsDHT
	routing  *routing.RoutingDiscovery
	stop     chan struct{}
}

func NewP2PTransport(opt Option) (transport.Transport, error) {
	tr := &p2pTransport{
		mode:         opt.Mode,
		protocol:     protocol.ID(defaultProtocol),
		centralPeers: opt.CentralPeers,
		stop:         make(chan struct{}, 1),
	}

	if len(opt.ListenAddrs) == 0 {
		opt.ListenAddrs = []string{defaultListenAddr}
	}

	opts := make([]libp2p.Option, 0, 16)
	if opt.Mode == "server" {
		decodedIdentity, err := base64.StdEncoding.DecodeString(opt.Identity)
		if err != nil {
			log.Fatalf("failed to decode private key: %v", err)
			return nil, err
		}

		pk, err := crypto.UnmarshalPrivateKey(decodedIdentity)
		if err != nil {
			log.Fatalf("failed to unmarshal private key: %v", err)
			return nil, err
		}

		opts = append(opts, libp2p.Identity(pk))
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

	kadDHT, err := dht.New(context.Background(), host, dht.Mode(tr.dhtMode()))
	if err != nil {
		log.Fatalf("failed to create DHT: %v", err)
		return nil, err
	}
	tr.kadDHT = kadDHT

	return tr, nil
}

// Start implements [transport.Transport].
func (p *p2pTransport) Start(ctx context.Context) error {
	log.Infof("transport starting with %s mode", p.mode)

	hostID := p.host.ID()
	for _, addr := range p.host.Addrs() {
		log.Infof("transport listening on %s/p2p/%s", addr.String(), hostID)
	}

	p.host.Network().Notify(&ConnNotifiee{})

	p.host.SetStreamHandler(p.protocol, p.handleStream)

	// if in server mode, listen for incoming connections
	// and serve DHT server
	if err := p.kadDHT.Bootstrap(ctx); err != nil {
		return err
	}

	// connect to central peers if in leaf mode
	if p.mode == "leaf" {

		for _, addr := range p.centralPeers {
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

		p.routing = routing.NewRoutingDiscovery(p.kadDHT)

		time.Sleep(time.Second)

		// register self leafnode with DHT
		ttl, err := p.routing.Advertise(context.Background(), defaultTag)
		if err != nil {
			log.Errorf("failed to advertise: %v", err)
			return err
		}
		log.Infof("advertised with ttl: %s", ttl.String())
	}

	// start a goroutine to periodically connect to peers
	if p.mode == "leaf" {
		go p.tickConnectPeers()
	}

	log.Infof("transport started with %s mode", p.mode)
	return nil
}

// Stop implements [transport.Transport].
func (p *p2pTransport) Stop(ctx context.Context) error {
	log.Infof("transport stopped")
	return nil
}

// OnReceive implements [transport.Transport].
func (p *p2pTransport) OnReceive(h transport.Handler) {
	panic("unimplemented")
}

// Send implements [transport.Transport].
func (p *p2pTransport) Send(ctx context.Context, dst string, msg transport.Message) error {
	panic("unimplemented")
}

func (p *p2pTransport) dhtMode() dht.ModeOpt {
	dhtMode := dht.ModeClient
	if p.mode == "server" {
		dhtMode = dht.ModeServer
	}
	return dhtMode
}

func (p *p2pTransport) tickConnectPeers() {

	tick := time.NewTicker(time.Second * 5)

	for {
		select {
		case <-tick.C:
			peerCh, err := p.routing.FindPeers(context.Background(), defaultTag)
			if err != nil {
				log.Errorf("failed to find peers: %v", err)
				continue
			}

			for pi := range peerCh {
				if pi.ID == p.host.ID() {
					continue
				}

				log.Infof("connecting to peer %s", pi.String())
				if err := p.host.Connect(context.Background(), pi); err != nil {
					log.Errorf("failed to connect to peer %s: %v", pi.String(), err)
				}
			}

		case <-p.stop:
			return
		}
	}
}

func (p *p2pTransport) handleStream(s network.Stream) {}
