package performance

import (
	"github.com/iotaledger/hive.go/ds"
	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/kvstore"
	"github.com/iotaledger/hive.go/runtime/syncutils"
	"github.com/iotaledger/iota-core/pkg/core/account"
	"github.com/iotaledger/iota-core/pkg/model"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
	"github.com/iotaledger/iota-core/pkg/storage/prunable/epochstore"
	"github.com/iotaledger/iota-core/pkg/storage/prunable/slotstore"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/api"
)

type Tracker struct {
	rewardsStorePerEpochFunc func(epoch iotago.EpochIndex) kvstore.KVStore
	poolStatsStore           *epochstore.Store[*model.PoolsStats]
	committeeStore           *epochstore.Store[*account.Accounts]

	performanceFactorsFunc func(slot iotago.SlotIndex) *slotstore.Store[iotago.AccountID, uint64]

	apiProvider api.Provider

	performanceFactorsMutex syncutils.RWMutex
	mutex                   syncutils.RWMutex
}

func NewTracker(
	rewardsStorePerEpochFunc func(epoch iotago.EpochIndex) kvstore.KVStore,
	poolStatsStore *epochstore.Store[*model.PoolsStats],
	committeeStore *epochstore.Store[*account.Accounts],
	performanceFactorsFunc func(slot iotago.SlotIndex) *slotstore.Store[iotago.AccountID, uint64],
	apiProvider api.Provider,
) *Tracker {
	return &Tracker{
		rewardsStorePerEpochFunc: rewardsStorePerEpochFunc,
		poolStatsStore:           poolStatsStore,
		committeeStore:           committeeStore,
		performanceFactorsFunc:   performanceFactorsFunc,
		apiProvider:              apiProvider,
	}
}

func (t *Tracker) RegisterCommittee(epoch iotago.EpochIndex, committee *account.Accounts) error {
	return t.committeeStore.Store(epoch, committee)
}

func (t *Tracker) TrackValidationBlock(block *blocks.Block) {
	validatorBlock, isValidationBlock := block.ValidationBlock()
	if !isValidationBlock {
		return
	}

	t.mutex.Lock()
	defer t.mutex.Unlock()

	t.performanceFactorsMutex.Lock()
	defer t.performanceFactorsMutex.Unlock()

	performanceFactors := t.performanceFactorsFunc(block.ID().Index())
	pf, err := performanceFactors.Load(block.ProtocolBlock().IssuerID)
	if err != nil {
		// TODO replace panic with errors in the future, like triggering an error event
		panic(ierrors.Errorf("failed to load performance factor for account %s", block.ProtocolBlock().IssuerID))
	}

	// TODO: store highest supported version per validator?
	_ = validatorBlock.HighestSupportedVersion

	err = performanceFactors.Store(block.ProtocolBlock().IssuerID, pf+1)
	if err != nil {
		// TODO replace panic with errors in the future, like triggering an error event
		panic(ierrors.Errorf("failed to store performance factor for account %s", block.ProtocolBlock().IssuerID))
	}
}

func (t *Tracker) ApplyEpoch(epoch iotago.EpochIndex, committee *account.Accounts) {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	timeProvider := t.apiProvider.APIForEpoch(epoch).TimeProvider()
	epochStartSlot := timeProvider.EpochStart(epoch)
	epochEndSlot := timeProvider.EpochEnd(epoch)

	profitMargin := calculateProfitMargin(committee.TotalValidatorStake(), committee.TotalStake())
	poolsStats := &model.PoolsStats{
		TotalStake:          committee.TotalStake(),
		TotalValidatorStake: committee.TotalValidatorStake(),
		ProfitMargin:        profitMargin,
	}

	if err := t.poolStatsStore.Store(epoch, poolsStats); err != nil {
		panic(ierrors.Wrapf(err, "failed to store pool stats for epoch %d", epoch))
	}

	rewardsTree := t.rewardsMap(epoch)

	committee.ForEach(func(accountID iotago.AccountID, pool *account.Pool) bool {
		intermediateFactors := make([]uint64, 0, epochEndSlot+1-epochStartSlot)
		for slot := epochStartSlot; slot <= epochEndSlot; slot++ {
			performanceFactorStorage := t.performanceFactorsFunc(slot)
			if performanceFactorStorage == nil {
				intermediateFactors = append(intermediateFactors, 0)
				continue
			}

			pf, err := performanceFactorStorage.Load(accountID)
			if err != nil {
				panic(ierrors.Wrapf(err, "failed to load performance factor for account %s", accountID))
			}

			intermediateFactors = append(intermediateFactors, pf)
		}

		if err := rewardsTree.Set(accountID, &model.PoolRewards{
			PoolStake:   pool.PoolStake,
			PoolRewards: t.poolReward(epochEndSlot, committee.TotalValidatorStake(), committee.TotalStake(), pool.PoolStake, pool.ValidatorStake, pool.FixedCost, t.aggregatePerformanceFactors(intermediateFactors)),
			FixedCost:   pool.FixedCost,
		}); err != nil {
			panic(ierrors.Wrapf(err, "failed to set rewards for account %s", accountID))
		}

		return true
	})

	if err := rewardsTree.Commit(); err != nil {
		panic(ierrors.Wrapf(err, "failed to commit rewards for epoch %d", epoch))
	}
}

func (t *Tracker) EligibleValidatorCandidates(_ iotago.EpochIndex) ds.Set[iotago.AccountID] {
	// TODO: we should choose candidates we tracked performance for

	return ds.NewSet[iotago.AccountID]()
}

func (t *Tracker) LoadCommitteeForEpoch(epoch iotago.EpochIndex) (committee *account.Accounts, exists bool) {
	c, err := t.committeeStore.Load(epoch)
	if err != nil {
		panic(ierrors.Wrapf(err, "failed to load committee for epoch %d", epoch))
	}
	if c == nil {
		return nil, false
	}

	return c, true
}

func (t *Tracker) aggregatePerformanceFactors(issuedBlocksPerSlot []uint64) uint64 {
	if len(issuedBlocksPerSlot) == 0 {
		return 0
	}

	var sum uint64
	for _, issuedBlocks := range issuedBlocksPerSlot {
		if issuedBlocks > uint64(validatorBlocksPerSlot) {
			// we harshly punish validators that issue any blocks more than allowed
			return 0
		}
		sum += issuedBlocks
	}

	// TODO: we should scale the result by the amount of slots per epoch,
	// otherwise we lose a lot of precision here.
	return sum / uint64(len(issuedBlocksPerSlot))
}
