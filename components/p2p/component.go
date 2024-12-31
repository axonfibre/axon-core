package p2p

import (
	"context"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
	"go.uber.org/dig"

	"github.com/iotaledger/hive.go/app"
	"github.com/iotaledger/hive.go/app/configuration"
	hivep2p "github.com/iotaledger/hive.go/crypto/p2p"
	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/iota-core/pkg/daemon"
	"github.com/iotaledger/iota-core/pkg/network"
	"github.com/iotaledger/iota-core/pkg/network/p2p"
	"github.com/iotaledger/iota-core/pkg/protocol"
)

func init() {
	Component = &app.Component{
		Name:      "P2P",
		DepsFunc:  func(cDeps dependencies) { deps = cDeps },
		Params:    params,
		Provide:   provide,
		Configure: configure,
		Run:       run,
	}
}

var (
	Component *app.Component
	deps      dependencies
)

type dependencies struct {
	dig.In
	PeeringConfig        *configuration.Configuration `name:"peeringConfig"`
	PeeringConfigManager *p2p.ConfigManager
	NetworkManager       network.Manager
	Protocol             *protocol.Protocol
}

func provide(c *dig.Container) error {
	type configManagerDeps struct {
		dig.In
		PeeringConfig         *configuration.Configuration `name:"peeringConfig"`
		PeeringConfigFilePath *string                      `name:"peeringConfigFilePath"`
	}

	if err := c.Provide(func(deps configManagerDeps) *p2p.ConfigManager {
		p2pConfigManager := p2p.NewConfigManager(func(peers []*p2p.PeerConfigItem) error {
			if err := deps.PeeringConfig.Set(CfgPeers, peers); err != nil {
				return err
			}

			return deps.PeeringConfig.StoreFile(*deps.PeeringConfigFilePath, 0o600, []string{"p2p"})
		})

		// peers from peering config
		var peers []*p2p.PeerConfig
		if err := deps.PeeringConfig.Unmarshal(CfgPeers, &peers); err != nil {
			Component.LogPanicf("invalid peer config: %s", err)
		}

		for i, p := range peers {
			multiAddr, err := multiaddr.NewMultiaddr(p.MultiAddress)
			if err != nil {
				Component.LogPanicf("invalid config peer address at pos %d: %s", i, err)
			}

			if err = p2pConfigManager.AddPeer(multiAddr, p.Alias); err != nil {
				Component.LogWarnf("unable to add peer to config manager %s: %s", p.MultiAddress, err)
			}
		}

		// peers from CLI arguments
		applyAliases := true
		if len(ParamsPeers.Peers) != len(ParamsPeers.PeerAliases) {
			Component.LogWarnf("won't apply peer aliases: you must define aliases for all defined static peers (got %d aliases, %d peers).", len(ParamsPeers.PeerAliases), len(ParamsPeers.Peers))
			applyAliases = false
		}

		peersMultiAddresses, err := getMultiAddrsFromString(ParamsPeers.Peers)
		if err != nil {
			Component.LogFatal(err.Error())
		}

		peerAdded := false
		for i, multiAddr := range peersMultiAddresses {
			var alias string
			if applyAliases {
				alias = ParamsPeers.PeerAliases[i]
			}

			if err = p2pConfigManager.AddPeer(multiAddr, alias); err != nil {
				Component.LogWarnf("unable to add peer to config manager %s: %s", multiAddr.String(), err)
			}

			peerAdded = true
		}

		p2pConfigManager.StoreOnChange(true)

		if peerAdded {
			if err := p2pConfigManager.Store(); err != nil {
				Component.LogWarnf("failed to store peering config: %s", err)
			}
		}

		return p2pConfigManager
	}); err != nil {
		Component.LogPanic(err.Error())
	}

	type p2pResult struct {
		dig.Out
		NodePrivateKey crypto.PrivKey `name:"nodePrivateKey"`
		Host           host.Host
	}

	if err := c.Provide(func() p2pResult {
		res := p2pResult{}

		// make sure nobody copies around the peer store since it contains the private key of the node
		Component.LogInfof(`WARNING: never share the file "%s" as it contains your node's private key!`, ParamsP2P.IdentityPrivateKeyFilePath)

		// load up the previously generated identity or create a new one
		nodePrivateKey, newlyCreated, err := hivep2p.LoadOrCreateIdentityPrivateKey(ParamsP2P.IdentityPrivateKeyFilePath, ParamsP2P.IdentityPrivateKey)
		if err != nil {
			Component.LogPanic(err.Error())
		}
		res.NodePrivateKey = nodePrivateKey

		if newlyCreated {
			Component.LogInfof(`stored new private key for peer identity under "%s"`, ParamsP2P.IdentityPrivateKeyFilePath)
		} else {
			Component.LogInfof(`loaded existing private key for peer identity from "%s"`, ParamsP2P.IdentityPrivateKeyFilePath)
		}

		connManager, err := connmgr.NewConnManager(
			ParamsP2P.ConnectionManager.LowWatermark,
			ParamsP2P.ConnectionManager.HighWatermark,
			connmgr.WithEmergencyTrim(true),
		)
		if err != nil {
			Component.LogPanicf("unable to initialize connection manager: %s", err)
		}

		createdHost, err := libp2p.New(
			libp2p.ListenAddrStrings(ParamsP2P.BindMultiAddresses...),
			libp2p.Identity(nodePrivateKey),
			libp2p.Transport(tcp.NewTCPTransport),
			libp2p.ConnectionManager(connManager),
			libp2p.NATPortMap(),
			libp2p.DisableRelay(),
			// Define a custom address factory to inject external addresses to the DHT advertisements.
			libp2p.AddrsFactory(externalAddresses(ParamsP2P.Autopeering.ExternalMultiAddresses, ParamsP2P.Autopeering.AllowLocalIPs)),
		)
		if err != nil {
			Component.LogFatalf("unable to initialize libp2p host: %s", err)
		}
		res.Host = createdHost

		Component.LogInfof("Initialized P2P host %s %s", createdHost.ID().String(), createdHost.Addrs())

		return res
	}); err != nil {
		Component.LogPanic(err.Error())
	}

	if err := c.Provide(func() *p2p.Metrics {
		return &p2p.Metrics{}
	}); err != nil {
		Component.LogPanic(err.Error())
	}

	type p2pManagerDeps struct {
		dig.In
		Host       host.Host
		P2PMetrics *p2p.Metrics
	}

	return c.Provide(func(inDeps p2pManagerDeps) network.Manager {
		onBlockSentCallback := func() {
			inDeps.P2PMetrics.OutgoingBlocks.Add(1)
		}

		return p2p.NewManager(Component.Logger, inDeps.Host, ParamsP2P.Autopeering.MaxPeers, ParamsP2P.Autopeering.AllowLocalIPs, onBlockSentCallback)
	})
}

func configure() error {
	// log the p2p events
	deps.NetworkManager.OnNeighborAdded(func(neighbor network.Neighbor) {
		Component.LogInfof("neighbor added: %s / %s", neighbor.Peer().PeerAddresses, neighbor.Peer().ID)
	})

	deps.NetworkManager.OnNeighborRemoved(func(neighbor network.Neighbor) {
		Component.LogInfof("neighbor removed: %s / %s", neighbor.Peer().PeerAddresses, neighbor.Peer().ID)
	})

	return nil
}

func run() error {
	if err := Component.Daemon().BackgroundWorker(Component.Name, func(ctx context.Context) {
		defer deps.NetworkManager.Shutdown()

		if err := deps.NetworkManager.Start(ctx, deps.Protocol.LatestAPI().ProtocolParameters().NetworkName(), bootstrapPeers()); err != nil {
			Component.LogFatalf("Failed to start p2p manager: %s", err)
		}

		//nolint:contextcheck // false positive
		connectConfigKnownPeers()

		<-ctx.Done()
	}, daemon.PriorityP2P); err != nil {
		Component.LogFatalf("Failed to start as daemon: %s", err)
	}

	return nil
}

func bootstrapPeers() []peer.AddrInfo {
	peersMultiAddresses, err := getMultiAddrsFromString(ParamsP2P.Autopeering.BootstrapPeers)
	if err != nil {
		Component.LogFatalf("Failed to parse bootstrapPeers param: %s", err)
	}

	addrInfos := make([]peer.AddrInfo, 0, len(peersMultiAddresses))
	for _, multiAddr := range peersMultiAddresses {
		addrInfo, err := peer.AddrInfoFromP2pAddr(multiAddr)
		if err != nil {
			Component.LogFatalf("Failed to parse bootstrap peer multiaddress: %s", err)
		}
		addrInfos = append(addrInfos, *addrInfo)
	}

	return addrInfos
}

func getMultiAddrsFromString(peers []string) ([]multiaddr.Multiaddr, error) {
	peersMultiAddresses := make([]multiaddr.Multiaddr, 0, len(peers))

	for _, peer := range peers {
		peerMultiAddr, err := multiaddr.NewMultiaddr(peer)
		if err != nil {
			return nil, ierrors.Wrapf(err, "invalid peer multiaddr \"%s\"", peer)
		}
		peersMultiAddresses = append(peersMultiAddresses, peerMultiAddr)
	}

	return peersMultiAddresses, nil
}

// connects to the peers defined in the config.
func connectConfigKnownPeers() {
	for _, p := range deps.PeeringConfigManager.Peers() {
		multiAddr, err := multiaddr.NewMultiaddr(p.MultiAddress)
		if err != nil {
			Component.LogPanicf("invalid peer address: %s", err)
		}

		// we try to parse the multi address and check if there is a "/p2p" part with ID
		_, err = peer.AddrInfoFromP2pAddr(multiAddr)
		if err != nil {
			Component.LogPanicf("invalid peer address info: %s", err)
		}

		if _, err := deps.NetworkManager.AddManualPeer(multiAddr); err != nil {
			Component.LogInfof("failed to add peer: %s, error: %s", multiAddr.String(), err)
		}
	}
}

func externalAddresses(additionalMultiaddresses []string, allowLocalNetworks bool) network.AddressFilter {
	var externalMultiAddrs []multiaddr.Multiaddr

	// Add the external multi addresses to the list of addresses to be announced.
	if len(additionalMultiaddresses) > 0 {
		for _, externalMultiAddress := range additionalMultiaddresses {
			addr, err := multiaddr.NewMultiaddr(externalMultiAddress)
			if err != nil {
				Component.LogPanicf("unable to parse external multi address %s: %s", externalMultiAddress, err)
			}

			externalMultiAddrs = append(externalMultiAddrs, addr)
		}
	}

	publicFilter := network.PublicOnlyAddressesFilter(allowLocalNetworks)

	return func(addresses []multiaddr.Multiaddr) []multiaddr.Multiaddr {
		return publicFilter(append(addresses, externalMultiAddrs...))
	}
}
