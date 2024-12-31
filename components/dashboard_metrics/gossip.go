package dashboardmetrics

import (
	"math"
	"sync"
)

var (
	lastGossipMetricsLock = &sync.RWMutex{}
	lastGossipMetrics     = &GossipMetrics{
		Incoming: 0,
		New:      0,
		Outgoing: 0,
	}
	lastIncomingBlocksCount    uint32
	lastIncomingNewBlocksCount uint32
	lastOutgoingBlocksCount    uint32
)

// uint32Diff returns the difference between newCount and oldCount
// and catches overflows.
func uint32Diff(newCount uint32, oldCount uint32) uint32 {
	// Catch overflows
	if newCount < oldCount {
		return (math.MaxUint32 - oldCount) + newCount
	}

	return newCount - oldCount
}

// measureGossipMetrics measures the BPS values.
func measureGossipMetrics() {
	newIncomingBlocksCount := deps.P2PMetrics.IncomingBlocks.Load()
	newIncomingNewBlocksCount := deps.P2PMetrics.IncomingNewBlocks.Load()
	newOutgoingBlocksCount := deps.P2PMetrics.OutgoingBlocks.Load()

	// calculate the new BPS metrics
	lastGossipMetricsLock.Lock()
	defer lastGossipMetricsLock.Unlock()

	lastGossipMetrics = &GossipMetrics{
		Incoming: uint32Diff(newIncomingBlocksCount, lastIncomingBlocksCount),
		New:      uint32Diff(newIncomingNewBlocksCount, lastIncomingNewBlocksCount),
		Outgoing: uint32Diff(newOutgoingBlocksCount, lastOutgoingBlocksCount),
	}

	// store the new counters
	lastIncomingBlocksCount = newIncomingBlocksCount
	lastIncomingNewBlocksCount = newIncomingNewBlocksCount
	lastOutgoingBlocksCount = newOutgoingBlocksCount
}

func gossipMetrics() *GossipMetrics {
	lastGossipMetricsLock.RLock()
	defer lastGossipMetricsLock.RUnlock()

	return lastGossipMetrics
}
