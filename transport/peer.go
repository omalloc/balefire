package transport

import (
	"bufio"
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	kbucket "github.com/libp2p/go-libp2p-kbucket"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
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
		h.Network().ClosePeer(distances[i].pid)
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

func (p *p2pTransport) maintainLeafPeers(ctx context.Context, interval time.Duration) {

	time.Sleep(time.Second * 10)

	peerChan, err := p.routing.FindPeers(ctx, defaultNamespace)
	if err != nil {
		log.Warnf("failed to find peers for leaf maintenance: %v", err)
		return
	}

	// maintain a local connection pool and a removal notifier
	connected := make(map[peer.ID]struct{})
	lostconnected := make(map[peer.ID]time.Time)
	removeCh := make(chan peer.ID, 1)

	ticker := time.NewTicker(time.Second * 15)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stop:
			return
		case pidInfo, ok := <-peerChan:
			if !ok {
				// discovery channel closed: try to reopen (best-effort)
				peerChan, err = p.routing.FindPeers(ctx, defaultNamespace)
				if err != nil {
					log.Warnf("failed to re-open peer discovery channel: %v", err)
					ticker.Stop()

					time.Sleep(time.Minute)
					ticker = time.NewTicker(time.Second * 15)
					continue
				}
				continue
			}

			if pidInfo.ID == p.host.ID() {
				continue
			}

			if _, exists := connected[pidInfo.ID]; exists {
				continue
			}

			if lostTime, exist := lostconnected[pidInfo.ID]; exist {
				if time.Since(lostTime) < time.Minute { // wait 1 minutes before retrying
					continue
				} else {
					delete(lostconnected, pidInfo.ID)
				}
			}

			// try to create a stream to the discovered peer
			stream, err := p.host.NewStream(ctx, pidInfo.ID, protocol.ID(defaultProtocol))
			if err != nil {
				log.Warnf("failed to create stream to peer %s: %v", pidInfo.ID, err)
				lostconnected[pidInfo.ID] = time.Now()
				continue
			}

			rw := bufio.NewReadWriter(bufio.NewReader(stream), bufio.NewWriter(stream))
			connected[pidInfo.ID] = struct{}{}
			log.Infof("leaf connected to peer %s", pidInfo.ID)

			// read/write goroutines will notify removal via removeCh when they exit
			go func(pid peer.ID, rw *bufio.ReadWriter) {
				readData(pid, rw)
				removeCh <- pid
			}(pidInfo.ID, rw)

			go func(pid peer.ID, rw *bufio.ReadWriter) {
				writeData(rw)
				removeCh <- pid
			}(pidInfo.ID, rw)

		case pid := <-removeCh:
			if _, ok := connected[pid]; ok {
				delete(connected, pid)
				log.Infof("leaf disconnected from peer %s", pid)
			}

		case <-ticker.C:
			// also ensure we attach to any already-connected peers (e.g., from other discovery paths)
			for _, conn := range p.host.Network().Conns() {
				peerID := conn.RemotePeer()
				if peerID == p.host.ID() {
					continue
				}
				if _, exists := connected[peerID]; exists {
					continue
				}

				stream, err := p.host.NewStream(ctx, peerID, protocol.ID(defaultProtocol))
				if err != nil {
					log.Warnf("failed to create stream to existing conn peer %s: %v", peerID, err)
					continue
				}

				rw := bufio.NewReadWriter(bufio.NewReader(stream), bufio.NewWriter(stream))
				connected[peerID] = struct{}{}
				log.Infof("leaf connected to peer (from Conns) %s", peerID)

				go func(pid peer.ID, rw *bufio.ReadWriter) {
					readData(pid, rw)
					removeCh <- pid
				}(peerID, rw)

				go func(pid peer.ID, rw *bufio.ReadWriter) {
					writeData(rw)
					removeCh <- pid
				}(peerID, rw)
			}
		}
	}

}

func readData(peerID peer.ID, rw *bufio.ReadWriter) {
	for {
		str, err := rw.ReadString('\n')
		if err != nil {
			fmt.Println("Error reading from buffer")
			return
		}

		if str == "" {
			return
		}
		if str != "\n" {
			// Green console colour: 	\x1b[32m
			// Reset console colour: 	\x1b[0m
			fmt.Printf("\x1b[32m[%s] From %s\x1b[0m > %s", time.Now().Format(time.TimeOnly), peerID.ShortString(), str)
		}
	}
}

func writeData(rw *bufio.ReadWriter) {
	for range time.Tick(time.Second * 2) {
		_, err := rw.WriteString("ping\n")
		if err != nil {
			fmt.Println("Error writing to buffer")
			return
		}
		rw.Flush()
	}
}
