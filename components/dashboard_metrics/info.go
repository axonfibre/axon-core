package dashboardmetrics

import (
	"runtime"
	"time"
)

var (
	nodeStartupTimestamp = time.Now()
)

func nodeInfoExtended() *NodeInfoExtended {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	getExternalMultiAddr := func() string {
		var fallback string

		for i, addr := range deps.Host.Addrs() {
			if i == 0 {
				fallback = addr.String()
			}

			for _, protocol := range addr.Protocols() {
				// search the first dns address
				if protocol.Name == "dns" {
					return addr.String()
				}
			}
		}

		return fallback
	}

	status := &NodeInfoExtended{
		Version:       deps.AppInfo.Version,
		LatestVersion: deps.AppInfo.LatestGitHubVersion,
		Uptime:        time.Since(nodeStartupTimestamp).Milliseconds(),
		NodeID:        deps.Host.ID().String(),
		MultiAddress:  getExternalMultiAddr(),
		Alias:         ParamsNode.Alias,
		MemoryUsage:   int64(m.HeapAlloc + m.StackSys + m.MSpanSys + m.MCacheSys + m.BuckHashSys + m.GCSys + m.OtherSys),
	}

	return status
}
