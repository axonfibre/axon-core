package prometheus

import (
	"strconv"
	"time"

	"github.com/iotaledger/hive.go/runtime/event"
	"github.com/iotaledger/iota-core/components/prometheus/collector"
	"github.com/iotaledger/iota-core/pkg/protocol"
	"github.com/iotaledger/iota-core/pkg/protocol/engine"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/notarization"
	iotago "github.com/iotaledger/iota.go/v4"
)

const (
	commitmentsNamespace = "commitments"

	latestCommitment    = "latest"
	finalizedCommitment = "finalized"
	forksCount          = "forks_total"
	acceptedBlocks      = "accepted_blocks"
	transactions        = "accepted_transactions"
	validators          = "active_validators"
	activeChainsCount   = "active_chains_count"
	seenChainsCount     = "seen_chains_count"
	spawnedEnginesCount = "spawned_engines_count"
)

var CommitmentsMetrics = collector.NewCollection(commitmentsNamespace,
	collector.WithMetric(collector.NewMetric(latestCommitment,
		collector.WithType(collector.Gauge),
		collector.WithHelp("Last commitment of the node."),
		collector.WithLabels("commitment"),
		collector.WithPruningDelay(10*time.Minute),
		collector.WithInitFunc(func() {
			deps.Protocol.Events.Engine.Notarization.SlotCommitted.Hook(func(details *notarization.SlotCommittedDetails) {
				deps.Collector.Update(commitmentsNamespace, latestCommitment, float64(details.Commitment.ID().Slot()), "C "+details.Commitment.ID().ToHex())
			}, event.WithWorkerPool(Component.WorkerPool))
		}),
	)),
	collector.WithMetric(collector.NewMetric(finalizedCommitment,
		collector.WithType(collector.Gauge),
		collector.WithHelp("Last commitment finalized by the node."),
		collector.WithInitFunc(func() {
			deps.Protocol.Events.Engine.SlotGadget.SlotFinalized.Hook(func(slot iotago.SlotIndex) {
				deps.Collector.Update(commitmentsNamespace, finalizedCommitment, float64(slot))
			}, event.WithWorkerPool(Component.WorkerPool))
		}),
	)),
	collector.WithMetric(collector.NewMetric(forksCount,
		collector.WithType(collector.Counter),
		collector.WithHelp("Number of forks seen by the node."),
		collector.WithInitFunc(func() {
			deps.Protocol.Chains.HeaviestVerifiedCandidate.OnUpdate(func(prevHeaviestVerifiedCandidate *protocol.Chain, _ *protocol.Chain) {
				if prevHeaviestVerifiedCandidate != nil {
					Component.WorkerPool.Submit(func() { deps.Collector.Increment(commitmentsNamespace, forksCount) })
				}
			})
		}),
	)),
	collector.WithMetric(collector.NewMetric(spawnedEnginesCount,
		collector.WithType(collector.Counter),
		collector.WithHelp("Number spawned engines since the node started."),
		collector.WithInitFunc(func() {
			deps.Protocol.Chains.WithInitializedEngines(func(_ *protocol.Chain, _ *engine.Engine) (shutdown func()) {
				Component.WorkerPool.Submit(func() { deps.Collector.Increment(commitmentsNamespace, spawnedEnginesCount) })

				return nil
			})
		}),
	)),
	collector.WithMetric(collector.NewMetric(seenChainsCount,
		collector.WithType(collector.Counter),
		collector.WithHelp("Number chains seen since the node started."),
		collector.WithInitFunc(func() {
			deps.Protocol.Chains.WithElements(func(_ *protocol.Chain) (teardown func()) {
				Component.WorkerPool.Submit(func() { deps.Collector.Increment(commitmentsNamespace, seenChainsCount) })

				return nil
			})
		}),
	)),
	collector.WithMetric(collector.NewMetric(activeChainsCount,
		collector.WithType(collector.Gauge),
		collector.WithHelp("Number currently active chains."),
		collector.WithCollectFunc(func() (metricValue float64, labelValues []string) {
			return float64(deps.Protocol.Chains.Size()), nil
		}),
	)),
	collector.WithMetric(collector.NewMetric(acceptedBlocks,
		collector.WithType(collector.Gauge),
		collector.WithHelp("Number of accepted blocks by the node per slot."),
		collector.WithLabels("slot"),
		collector.WithPruningDelay(10*time.Minute),
		collector.WithInitFunc(func() {
			deps.Protocol.Events.Engine.Notarization.SlotCommitted.Hook(func(details *notarization.SlotCommittedDetails) {
				deps.Collector.Update(commitmentsNamespace, acceptedBlocks, float64(details.AcceptedBlocks.Size()), strconv.Itoa(int(details.Commitment.Slot())))
			}, event.WithWorkerPool(Component.WorkerPool))
		}),
	)),
	collector.WithMetric(collector.NewMetric(transactions,
		collector.WithType(collector.Gauge),
		collector.WithHelp("Number of accepted transactions by the node per slot."),
		collector.WithLabels("slot"),
		collector.WithPruningDelay(10*time.Minute),
		collector.WithInitFunc(func() {
			deps.Protocol.Events.Engine.Notarization.SlotCommitted.Hook(func(details *notarization.SlotCommittedDetails) {
				deps.Collector.Update(commitmentsNamespace, transactions, float64(len(details.Mutations)), strconv.Itoa(int(details.Commitment.Slot())))
			}, event.WithWorkerPool(Component.WorkerPool))
		}),
	)),
	collector.WithMetric(collector.NewMetric(validators,
		collector.WithType(collector.Gauge),
		collector.WithHelp("Number of active validators per slot."),
		collector.WithLabels("slot"),
		collector.WithPruningDelay(10*time.Minute),
		collector.WithInitFunc(func() {
			deps.Protocol.Events.Engine.Notarization.SlotCommitted.Hook(func(details *notarization.SlotCommittedDetails) {
				deps.Collector.Update(commitmentsNamespace, validators, float64(details.ActiveValidatorsCount), strconv.Itoa(int(details.Commitment.Slot())))
			}, event.WithWorkerPool(Component.WorkerPool))
		}),
	)),
)
