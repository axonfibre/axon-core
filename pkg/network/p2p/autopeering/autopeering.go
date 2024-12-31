package autopeering

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/libp2p/go-libp2p/p2p/discovery/util"

	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/log"
	"github.com/iotaledger/iota-core/pkg/network"
)

type Manager struct {
	namespace        string
	maxPeers         int
	networkManager   network.Manager
	logger           log.Logger
	host             host.Host
	startOnce        sync.Once
	isStarted        atomic.Bool
	stopOnce         sync.Once
	ctx              context.Context
	stopFunc         context.CancelFunc
	routingDiscovery *routing.RoutingDiscovery
	addrFilter       network.AddressFilter

	advertiseLock   sync.Mutex
	advertiseCtx    context.Context
	advertiseCancel context.CancelFunc
}

// NewManager creates a new autopeering manager.
func NewManager(maxPeers int, networkManager network.Manager, host host.Host, addressFilter network.AddressFilter, logger log.Logger) *Manager {
	return &Manager{
		maxPeers:       maxPeers,
		networkManager: networkManager,
		host:           host,
		logger:         logger.NewChildLogger("Autopeering"),
		addrFilter:     addressFilter,
	}
}

func (m *Manager) MaxNeighbors() int {
	return m.maxPeers
}

// Start starts the autopeering manager.
func (m *Manager) Start(ctx context.Context, networkID string, bootstrapPeers []peer.AddrInfo) (err error) {
	//nolint:contextcheck
	m.startOnce.Do(func() {
		// We will use /iota/networkID/kad/1.0.0 for the DHT protocol.
		// And /iota/networkID/iota-core/1.0.0 for the peer discovery.
		prefix := protocol.ID("/iota")
		extension := protocol.ID(fmt.Sprintf("/%s", networkID))
		m.namespace = fmt.Sprintf("%s%s/%s", prefix, extension, network.CoreProtocolID)
		dhtCtx, dhtCancel := context.WithCancel(ctx)
		kademliaDHT, innerErr := dht.New(
			dhtCtx,
			m.host,
			dht.Mode(dht.ModeServer),
			dht.ProtocolPrefix(prefix),
			dht.ProtocolExtension(extension),
			dht.AddressFilter(m.addrFilter),
			dht.BootstrapPeers(bootstrapPeers...),
			dht.MaxRecordAge(10*time.Minute),
		)
		if innerErr != nil {
			err = innerErr
			dhtCancel()

			return
		}

		// Bootstrap the DHT. In the default configuration, this spawns a Background worker that will keep the
		// node connected to the bootstrap peers and will disconnect from peers that are not useful.
		if innerErr = kademliaDHT.Bootstrap(dhtCtx); innerErr != nil {
			err = innerErr
			dhtCancel()

			return
		}

		m.ctx = dhtCtx

		m.routingDiscovery = routing.NewRoutingDiscovery(kademliaDHT)
		m.startAdvertisingIfNeeded()
		go m.discoveryLoop()

		onGossipNeighborRemovedHook := m.networkManager.OnNeighborRemoved(func(_ network.Neighbor) {
			m.startAdvertisingIfNeeded()
		})
		onGossipNeighborAddedHook := m.networkManager.OnNeighborAdded(func(neighbor network.Neighbor) {
			m.logger.LogInfof("Gossip layer successfully connected with the peer %s", neighbor.Peer())
			m.stopAdvertisingIfNotNeeded()
		})

		m.stopFunc = func() {
			dhtCancel()
			onGossipNeighborRemovedHook.Unhook()
			onGossipNeighborAddedHook.Unhook()
			m.stopAdvertising()
		}

		m.isStarted.Store(true)
	})

	return err
}

// Stop terminates internal background workers. Calling multiple times has no effect.
func (m *Manager) Stop() error {
	if !m.isStarted.Load() {
		return ierrors.New("can't stop the manager: it hasn't been started yet")
	}
	m.stopOnce.Do(func() {
		m.stopFunc()
		m.stopAdvertising()
	})

	return nil
}

func (m *Manager) startAdvertisingIfNeeded() {
	m.advertiseLock.Lock()
	defer m.advertiseLock.Unlock()

	if len(m.networkManager.AutopeeringNeighbors()) >= m.maxPeers {
		return
	}

	if m.advertiseCtx == nil && m.ctx.Err() == nil {
		m.logger.LogInfof("Start advertising for namespace %s", m.namespace)
		m.advertiseCtx, m.advertiseCancel = context.WithCancel(m.ctx)
		util.Advertise(m.advertiseCtx, m.routingDiscovery, m.namespace, discovery.TTL(time.Minute))
	}
}

func (m *Manager) stopAdvertisingIfNotNeeded() {
	if len(m.networkManager.AutopeeringNeighbors()) >= m.maxPeers {
		m.stopAdvertising()
	}
}

func (m *Manager) stopAdvertising() {
	m.advertiseLock.Lock()
	defer m.advertiseLock.Unlock()

	if m.advertiseCancel != nil {
		m.logger.LogInfof("Stop advertising for namespace %s", m.namespace)
		m.advertiseCancel()
		m.advertiseCtx = nil
		m.advertiseCancel = nil
	}
}

func (m *Manager) discoveryLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	m.discoverAndDialPeers()

	for {
		select {
		case <-ticker.C:
			m.discoverAndDialPeers()
		case <-m.ctx.Done():
			return
		}
	}
}

func randomSubset[T any](slice []T, n int) []T {
	if n >= len(slice) {
		rand.Shuffle(len(slice), func(i, j int) {
			slice[i], slice[j] = slice[j], slice[i]
		})

		return slice
	}

	subset := make([]T, n)
	indices := rand.Perm(len(slice)) // Get a slice of random unique indices
	for i := range n {
		subset[i] = slice[indices[i]]
	}

	return subset
}

func (m *Manager) discoverAndDialPeers() {
	autopeeringNeighbors := m.networkManager.AutopeeringNeighbors()
	peersToFind := m.maxPeers - len(autopeeringNeighbors)
	if peersToFind == 0 {
		m.logger.LogDebugf("%d autopeering neighbors connected, not discovering new ones. (max %d)", len(autopeeringNeighbors), m.maxPeers)
		return
	}

	if peersToFind < 0 {
		neighborsToDrop := randomSubset(autopeeringNeighbors, -peersToFind)
		m.logger.LogDebugf("Too many autopeering neighbors connected %d, disconnecting some", len(neighborsToDrop))
		for _, peer := range neighborsToDrop {
			if err := m.networkManager.DisconnectNeighbor(peer.Peer().ID); err != nil {
				m.logger.LogDebugf("Failed to disconnect neighbor %s", peer.Peer().ID)
			}
		}

		return
	}

	findCtx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	defer cancel()

	m.logger.LogDebugf("%d autopeering neighbors connected. Discovering new peers for namespace %s", len(autopeeringNeighbors), m.namespace)
	peerChan, err := m.routingDiscovery.FindPeers(findCtx, m.namespace)
	if err != nil {
		m.logger.LogWarnf("Failed to find peers: %s", err)
		return
	}

	for peerAddrInfo := range peerChan {
		if m.ctx.Err() != nil {
			m.logger.LogDebug("Context is done, stopping dialing new peers")
			return
		}

		if peersToFind <= 0 {
			m.logger.LogDebug("Enough new autopeering peers connected")
			return
		}

		// Do not self-dial.
		if peerAddrInfo.ID == m.host.ID() {
			continue
		}

		// Do not try to dial already connected peers.
		if m.networkManager.NeighborExists(peerAddrInfo.ID) {
			continue
		}

		peerInfo := m.filteredPeerAddrInfo(&peerAddrInfo)
		if len(peerInfo.Addrs) == 0 {
			m.logger.LogWarnf("Filtered out peer %s because it has no public reachable addresses", peerAddrInfo)
			continue
		}

		m.logger.LogInfof("Found peer: %s", peerInfo)

		p := network.NewPeerFromAddrInfo(peerInfo)
		if err := m.networkManager.DialPeer(m.ctx, p); err != nil {
			m.logger.LogWarnf("Failed to dial peer %s: %s", peerAddrInfo, err)
		} else {
			peersToFind--
		}
	}
}

func (m *Manager) filteredPeerAddrInfo(peerAddrInfo *peer.AddrInfo) *peer.AddrInfo {
	return &peer.AddrInfo{
		ID:    peerAddrInfo.ID,
		Addrs: m.addrFilter(peerAddrInfo.Addrs),
	}
}
