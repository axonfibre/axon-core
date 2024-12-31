package prometheus

import (
	"time"

	"github.com/iotaledger/hive.go/runtime/event"
	"github.com/iotaledger/iota-core/components/prometheus/collector"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
)

const (
	schedulerNamespace = "scheduler"

	queueSizePerNodeWork           = "queue_size_per_node_work"
	queueSizePerNodeCount          = "queue_size_per_node_count"
	validatorQueueSizePerNodeCount = "validator_queue_size_per_node_count"
	schedulerProcessedBlocks       = "processed_blocks"
	scheduledBlockLabel            = "scheduled"
	skippedBlockLabel              = "skipped"
	droppedBlockLabel              = "dropped"
	enqueuedBlockLabel             = "enqueued"
	basicBufferReadyBlockCount     = "buffer_ready_block_total"
	basicBufferTotalSize           = "buffer_size_block_total"
	basicBufferMaxSize             = "buffer_max_size"
	rate                           = "rate"
	validatorBufferTotalSize       = "validator_buffer_size_block_total"
	validatorQueueMaxSize          = "validator_buffer_max_size"
)

var SchedulerMetrics = collector.NewCollection(schedulerNamespace,
	collector.WithMetric(collector.NewMetric(queueSizePerNodeWork,
		collector.WithType(collector.Gauge),
		collector.WithLabels("issuer_id"),
		collector.WithPruningDelay(10*time.Minute),
		collector.WithHelp("Current size of each node's queue (in work units)."),
		collector.WithInitFunc(func() {
			deps.Protocol.Events.Engine.Scheduler.BlockEnqueued.Hook(func(block *blocks.Block) {
				deps.Collector.Update(schedulerNamespace, queueSizePerNodeWork, float64(deps.Protocol.Engines.Main.Get().Scheduler.IssuerQueueWork(block.IssuerID())), block.IssuerID().String())

			}, event.WithWorkerPool(Component.WorkerPool))

			deps.Protocol.Events.Engine.Scheduler.BlockSkipped.Hook(func(block *blocks.Block) {
				deps.Collector.Update(schedulerNamespace, queueSizePerNodeWork, float64(deps.Protocol.Engines.Main.Get().Scheduler.IssuerQueueWork(block.IssuerID())), block.IssuerID().String())
			}, event.WithWorkerPool(Component.WorkerPool))

			deps.Protocol.Events.Engine.Scheduler.BlockDropped.Hook(func(block *blocks.Block, _ error) {
				deps.Collector.Update(schedulerNamespace, queueSizePerNodeWork, float64(deps.Protocol.Engines.Main.Get().Scheduler.IssuerQueueWork(block.IssuerID())), block.IssuerID().String())
			}, event.WithWorkerPool(Component.WorkerPool))

			deps.Protocol.Events.Engine.Scheduler.BlockScheduled.Hook(func(block *blocks.Block) {
				deps.Collector.Update(schedulerNamespace, queueSizePerNodeWork, float64(deps.Protocol.Engines.Main.Get().Scheduler.IssuerQueueWork(block.IssuerID())), block.IssuerID().String())
			}, event.WithWorkerPool(Component.WorkerPool))
		}),
	)),
	collector.WithMetric(collector.NewMetric(queueSizePerNodeCount,
		collector.WithType(collector.Gauge),
		collector.WithLabels("issuer_id"),
		collector.WithPruningDelay(10*time.Minute),
		collector.WithHelp("Current size of each node's queue (as block count)."),
		collector.WithInitFunc(func() {
			deps.Protocol.Events.Engine.Scheduler.BlockEnqueued.Hook(func(block *blocks.Block) {
				if block.IsBasicBlock() {
					deps.Collector.Update(schedulerNamespace, queueSizePerNodeCount, float64(deps.Protocol.Engines.Main.Get().Scheduler.IssuerQueueBlockCount(block.IssuerID())), block.IssuerID().String())
				}
			}, event.WithWorkerPool(Component.WorkerPool))

			deps.Protocol.Events.Engine.Scheduler.BlockSkipped.Hook(func(block *blocks.Block) {
				if block.IsBasicBlock() {
					deps.Collector.Update(schedulerNamespace, queueSizePerNodeCount, float64(deps.Protocol.Engines.Main.Get().Scheduler.IssuerQueueBlockCount(block.IssuerID())), block.IssuerID().String())
				}
			}, event.WithWorkerPool(Component.WorkerPool))

			deps.Protocol.Events.Engine.Scheduler.BlockDropped.Hook(func(block *blocks.Block, _ error) {
				if block.IsBasicBlock() {
					deps.Collector.Update(schedulerNamespace, queueSizePerNodeCount, float64(deps.Protocol.Engines.Main.Get().Scheduler.IssuerQueueBlockCount(block.IssuerID())), block.IssuerID().String())
				}
			}, event.WithWorkerPool(Component.WorkerPool))

			deps.Protocol.Events.Engine.Scheduler.BlockScheduled.Hook(func(block *blocks.Block) {
				if block.IsBasicBlock() {
					deps.Collector.Update(schedulerNamespace, queueSizePerNodeCount, float64(deps.Protocol.Engines.Main.Get().Scheduler.IssuerQueueBlockCount(block.IssuerID())), block.IssuerID().String())
				}
			}, event.WithWorkerPool(Component.WorkerPool))
		}),
	)),
	collector.WithMetric(collector.NewMetric(validatorQueueSizePerNodeCount,
		collector.WithType(collector.Gauge),
		collector.WithLabels("issuer_id"),
		collector.WithPruningDelay(10*time.Minute),
		collector.WithHelp("Current number of validation blocks in each validator's queue."),
		collector.WithInitFunc(func() {
			deps.Protocol.Events.Engine.Scheduler.BlockEnqueued.Hook(func(block *blocks.Block) {
				if block.IsValidationBlock() {
					deps.Collector.Update(schedulerNamespace, validatorQueueSizePerNodeCount, float64(deps.Protocol.Engines.Main.Get().Scheduler.ValidatorQueueBlockCount(block.IssuerID())), block.IssuerID().String())
				}
			}, event.WithWorkerPool(Component.WorkerPool))

			deps.Protocol.Events.Engine.Scheduler.BlockSkipped.Hook(func(block *blocks.Block) {
				if block.IsValidationBlock() {
					deps.Collector.Update(schedulerNamespace, validatorQueueSizePerNodeCount, float64(deps.Protocol.Engines.Main.Get().Scheduler.ValidatorQueueBlockCount(block.IssuerID())), block.IssuerID().String())
				}
			}, event.WithWorkerPool(Component.WorkerPool))

			deps.Protocol.Events.Engine.Scheduler.BlockDropped.Hook(func(block *blocks.Block, _ error) {
				if block.IsValidationBlock() {
					deps.Collector.Update(schedulerNamespace, validatorQueueSizePerNodeCount, float64(deps.Protocol.Engines.Main.Get().Scheduler.ValidatorQueueBlockCount(block.IssuerID())), block.IssuerID().String())
				}
			}, event.WithWorkerPool(Component.WorkerPool))

			deps.Protocol.Events.Engine.Scheduler.BlockScheduled.Hook(func(block *blocks.Block) {
				if block.IsValidationBlock() {
					deps.Collector.Update(schedulerNamespace, validatorQueueSizePerNodeCount, float64(deps.Protocol.Engines.Main.Get().Scheduler.ValidatorQueueBlockCount(block.IssuerID())), block.IssuerID().String())
				}
			}, event.WithWorkerPool(Component.WorkerPool))
		}),
	)),
	collector.WithMetric(collector.NewMetric(schedulerProcessedBlocks,
		collector.WithType(collector.Counter),
		collector.WithLabels("state"),
		collector.WithHelp("Number of blocks processed by the scheduler."),
		collector.WithInitFunc(func() {
			deps.Protocol.Events.Engine.Scheduler.BlockEnqueued.Hook(func(_ *blocks.Block) {
				deps.Collector.Increment(schedulerNamespace, schedulerProcessedBlocks, enqueuedBlockLabel)
			}, event.WithWorkerPool(Component.WorkerPool))

			deps.Protocol.Events.Engine.Scheduler.BlockDropped.Hook(func(_ *blocks.Block, _ error) {
				deps.Collector.Increment(schedulerNamespace, schedulerProcessedBlocks, droppedBlockLabel)
			}, event.WithWorkerPool(Component.WorkerPool))

			deps.Protocol.Events.Engine.Scheduler.BlockSkipped.Hook(func(_ *blocks.Block) {
				deps.Collector.Increment(schedulerNamespace, schedulerProcessedBlocks, skippedBlockLabel)
			}, event.WithWorkerPool(Component.WorkerPool))

			deps.Protocol.Events.Engine.Scheduler.BlockScheduled.Hook(func(_ *blocks.Block) {
				deps.Collector.Increment(schedulerNamespace, schedulerProcessedBlocks, scheduledBlockLabel)
			}, event.WithWorkerPool(Component.WorkerPool))
		}),
	)),
	collector.WithMetric(collector.NewMetric(basicBufferMaxSize,
		collector.WithType(collector.Gauge),
		collector.WithHelp("Maximum number of basic blocks that can be stored in the buffer."),
		collector.WithCollectFunc(func() (float64, []string) {
			return float64(deps.Protocol.Engines.Main.Get().CommittedAPI().ProtocolParameters().CongestionControlParameters().MaxBufferSize), []string{}
		}),
	)),
	collector.WithMetric(collector.NewMetric(basicBufferReadyBlockCount,
		collector.WithType(collector.Gauge),
		collector.WithHelp("Number of ready blocks in the scheduler buffer."),
		collector.WithCollectFunc(func() (float64, []string) {
			return float64(deps.Protocol.Engines.Main.Get().Scheduler.ReadyBlocksCount()), []string{}
		}),
	)),
	collector.WithMetric(collector.NewMetric(basicBufferTotalSize,
		collector.WithType(collector.Gauge),
		collector.WithHelp("Current number of basic blocks in the scheduler buffer."),
		collector.WithCollectFunc(func() (float64, []string) {
			return float64(deps.Protocol.Engines.Main.Get().Scheduler.BasicBufferSize()), []string{}
		}),
	)),
	collector.WithMetric(collector.NewMetric(rate,
		collector.WithType(collector.Gauge),
		collector.WithHelp("Current scheduling rate of basic blocks."),
		collector.WithCollectFunc(func() (float64, []string) {
			return float64(deps.Protocol.Engines.Main.Get().CommittedAPI().ProtocolParameters().CongestionControlParameters().SchedulerRate), []string{}
		}),
	)),
	collector.WithMetric(collector.NewMetric(validatorBufferTotalSize,
		collector.WithType(collector.Gauge),
		collector.WithHelp("Current number of validation blocks in the scheduling buffer."),
		collector.WithCollectFunc(func() (float64, []string) {
			return float64(deps.Protocol.Engines.Main.Get().Scheduler.ValidatorBufferSize()), []string{}
		}),
	)),
	collector.WithMetric(collector.NewMetric(validatorQueueMaxSize,
		collector.WithType(collector.Gauge),
		collector.WithHelp("Maximum number of validation blocks that can be stored in each validator queue."),
		collector.WithCollectFunc(func() (float64, []string) {
			return float64(deps.Protocol.Engines.Main.Get().CommittedAPI().ProtocolParameters().CongestionControlParameters().MaxValidationBufferSize), []string{}
		}),
	)),
)
