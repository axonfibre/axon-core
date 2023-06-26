package rewards

import (
	"encoding/binary"
	"io"

	"github.com/pkg/errors"

	"github.com/iotaledger/hive.go/ads"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/iota-core/pkg/utils"
	iotago "github.com/iotaledger/iota.go/v4"
)

func (m *Manager) Import(reader io.ReadSeeker) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	err := m.importPerformanceFactor(reader)
	if err != nil {
		return errors.Wrap(err, "unable to import performance factor")
	}

	err = m.importPoolRewards(reader)
	if err != nil {
		return errors.Wrap(err, "unable to import pool rewards")
	}

	err = m.importPoolsStats(reader)
	if err != nil {
		return errors.Wrap(err, "unable to import pool stats")
	}

	return nil

}

func (m *Manager) importPerformanceFactor(reader io.ReadSeeker) error {
	var slotCount uint64
	if err := binary.Read(reader, binary.LittleEndian, &slotCount); err != nil {
		return errors.Wrap(err, "unable to read slot count")
	}
	for i := uint64(0); i < slotCount; i++ {
		var slotIndex iotago.SlotIndex
		if err := binary.Read(reader, binary.LittleEndian, &slotIndex); err != nil {
			return errors.Wrap(err, "unable to read slot index")
		}

		var accountsCount uint64
		if err := binary.Read(reader, binary.LittleEndian, &accountsCount); err != nil {
			return errors.Wrap(err, "unable to read accounts count")
		}

		performanceFactors := m.performanceFactorsFunc(slotIndex)
		for j := uint64(0); j < accountsCount; j++ {
			var accountID iotago.AccountID
			if err := binary.Read(reader, binary.LittleEndian, &accountID); err != nil {
				return errors.Wrap(err, "unable to read account id")
			}
			var performanceFactor uint64
			if err := binary.Read(reader, binary.LittleEndian, &performanceFactor); err != nil {
				return errors.Wrap(err, "unable to read performance factor")
			}
			err := performanceFactors.Store(accountID, performanceFactor)
			if err != nil {
				return errors.Wrap(err, "unable to store performance factor")
			}
		}
	}

	return nil
}

func (m *Manager) importPoolRewards(reader io.ReadSeeker) error {
	var epochCount uint64
	if err := binary.Read(reader, binary.LittleEndian, &epochCount); err != nil {
		return errors.Wrap(err, "unable to read epoch count")
	}
	for i := uint64(0); i < epochCount; i++ {
		var epochIndex iotago.EpochIndex
		if err := binary.Read(reader, binary.LittleEndian, &epochIndex); err != nil {
			return errors.Wrap(err, "unable to read epoch index")
		}
		rewardsTree := ads.NewMap[iotago.AccountID, AccountRewards](m.rewardsStorage(epochIndex))
		var accountsCount uint64
		if err := binary.Read(reader, binary.LittleEndian, &accountsCount); err != nil {
			return errors.Wrap(err, "unable to read accounts count")
		}
		for j := uint64(0); j < accountsCount; j++ {
			var accountID iotago.AccountID
			if err := binary.Read(reader, binary.LittleEndian, &accountID); err != nil {
				return errors.Wrap(err, "unable to read account id")
			}
			var reward AccountRewards
			if err := binary.Read(reader, binary.LittleEndian, &reward); err != nil {
				return errors.Wrap(err, "unable to read reward")
			}
			rewardsTree.Set(accountID, &reward)
		}
	}

	return nil
}

func (m *Manager) importPoolsStats(reader io.ReadSeeker) error {
	var epochCount uint64
	if err := binary.Read(reader, binary.LittleEndian, &epochCount); err != nil {
		return errors.Wrap(err, "unable to read epoch count")
	}
	for i := uint64(0); i < epochCount; i++ {
		var epochIndex iotago.EpochIndex
		if err := binary.Read(reader, binary.LittleEndian, &epochIndex); err != nil {
			return errors.Wrap(err, "unable to read epoch index")
		}

		var poolStats PoolsStats
		if err := binary.Read(reader, binary.LittleEndian, &poolStats); err != nil {
			return errors.Wrap(err, "unable to read pool stats")
		}
		err := m.poolStatsStore.Set(epochIndex.Bytes(), lo.PanicOnErr(poolStats.Bytes()))
		if err != nil {
			return errors.Wrap(err, "unable to store pool stats")
		}
	}

	return nil
}

func (m *Manager) Export(writer io.WriteSeeker, targetSlotIndex iotago.SlotIndex) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	targetEpoch := m.timeProvider.EpochFromSlot(targetSlotIndex)
	positionedWriter := utils.NewPositionedWriter(writer)

	// if target index is the last slot of the epoch, the epoch was committed
	if m.timeProvider.EpochEnd(targetEpoch) != targetSlotIndex {
		targetEpoch--
	}

	err := m.exportPerformanceFactor(positionedWriter, m.timeProvider.EpochStart(targetEpoch+1), targetSlotIndex)
	if err != nil {
		return errors.Wrap(err, "unable to export performance factor")
	}

	err = m.exportPoolRewards(positionedWriter, targetEpoch)
	if err != nil {
		return errors.Wrap(err, "unable to export pool rewards")
	}

	err = m.exportPoolsStats(positionedWriter, targetEpoch)
	if err != nil {
		return errors.Wrap(err, "unable to export pool stats")
	}

	return nil
}

func (m *Manager) exportPerformanceFactor(pWriter *utils.PositionedWriter, startSlot, targetSlot iotago.SlotIndex) error {
	m.performanceFactorsMutex.RLock()
	defer m.performanceFactorsMutex.RUnlock()

	var slotCount uint64
	if err := pWriter.WriteValue("pf slot count", slotCount, true); err != nil {
		return errors.Wrap(err, "unable to write pf slot count")
	}

	for currentSlot := startSlot; currentSlot <= targetSlot; currentSlot++ {
		if err := pWriter.WriteValue("slot index", currentSlot); err != nil {
			return errors.Wrap(err, "unable to write slot index")
		}

		var accountsCount uint64
		if err := pWriter.WriteValue("pf account count", accountsCount, true); err != nil {
			return errors.Wrap(err, "unable to write pf accounts count")
		}

		if err := m.performanceFactorsFunc(currentSlot).ForEachPerformanceFactor(func(accountID iotago.AccountID, pf uint64) error {
			if err := pWriter.WriteValue("account id", accountID); err != nil {
				return errors.Wrap(err, "unable to write account id")

			}
			if err := pWriter.WriteValue("performance factor", pf); err != nil {
				return errors.Wrap(err, "unable to write performance factor")
			}
			accountsCount++

			return nil
		}); err != nil {
			return errors.Wrap(err, "unable to write performance factors")
		}

		if err := pWriter.WriteValueAtBookmark("pf account count", accountsCount); err != nil {
			return errors.Wrap(err, "unable to write pf accounts count")
		}

		slotCount++
	}

	if err := pWriter.WriteValueAtBookmark("pf slot count", slotCount); err != nil {
		return errors.Wrap(err, "unable to write pf slot count")
	}

	return nil
}

func (m *Manager) exportPoolRewards(pWriter *utils.PositionedWriter, epoch iotago.EpochIndex) error {
	// export all stored pools
	// in theory we could save the epoch count only once, because stats and rewards should be the same length
	var epochCount uint64
	if err := pWriter.WriteValue("pool rewards epoch count", epochCount, true); err != nil {
		return errors.Wrap(err, "unable to write epoch count")
	}
	var innerErr error
	err := m.rewardBaseStore.Iterate([]byte{}, func(key []byte, value []byte) bool {
		epochIndex := iotago.EpochIndex(binary.LittleEndian.Uint64(key))
		if epochIndex > epoch {
			// continue
			return true
		}
		if err := pWriter.WriteBytes(key); err != nil {
			innerErr = errors.Wrap(err, "unable to write epoch index")
			return false
		}
		if err := pWriter.WriteBytes(value); err != nil {
			innerErr = errors.Wrap(err, "unable to write epoch pools rewards")
			return false
		}
		epochCount++

		return true
	})
	if err != nil {
		return errors.Wrapf(err, "unable to iterate over reward base store: %s", innerErr)
	}
	if err = pWriter.WriteValueAtBookmark("pool rewards epoch count", epochCount); err != nil {
		return errors.Wrap(err, "unable to write epoch count")
	}

	return nil
}

func (m *Manager) exportPoolsStats(pWriter *utils.PositionedWriter, targetEpoch iotago.EpochIndex) error {
	var epochCount uint64
	if err := pWriter.WriteValue("pools stats epoch count", epochCount, true); err != nil {
		return errors.Wrap(err, "unable to write epoch count")
	}
	// export all stored pools
	var innerErr error
	if err := m.poolStatsStore.Iterate([]byte{}, func(key []byte, value []byte) bool {
		epochIndex := iotago.EpochIndex(binary.LittleEndian.Uint64(key))
		if epochIndex > targetEpoch {
			// continue
			return true
		}
		if err := pWriter.WriteBytes(key); err != nil {
			innerErr = errors.Wrap(err, "unable to write epoch index")
			return false
		}
		if err := pWriter.WriteBytes(value); err != nil {
			innerErr = errors.Wrap(err, "unable to write pools stats")
			return false
		}
		epochCount++

		return true
	}); err != nil {
		return errors.Wrap(err, "unable to iterate over pools stats")
	} else if innerErr != nil {
		return errors.Wrap(innerErr, "error while iterating over pools stats")

	}
	if err := pWriter.WriteValueAtBookmark("pools stats epoch count", epochCount); err != nil {
		return errors.Wrap(err, "unable to write epoch count")
	}

	return nil
}
