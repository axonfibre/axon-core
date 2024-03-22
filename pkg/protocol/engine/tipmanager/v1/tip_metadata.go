package tipmanagerv1

import (
	"fmt"

	"github.com/iotaledger/hive.go/ds/reactive"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/log"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/tipmanager"
	iotago "github.com/iotaledger/iota.go/v4"
)

// TipMetadata represents the metadata for a block in the TipManager.
type TipMetadata struct {
	// block holds the block that the metadata belongs to.
	block *blocks.Block

	// tipPool holds the tip pool the block is currently assigned to.
	tipPool reactive.Variable[tipmanager.TipPool]

	// livenessThresholdReached is an event that is triggered when the liveness threshold is reached.
	livenessThresholdReached reactive.Event

	// evicted is an event that is triggered when the block is evicted.
	evicted reactive.Event

	// isStrongTipPoolMember is true if the block is part of the strong tip pool and not orphaned.
	isStrongTipPoolMember reactive.Variable[bool]

	// isWeakTipPoolMember is true if the block is part of the weak tip pool and not orphaned.
	isWeakTipPoolMember reactive.Variable[bool]

	// isStronglyConnectedToTips is true if the block is either strongly referenced by others tips or is itself a strong
	// tip pool member.
	isStronglyConnectedToTips reactive.Variable[bool]

	// isConnectedToTips is true if the block is either referenced by others tips or is itself a weak or strong tip pool
	// member.
	isConnectedToTips reactive.Variable[bool]

	// isStronglyReferencedByTips is true if the block has at least one strong child that is strongly connected
	// to the tips.
	isStronglyReferencedByTips reactive.Variable[bool]

	// isWeaklyReferencedByTips is true if the block has at least one weak child that is connected to the tips.
	isWeaklyReferencedByTips reactive.Variable[bool]

	// isReferencedByTips is true if the block is strongly referenced by other tips or has at least one weak child
	// that is connected to the tips.
	isReferencedByTips reactive.Variable[bool]

	// isLatestValidationBlock is true if the block is the latest block of a validator.
	isLatestValidationBlock reactive.Variable[bool]

	// referencesLatestValidationBlock is true if the block is the latest validator block or has parents that reference
	// the latest validator block.
	referencesLatestValidationBlock reactive.Variable[bool]

	// isStrongTip is true if the block is a strong tip pool member and is not strongly referenced by other tips.
	isStrongTip reactive.Variable[bool]

	// isWeakTip is true if the block is a weak tip pool member and is not referenced by other tips.
	isWeakTip reactive.Variable[bool]

	// isValidationTip is true if the block is a strong tip and references the latest validator block.
	isValidationTip reactive.Variable[bool]

	// isMarkedOrphaned is true if the liveness threshold has been reached and the block was not accepted.
	isMarkedOrphaned reactive.Variable[bool]

	// isOrphaned is true if the block is either strongly or weakly orphaned.
	isOrphaned reactive.Variable[bool]

	// anyStrongParentStronglyOrphaned is true if the block has at least one strong parent that is strongly orphaned.
	anyStrongParentStronglyOrphaned reactive.Variable[bool]

	// anyWeakParentWeaklyOrphaned is true if the block has at least one weak parent that is weakly orphaned.
	anyWeakParentWeaklyOrphaned reactive.Variable[bool]

	// isStronglyOrphaned is true if the block is either marked as orphaned, any of its strong parents is strongly
	// orphaned or any of its weak parents is weakly orphaned.
	isStronglyOrphaned reactive.Variable[bool]

	// isWeaklyOrphaned is true if the block is either marked as orphaned or has at least one weakly orphaned weak
	// parent.
	isWeaklyOrphaned reactive.Variable[bool]

	// stronglyConnectedStrongChildren holds the number of strong children that are strongly connected to the tips.
	stronglyConnectedStrongChildren reactive.Counter[bool]

	// connectedWeakChildren holds the number of weak children that are connected to the tips.
	connectedWeakChildren reactive.Counter[bool]

	// stronglyOrphanedStrongParents holds the number of strong parents that are strongly orphaned.
	stronglyOrphanedStrongParents reactive.Counter[bool]

	// weaklyOrphanedWeakParents holds the number of weak parents that are weakly orphaned.
	weaklyOrphanedWeakParents reactive.Counter[bool]

	// parentsReferencingLatestValidationBlock holds the number of parents that reference the latest validator block.
	parentsReferencingLatestValidationBlock reactive.Counter[bool]

	log.Logger
}

// NewBlockMetadata creates a new TipMetadata instance.
func NewBlockMetadata(block *blocks.Block, logger log.Logger) *TipMetadata {
	t := &TipMetadata{
		block:                                   block,
		tipPool:                                 reactive.NewVariable[tipmanager.TipPool](tipmanager.TipPool.Max),
		livenessThresholdReached:                reactive.NewEvent(),
		evicted:                                 reactive.NewEvent(),
		isLatestValidationBlock:                 reactive.NewVariable[bool](),
		stronglyConnectedStrongChildren:         reactive.NewCounter[bool](),
		connectedWeakChildren:                   reactive.NewCounter[bool](),
		stronglyOrphanedStrongParents:           reactive.NewCounter[bool](),
		weaklyOrphanedWeakParents:               reactive.NewCounter[bool](),
		parentsReferencingLatestValidationBlock: reactive.NewCounter[bool](),
		Logger:                                  logger,
	}

	t.referencesLatestValidationBlock = reactive.NewDerivedVariable2(func(_ bool, isLatestValidationBlock bool, parentsReferencingLatestValidationBlock int) bool {
		return isLatestValidationBlock || parentsReferencingLatestValidationBlock > 0
	}, t.isLatestValidationBlock, t.parentsReferencingLatestValidationBlock)

	t.isMarkedOrphaned = reactive.NewDerivedVariable2[bool, bool](func(_ bool, isLivenessThresholdReached bool, isPreAccepted bool) bool {
		return isLivenessThresholdReached && !isPreAccepted
	}, t.livenessThresholdReached, block.PreAccepted())

	t.anyStrongParentStronglyOrphaned = reactive.NewDerivedVariable[bool, int](func(_ bool, stronglyOrphanedStrongParents int) bool {
		return stronglyOrphanedStrongParents > 0
	}, t.stronglyOrphanedStrongParents)

	t.anyWeakParentWeaklyOrphaned = reactive.NewDerivedVariable[bool, int](func(_ bool, weaklyOrphanedWeakParents int) bool {
		return weaklyOrphanedWeakParents > 0
	}, t.weaklyOrphanedWeakParents)

	t.isStronglyOrphaned = reactive.NewDerivedVariable3[bool, bool, bool, bool](func(_ bool, isMarkedOrphaned bool, anyStrongParentStronglyOrphaned bool, anyWeakParentWeaklyOrphaned bool) bool {
		return isMarkedOrphaned || anyStrongParentStronglyOrphaned || anyWeakParentWeaklyOrphaned
	}, t.isMarkedOrphaned, t.anyStrongParentStronglyOrphaned, t.anyWeakParentWeaklyOrphaned)

	t.isWeaklyOrphaned = reactive.NewDerivedVariable2[bool, bool, bool](func(_ bool, isMarkedOrphaned bool, anyWeakParentWeaklyOrphaned bool) bool {
		return isMarkedOrphaned || anyWeakParentWeaklyOrphaned
	}, t.isMarkedOrphaned, t.anyWeakParentWeaklyOrphaned)

	t.isOrphaned = reactive.NewDerivedVariable2[bool, bool, bool](func(_ bool, isStronglyOrphaned bool, isWeaklyOrphaned bool) bool {
		return isStronglyOrphaned || isWeaklyOrphaned
	}, t.isStronglyOrphaned, t.isWeaklyOrphaned)

	t.isStrongTipPoolMember = reactive.NewDerivedVariable3(func(_ bool, tipPool tipmanager.TipPool, isOrphaned bool, isEvicted bool) bool {
		return tipPool == tipmanager.StrongTipPool && !isOrphaned && !isEvicted
	}, t.tipPool, t.isOrphaned, t.evicted)

	t.isWeakTipPoolMember = reactive.NewDerivedVariable3(func(_ bool, tipPool tipmanager.TipPool, isOrphaned bool, isEvicted bool) bool {
		return tipPool == tipmanager.WeakTipPool && !isOrphaned && !isEvicted
	}, t.tipPool, t.isOrphaned, t.evicted)

	t.isStronglyReferencedByTips = reactive.NewDerivedVariable[bool, int](func(_ bool, stronglyConnectedStrongChildren int) bool {
		return stronglyConnectedStrongChildren > 0
	}, t.stronglyConnectedStrongChildren)

	t.isWeaklyReferencedByTips = reactive.NewDerivedVariable[bool, int](func(_ bool, connectedWeakChildren int) bool {
		return connectedWeakChildren > 0
	}, t.connectedWeakChildren)

	t.isReferencedByTips = reactive.NewDerivedVariable2[bool, bool, bool](func(_ bool, isWeaklyReferencedByTips bool, isStronglyReferencedByTips bool) bool {
		return isWeaklyReferencedByTips || isStronglyReferencedByTips
	}, t.isWeaklyReferencedByTips, t.isStronglyReferencedByTips)

	t.isStronglyConnectedToTips = reactive.NewDerivedVariable2(func(_ bool, isStrongTipPoolMember bool, isStronglyReferencedByTips bool) bool {
		return isStrongTipPoolMember || isStronglyReferencedByTips
	}, t.isStrongTipPoolMember, t.isStronglyReferencedByTips)

	t.isConnectedToTips = reactive.NewDerivedVariable3(func(_ bool, isReferencedByTips bool, isStrongTipPoolMember bool, isWeakTipPoolMember bool) bool {
		return isReferencedByTips || isStrongTipPoolMember || isWeakTipPoolMember
	}, t.isReferencedByTips, t.isStrongTipPoolMember, t.isWeakTipPoolMember)

	t.isStrongTip = reactive.NewDerivedVariable2[bool, bool, bool](func(_ bool, isStrongTipPoolMember bool, isStronglyReferencedByTips bool) bool {
		return isStrongTipPoolMember && !isStronglyReferencedByTips
	}, t.isStrongTipPoolMember, t.isStronglyReferencedByTips)

	t.isWeakTip = reactive.NewDerivedVariable2[bool, bool, bool](func(_ bool, isWeakTipPoolMember bool, isReferencedByTips bool) bool {
		return isWeakTipPoolMember && !isReferencedByTips
	}, t.isWeakTipPoolMember, t.isReferencedByTips)

	t.isValidationTip = reactive.NewDerivedVariable2(func(_ bool, isStrongTip bool, referencesLatestValidationBlock bool) bool {
		return isStrongTip && referencesLatestValidationBlock
	}, t.isStrongTip, t.referencesLatestValidationBlock)

	unsubscribeLogs := lo.BatchReverse(
		t.tipPool.LogUpdates(logger, log.LevelInfo, "TipPool"),
		t.livenessThresholdReached.LogUpdates(logger, log.LevelInfo, "LivenessThresholdReached"),
		t.evicted.LogUpdates(logger, log.LevelInfo, "Evicted"),
		t.isStrongTipPoolMember.LogUpdates(logger, log.LevelInfo, "IsStrongTipPoolMember"),
		t.isWeakTipPoolMember.LogUpdates(logger, log.LevelInfo, "IsWeakTipPoolMember"),
		t.isStronglyConnectedToTips.LogUpdates(logger, log.LevelInfo, "IsStronglyConnectedToTips"),
		t.isConnectedToTips.LogUpdates(logger, log.LevelInfo, "IsConnectedToTips"),
		t.isStronglyReferencedByTips.LogUpdates(logger, log.LevelInfo, "IsStronglyReferencedByTips"),
		t.isWeaklyReferencedByTips.LogUpdates(logger, log.LevelInfo, "IsWeaklyReferencedByTips"),
		t.isReferencedByTips.LogUpdates(logger, log.LevelInfo, "IsReferencedByTips"),
		t.isLatestValidationBlock.LogUpdates(logger, log.LevelInfo, "IsLatestValidationBlock"),
		t.referencesLatestValidationBlock.LogUpdates(logger, log.LevelInfo, "ReferencesLatestValidationBlock"),
		t.isStrongTip.LogUpdates(logger, log.LevelInfo, "IsStrongTip"),
		t.isWeakTip.LogUpdates(logger, log.LevelInfo, "IsWeakTip"),
		t.isValidationTip.LogUpdates(logger, log.LevelInfo, "IsValidationTip"),
		t.isMarkedOrphaned.LogUpdates(logger, log.LevelInfo, "IsMarkedOrphaned"),
		t.isOrphaned.LogUpdates(logger, log.LevelInfo, "IsOrphaned"),
		t.anyStrongParentStronglyOrphaned.LogUpdates(logger, log.LevelInfo, "AnyStrongParentStronglyOrphaned"),
		t.anyWeakParentWeaklyOrphaned.LogUpdates(logger, log.LevelInfo, "AnyWeakParentWeaklyOrphaned"),
		t.isStronglyOrphaned.LogUpdates(logger, log.LevelInfo, "IsStronglyOrphaned"),
		t.isWeaklyOrphaned.LogUpdates(logger, log.LevelInfo, "IsWeaklyOrphaned"),
		t.stronglyConnectedStrongChildren.LogUpdates(logger, log.LevelInfo, "StronglyConnectedStrongChildren"),
		t.connectedWeakChildren.LogUpdates(logger, log.LevelInfo, "ConnectedWeakChildren"),
		t.stronglyOrphanedStrongParents.LogUpdates(logger, log.LevelInfo, "StronglyOrphanedStrongParents"),
		t.weaklyOrphanedWeakParents.LogUpdates(logger, log.LevelInfo, "WeaklyOrphanedWeakParents"),
		t.parentsReferencingLatestValidationBlock.LogUpdates(logger, log.LevelInfo, "ParentsReferencingLatestValidationBlock"),
	)

	t.evicted.OnUpdate(func(_, newValue bool) {
		if newValue {
			unsubscribeLogs()
		}
	})

	return t
}

// ID returns the identifier of the block the TipMetadata belongs to.
func (t *TipMetadata) ID() iotago.BlockID {
	return t.block.ID()
}

// Block returns the block that the TipMetadata belongs to.
func (t *TipMetadata) Block() *blocks.Block {
	return t.block
}

// TipPool exposes a variable that stores the current TipPool of the block.
func (t *TipMetadata) TipPool() reactive.Variable[tipmanager.TipPool] {
	return t.tipPool
}

// LivenessThresholdReached exposes an event that is triggered when the liveness threshold is reached.
func (t *TipMetadata) LivenessThresholdReached() reactive.Event {
	return t.livenessThresholdReached
}

// IsStrongTip returns a ReadableVariable that indicates if the block is a strong tip.
func (t *TipMetadata) IsStrongTip() reactive.ReadableVariable[bool] {
	return t.isStrongTip
}

// IsWeakTip returns a ReadableVariable that indicates if the block is a weak tip.
func (t *TipMetadata) IsWeakTip() reactive.ReadableVariable[bool] {
	return t.isWeakTip
}

// IsOrphaned returns a ReadableVariable that indicates if the block was orphaned.
func (t *TipMetadata) IsOrphaned() reactive.ReadableVariable[bool] {
	return t.isOrphaned
}

// Evicted exposes an event that is triggered when the block is evicted.
func (t *TipMetadata) Evicted() reactive.Event {
	return t.evicted
}

// registerAsLatestValidationBlock registers the TipMetadata as the latest validation block if it is newer than the
// currently registered block and sets the isLatestValidationBlock variable accordingly. The function returns true if the
// operation was successful.
func (t *TipMetadata) registerAsLatestValidationBlock(latestValidationBlock reactive.Variable[*TipMetadata]) (registered bool) {
	latestValidationBlock.Compute(func(currentLatestValidationBlock *TipMetadata) *TipMetadata {
		registered = currentLatestValidationBlock == nil || currentLatestValidationBlock.block.IssuingTime().Before(t.block.IssuingTime())

		return lo.Cond(registered, t, currentLatestValidationBlock)
	})

	if registered {
		t.isLatestValidationBlock.Set(true)

		// Once the latestValidationBlock is updated again (by another block), we need to reset the isLatestValidationBlock
		// variable.
		latestValidationBlock.OnUpdateOnce(func(_ *TipMetadata, _ *TipMetadata) {
			t.isLatestValidationBlock.Set(false)
		}, func(_ *TipMetadata, latestValidationBlock *TipMetadata) bool {
			return latestValidationBlock != t
		})
	}

	return registered
}

// connectStrongParent sets up the parent and children related properties for a strong parent.
func (t *TipMetadata) connectStrongParent(strongParent *TipMetadata) {
	t.stronglyOrphanedStrongParents.Monitor(strongParent.isStronglyOrphaned)
	t.parentsReferencingLatestValidationBlock.Monitor(strongParent.referencesLatestValidationBlock)

	// unsubscribe when the parent is evicted, since we otherwise continue to hold a reference to it.
	unsubscribe := strongParent.stronglyConnectedStrongChildren.Monitor(t.isStronglyConnectedToTips)
	strongParent.evicted.OnUpdate(func(_ bool, _ bool) { unsubscribe() })
}

// connectWeakParent sets up the parent and children related properties for a weak parent.
func (t *TipMetadata) connectWeakParent(weakParent *TipMetadata) {
	t.weaklyOrphanedWeakParents.Monitor(weakParent.isWeaklyOrphaned)

	// unsubscribe when the parent is evicted, since we otherwise continue to hold a reference to it.
	unsubscribe := weakParent.connectedWeakChildren.Monitor(t.isConnectedToTips)
	weakParent.evicted.OnUpdate(func(_ bool, _ bool) { unsubscribe() })
}

// String returns a human-readable representation of the TipMetadata.
func (t *TipMetadata) String() string {
	return fmt.Sprintf(
		"TipMetadata: [\n"+
			"Block: %s\n"+
			"TipPool: %d\n"+
			"IsStrongTipPoolMember: %v\n"+
			"IsWeakTipPoolMember: %v\n"+
			"IsStronglyConnectedToTips: %v\n"+
			"IsConnectedToTips: %v\n"+
			"IsStronglyReferencedByTips: %v\n"+
			"IsWeaklyReferencedByTips: %v\n"+
			"IsReferencedByTips: %v\n"+
			"IsStrongTip: %v\n"+
			"IsWeakTip: %v\n"+
			"IsMarkedOrphaned: %v\n"+
			"IsOrphaned: %v\n"+
			"AnyStrongParentStronglyOrphaned: %v\n"+
			"AnyWeakParentWeaklyOrphaned: %v\n"+
			"IsStronglyOrphaned: %v\n"+
			"IsWeaklyOrphaned: %v\n"+
			"StronglyConnectedStrongChildren: %d\n"+
			"ConnectedWeakChildren: %d\n"+
			"StronglyOrphanedStrongParents: %d\n"+
			"WeaklyOrphanedWeakParents: %d\n"+
			"]",
		t.block,
		t.tipPool.Get(),
		t.isStrongTipPoolMember.Get(),
		t.isWeakTipPoolMember.Get(),
		t.isStronglyConnectedToTips.Get(),
		t.isConnectedToTips.Get(),
		t.isStronglyReferencedByTips.Get(),
		t.isWeaklyReferencedByTips.Get(),
		t.isReferencedByTips.Get(),
		t.isStrongTip.Get(),
		t.isWeakTip.Get(),
		t.isMarkedOrphaned.Get(),
		t.isOrphaned.Get(),
		t.anyStrongParentStronglyOrphaned.Get(),
		t.anyWeakParentWeaklyOrphaned.Get(),
		t.isStronglyOrphaned.Get(),
		t.isWeaklyOrphaned.Get(),
		t.stronglyConnectedStrongChildren.Get(),
		t.connectedWeakChildren.Get(),
		t.stronglyOrphanedStrongParents.Get(),
		t.weaklyOrphanedWeakParents.Get(),
	)
}

// code contract (make sure the type implements all required methods).
var _ tipmanager.TipMetadata = new(TipMetadata)
