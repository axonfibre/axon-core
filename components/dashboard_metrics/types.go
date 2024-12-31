package dashboardmetrics

// NodeInfoExtended represents extended information about the node.
type NodeInfoExtended struct {
	Version       string `serix:",lenPrefix=uint8"`
	LatestVersion string `serix:",lenPrefix=uint8"`
	Uptime        int64  `serix:""`
	NodeID        string `serix:",lenPrefix=uint8"`
	MultiAddress  string `serix:",lenPrefix=uint8"`
	Alias         string `serix:",lenPrefix=uint8"`
	MemoryUsage   int64  `serix:""`
}

// DatabaseSizesMetric represents database size metrics.
type DatabaseSizesMetric struct {
	Permanent  int64 `serix:""`
	Prunable   int64 `serix:""`
	TxRetainer int64 `serix:""`
	Total      int64 `serix:""`
	Time       int64 `serix:""`
}

// GossipMetrics represents the metrics for blocks per second.
type GossipMetrics struct {
	Incoming uint32 `serix:""`
	New      uint32 `serix:""`
	Outgoing uint32 `serix:""`
}
