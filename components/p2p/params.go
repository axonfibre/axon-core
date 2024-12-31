package p2p

import (
	"github.com/iotaledger/hive.go/app"
)

const (
	// CfgPeers defines the static peers this node should retain a connection to (CLI).
	CfgPeers = "peers"
)

// ParametersP2P contains the definition of configuration parameters used by the p2p plugin.
type ParametersP2P struct {
	// BindAddress defines on which multi addresses the p2p service should listen on.
	BindMultiAddresses []string `default:"/ip4/0.0.0.0/tcp/15600,/ip6/::/tcp/15600" usage:"the bind multi addresses for p2p connections"`

	ConnectionManager struct {
		// Defines the high watermark to use within the connection manager.
		HighWatermark int `default:"10" usage:"the threshold up on which connections count truncates to the lower watermark"`
		// Defines the low watermark to use within the connection manager.
		LowWatermark int `default:"5" usage:"the minimum connections count to hold after the high watermark was reached"`
	}

	// Defines the private key used to derive the node identity (optional).
	IdentityPrivateKey string `default:"" usage:"private key used to derive the node identity (optional)"`

	// Defines the file path to the private key used to derive the node identity.
	IdentityPrivateKeyFilePath string `default:"testnet/p2p/identity.key" usage:"the file path to the private key used to derive the node identity"`

	Autopeering struct {
		// MaxPeers defines the max number of auto-peer connections. Set to 0 to disable auto-peering.
		MaxPeers int `default:"5" usage:"the max number of auto-peer connections. Set to 0 to disable auto-peering."`

		// Defines the peers to be used as discovery for other peers (CLI).
		BootstrapPeers []string `default:"" usage:"peers to be used as discovery for other peers"`

		// AllowLocalIPs defines if local IPs are allowed to be used for autopeering.
		AllowLocalIPs bool `default:"false" usage:"allow local IPs to be used for autopeering"`

		// ExternalMultiAddress defines additional p2p multiaddresses to be advertised via DHT.
		ExternalMultiAddresses []string `default:"" usage:"external reacheable multi addresses advertised to the network"`
	}
}

// ParametersPeers contains the definition of the parameters used by peers.
type ParametersPeers struct {
	// Defines the static peers this node should retain a connection to (CLI).
	Peers []string `default:"" usage:"the static peers this node should retain a connection to (CLI)"`
	// Defines the aliases of the static peers (must be the same length like CfgP2PPeers) (CLI).
	PeerAliases []string `default:"" usage:"the aliases of the static peers (must be the same amount like \"p2p.peers\""`
}

var (
	ParamsP2P   = &ParametersP2P{}
	ParamsPeers = &ParametersPeers{}
)

var params = &app.ComponentParams{
	Params: map[string]any{
		"p2p": ParamsP2P,
	},
	AdditionalParams: map[string]map[string]any{
		"peeringConfig": {
			"p2p": ParamsPeers,
		},
	},
	Masked: []string{"p2p.identityPrivateKey"},
}
