package transport

import (
	"github.com/go-kratos/kratos/v2/log"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/multiformats/go-multiaddr"
)

type ConnNotifiee struct{}

func (n *ConnNotifiee) Listen(network.Network, multiaddr.Multiaddr)      {}
func (n *ConnNotifiee) ListenClose(network.Network, multiaddr.Multiaddr) {}

func (n *ConnNotifiee) Connected(net network.Network, c network.Conn) {
	dir := "outbound"
	if c.Stat().Direction == network.DirInbound {
		dir = "inbound"
	}

	log.Infof("%s peer %s addr %s", dir, c.RemotePeer(), c.RemoteMultiaddr())
}

func (n *ConnNotifiee) Disconnected(net network.Network, c network.Conn) {
	log.Infof("disconnect peer %s", c.RemotePeer())
}
