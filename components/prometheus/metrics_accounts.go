package prometheus

import (
	"github.com/iotaledger/iota-core/components/prometheus/collector"
)

const (
	accountNamespace = "account"

	activeSeats = "active_seats"
)

var AccountMetrics = collector.NewCollection(accountNamespace,
	collector.WithMetric(collector.NewMetric(activeSeats,
		collector.WithType(collector.Gauge),
		collector.WithHelp("Seats seen as active by the node."),
		collector.WithCollectFunc(func() (metricValue float64, labelValues []string) {
			return float64(deps.Protocol.Engines.Main.Get().SybilProtection.SeatManager().OnlineCommittee().Size()), nil
		}),
	)),
)
