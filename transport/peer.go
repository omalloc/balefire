package transport

import (
	"context"
	"sort"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	kbucket "github.com/libp2p/go-libp2p-kbucket"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	pb "github.com/omalloc/balefire/api/transport/v1"
)

func (p *p2pTransport) maintainClosestPeers(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stop:
			return
		case <-ticker.C:
			// 定期刷新连接
			if err := findAndConnectClosestPeers(ctx, p.host, p.kdht, p.host.ID(), 10); err != nil {
				log.Warnf("auto connect closest peers failed %v", err)
			}

			// 断开过远的连接（可选）
			pruneDistantPeers(p.host, p.host.ID(), 20)
		}
	}
}

func (p *p2pTransport) maintainPingPong(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stop:
			return
		case <-ticker.C:
			for _, pid := range p.host.Network().Peers() {
				go func(target peer.ID) {
					// Send PING
					ping := &pb.Message{
						Id:        uuid.New().String(),
						Type:      pb.MessageType_PING,
						Payload:   []byte("ping"),
						Timestamp: time.Now().UnixNano(),
					}
					if err := p.Send(ctx, target.String(), ping); err != nil {
						log.Warnf("failed to ping peer %s: %v", target, err)
					} else {
						log.Debugf("ping peer %s success", target)
					}
				}(pid)
			}
		}
	}
}

func pruneDistantPeers(h host.Host, target peer.ID, keepCount int) {
	// 获取所有连接
	conns := h.Network().Conns()

	// 计算每个连接的距离并排序
	type peerDist struct {
		pid  peer.ID
		dist []byte
	}

	var distances []peerDist
	for _, conn := range conns {
		pid := conn.RemotePeer()
		dist := xor(target, pid)
		distances = append(distances, peerDist{pid, dist})
	}

	// 按距离排序（从近到远）
	sort.Slice(distances, func(i, j int) bool {
		a, b := distances[i].dist, distances[j].dist
		for k := 0; k < len(a); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return false
	})

	// 断开距离较远的连接
	for i := keepCount; i < len(distances); i++ {
		_ = h.Network().ClosePeer(distances[i].pid)
	}
}

// XOR with peer ID to get distance
func xor(a, b peer.ID) []byte {
	aKey := kbucket.ConvertPeerID(a)
	bKey := kbucket.ConvertPeerID(b)

	distance := make([]byte, len(aKey))
	for i := 0; i < len(aKey); i++ {
		distance[i] = aKey[i] ^ bKey[i]
	}
	return distance
}

// Compare distance of two peer IDs to a target peer ID
func compare(target peer.ID, a, b peer.ID) bool {
	distA := xor(target, a)
	distB := xor(target, b)

	for i := 0; i < len(distA); i++ {
		if distA[i] != distB[i] {
			return distA[i] < distB[i]
		}
	}

	return false
}

// Find and connect to the closest N peers
func findAndConnectClosestPeers(ctx context.Context, h host.Host, kdht *dht.IpfsDHT, target peer.ID, count int) error {
	// Use DHT routing table to get nearest peers
	peers := kdht.RoutingTable().NearestPeers(
		kbucket.ConvertPeerID(target),
		count*2, // Find more for fault tolerance
	)

	// Sort by distance
	sort.Slice(peers, func(i, j int) bool {
		return compare(target, peers[i], peers[j])
	})

	// Connect to closest peers
	connected := 0
	for _, p := range peers {
		if p == h.ID() || connected >= count {
			break
		}

		// Check if already connected
		if len(h.Network().ConnsToPeer(p)) > 0 {
			continue
		}

		// Get addresses and connect
		addrs, err := kdht.FindPeer(ctx, p)
		if len(addrs.Addrs) == 0 || err != nil {
			continue
		}

		if err = h.Connect(ctx, peer.AddrInfo{
			ID:    p,
			Addrs: addrs.Addrs,
		}); err == nil {
			connected++
			log.Infof("Connected to close peer (distance: %x)\n", xor(target, p)[:8])
		}
	}

	return nil
}
