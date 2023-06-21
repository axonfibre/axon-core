package rewards

import (
	"sync"

	"github.com/cockroachdb/errors"

	"github.com/iotaledger/hive.go/ads"
	"github.com/iotaledger/hive.go/ds/shrinkingmap"
	"github.com/iotaledger/hive.go/kvstore"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
	"github.com/iotaledger/iota-core/pkg/storage/prunable"
	iotago "github.com/iotaledger/iota.go/v4"
)

type Manager struct {
	rewardBaseStore kvstore.KVStore
	poolStatsStore  kvstore.KVStore

	performanceFactorsFunc func(slot iotago.SlotIndex) *prunable.PerformanceFactors

	// Cache to allow atomic increase operations protected by a lock
	performanceFactorsCache *shrinkingmap.ShrinkingMap[iotago.SlotIndex, *prunable.PerformanceFactors]
	performanceFactorsMutex sync.RWMutex

	timeProvider *iotago.TimeProvider

	mutex sync.RWMutex
}

func New(rewardsBaseStore, poolStatsStore kvstore.KVStore, performanceFactorsFunc func(slot iotago.SlotIndex) *prunable.PerformanceFactors, timeProvider *iotago.TimeProvider) *Manager {
	return &Manager{
		rewardBaseStore:         rewardsBaseStore,
		poolStatsStore:          poolStatsStore,
		performanceFactorsCache: shrinkingmap.New[iotago.SlotIndex, *prunable.PerformanceFactors](),
		performanceFactorsFunc:  performanceFactorsFunc,
		timeProvider:            timeProvider,
	}
}

func (m *Manager) rewardsStorage(epochIndex iotago.EpochIndex) kvstore.KVStore {
	return lo.PanicOnErr(m.rewardBaseStore.WithExtendedRealm(epochIndex.Bytes()))
}

func (m *Manager) BlockAccepted(block *blocks.Block) {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	m.performanceFactorsMutex.Lock()
	defer m.performanceFactorsMutex.Unlock()

	// TODO: check if this block is a validator block

	performanceFactors, _ := m.performanceFactorsCache.GetOrCreate(block.ID().Index(), func() *prunable.PerformanceFactors {
		return m.performanceFactorsFunc(block.ID().Index())
	})
	pf, err := performanceFactors.Load(block.Block().IssuerID)
	if err != nil {
		panic(err)
	}
	performanceFactors.Store(block.Block().IssuerID, pf+1)
}

func (m *Manager) Evict(slot iotago.SlotIndex) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.performanceFactorsCache.Delete(slot)
}

func (m *Manager) RewardsRoot(epochIndex iotago.EpochIndex) iotago.Identifier {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return iotago.Identifier(ads.NewMap[iotago.AccountID, RewardsForAccount](m.rewardsStorage(epochIndex)).Root())
}

func (m *Manager) ApplyEpoch(epochIndex iotago.EpochIndex, poolStakes map[iotago.AccountID]*Pool) error {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	rewardsTree := ads.NewMap[iotago.AccountID, RewardsForAccount](m.rewardsStorage(epochIndex))
	epochSlotStart := m.timeProvider.EpochStart(epochIndex)
	epochSlotEnd := m.timeProvider.EpochEnd(epochIndex)

	var totalStake uint64
	var totalValidatorStake uint64

	for _, pool := range poolStakes {
		totalStake += pool.PoolStake
		totalValidatorStake += pool.ValidatorStake
	}

	profitMargin := profitMargin(totalValidatorStake, totalStake)
	poolsStats := PoolsStats{
		TotalStake:          totalStake,
		TotalValidatorStake: totalValidatorStake,
		ProfitMargin:        profitMargin,
	}

	m.poolStatsStore.Set(epochIndex.Bytes(), lo.PanicOnErr(poolsStats.Bytes()))

	for accountID, pool := range poolStakes {
		intermediateFactors := make([]uint64, 0)
		for slot := epochSlotStart; slot <= epochSlotEnd; slot++ {
			performanceFactorStorage, _ := m.performanceFactorsCache.Get(slot)
			if performanceFactorStorage == nil {
				intermediateFactors = append(intermediateFactors, 0)
			}

			pf, err := performanceFactorStorage.Load(accountID)
			if err != nil {
				return errors.Wrapf(err, "failed to load performance factor for account %s", accountID)
			}

			intermediateFactors = append(intermediateFactors, pf)
		}

		rewardsTree.Set(accountID, &RewardsForAccount{
			PoolStake:   pool.PoolStake,
			PoolRewards: poolReward(totalValidatorStake, totalStake, profitMargin, pool.FixedCost, aggregatePerformanceFactors(intermediateFactors)),
			FixedCost:   pool.FixedCost,
		})
	}

	return nil
}

func aggregatePerformanceFactors(pfs []uint64) uint64 {
	var sum uint64
	for _, pf := range pfs {
		sum += pf
	}
	return sum / uint64(len(pfs))
}

func profitMargin(totalValidatorsStake, totalPoolStake uint64) uint64 {
	return (1 << 8) * totalValidatorsStake / (totalValidatorsStake + totalPoolStake)
}

func poolReward(totalValidatorsStake, totalStake, profitMargin, fixedCosts, performanceFactor uint64) uint64 {
	// TODO: decay is calculated per epoch now, so do we need to calculate the rewards for each slot of the epoch?
	return totalValidatorsStake * performanceFactor
}
