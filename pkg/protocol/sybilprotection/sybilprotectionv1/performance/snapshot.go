package performance

import (
	"io"

	"github.com/iotaledger/hive.go/core/safemath"
	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/serializer/v2"
	"github.com/iotaledger/hive.go/serializer/v2/stream"
	"github.com/iotaledger/iota-core/pkg/core/account"
	"github.com/iotaledger/iota-core/pkg/model"
	iotago "github.com/iotaledger/iota.go/v4"
)

func (t *Tracker) Import(reader io.ReadSeeker) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if err := t.importPerformanceFactor(reader); err != nil {
		return ierrors.Wrap(err, "unable to import performance factor")
	}

	if err := t.importPoolRewards(reader); err != nil {
		return ierrors.Wrap(err, "unable to import pool rewards")
	}

	if err := t.importPoolsStats(reader); err != nil {
		return ierrors.Wrap(err, "unable to import pool stats")
	}

	if err := t.importCommittees(reader); err != nil {
		return ierrors.Wrap(err, "unable to import committees")
	}

	return nil
}

func (t *Tracker) Export(writer io.WriteSeeker, targetSlotIndex iotago.SlotIndex) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	timeProvider := t.apiProvider.APIForSlot(targetSlotIndex).TimeProvider()
	targetEpoch := timeProvider.EpochFromSlot(targetSlotIndex)

	// if the target index is the last slot of the epoch, the epoch was committed - unless it's epoch 0 to avoid underflow.
	if timeProvider.EpochEnd(targetEpoch) != targetSlotIndex && targetEpoch > 0 {
		targetEpoch = lo.PanicOnErr(safemath.SafeSub(targetEpoch, 1))
	}

	// If targetEpoch==0 then export performance factors from slot 0 to the targetSlotIndex.
	// PoolRewards and PoolStats are empty if epoch 0 was not committed yet, so it's not a problem.
	// But PerformanceFactors are exported for the ongoing epoch, so for epoch 0 we must make an exception and not add 1 to the targetEpoch.
	if err := t.exportPerformanceFactor(writer, timeProvider.EpochStart(targetEpoch+lo.Cond(targetEpoch == 0, iotago.EpochIndex(0), iotago.EpochIndex(1))), targetSlotIndex); err != nil {
		return ierrors.Wrap(err, "unable to export performance factor")
	}

	if err := t.exportPoolRewards(writer, targetEpoch); err != nil {
		return ierrors.Wrap(err, "unable to export pool rewards")
	}

	if err := t.exportPoolsStats(writer, targetEpoch); err != nil {
		return ierrors.Wrap(err, "unable to export pool stats")
	}

	if err := t.exportCommittees(writer, targetSlotIndex); err != nil {
		return ierrors.Wrap(err, "unable to export committees")
	}

	return nil
}

func (t *Tracker) importPerformanceFactor(reader io.ReadSeeker) error {
	if err := stream.ReadCollection(reader, serializer.SeriLengthPrefixTypeAsUint32, func(i int) error {
		slot, err := stream.Read[iotago.SlotIndex](reader)
		if err != nil {
			return ierrors.Wrapf(err, "unable to read slot index at index %d", i)
		}

		performanceFactors, err := t.validatorPerformancesFunc(slot)
		if err != nil {
			return ierrors.Wrapf(err, "unable to get performance factors for slot index %d", slot)
		}

		if err := stream.ReadCollection(reader, serializer.SeriLengthPrefixTypeAsUint64, func(j int) error {
			accountID, err := stream.Read[iotago.AccountID](reader)
			if err != nil {
				return ierrors.Wrapf(err, "unable to read account id at index %d", j)
			}
			performanceFactor, err := stream.ReadObjectFromReader(reader, model.ValidatorPerformanceFromReader)
			if err != nil {
				return ierrors.Wrapf(err, "unable to read performance factor for account %s and slot %d", accountID, slot)
			}
			if err = performanceFactors.Store(accountID, performanceFactor); err != nil {
				return ierrors.Wrapf(err, "unable to store performance factor for account %s and slot index %d", accountID, slot)
			}

			t.LogDebug("Importing performance factor", "accountID", accountID, "slot", slot, "performanceFactor", performanceFactor)

			return nil
		}); err != nil {
			return ierrors.Wrapf(err, "unable to read performance factors for slot %d", slot)
		}

		return nil
	}); err != nil {
		return ierrors.Wrap(err, "unable to read performance factors collection")
	}

	return nil
}

func (t *Tracker) importPoolRewards(reader io.ReadSeeker) error {
	if err := stream.ReadCollection(reader, serializer.SeriLengthPrefixTypeAsUint32, func(int) error {
		epoch, err := stream.Read[iotago.EpochIndex](reader)
		if err != nil {
			return ierrors.Wrap(err, "unable to read epoch")
		}

		rewardsTree, err := t.rewardsMap(epoch)
		if err != nil {
			return ierrors.Wrapf(err, "unable to get rewards tree for epoch index %d", epoch)
		}

		if err := stream.ReadCollection(reader, serializer.SeriLengthPrefixTypeAsUint64, func(int) error {
			accountID, err := stream.Read[iotago.AccountID](reader)
			if err != nil {
				return ierrors.Wrap(err, "unable to read account id")
			}

			reward, err := stream.ReadObjectFromReader(reader, model.PoolRewardsFromReader)
			if err != nil {
				return ierrors.Wrapf(err, "unable to read reward for account %s and epoch index %d", accountID, epoch)
			}

			t.LogDebug("Importing reward", "accountID", accountID, "epoch", epoch, "reward", reward)

			if err = rewardsTree.Set(accountID, reward); err != nil {
				return ierrors.Wrapf(err, "unable to set reward for account %s and epoch index %d", accountID, epoch)
			}

			return nil
		}); err != nil {
			return ierrors.Wrapf(err, "unable to read rewards collection for epoch %d", epoch)
		}

		if err := rewardsTree.Commit(); err != nil {
			return ierrors.Wrapf(err, "unable to commit rewards for epoch index %d", epoch)
		}

		return nil
	}); err != nil {
		return ierrors.Wrap(err, "unable to read pool rewards collection")
	}

	return nil
}

func (t *Tracker) importPoolsStats(reader io.ReadSeeker) error {
	if err := stream.ReadCollection(reader, serializer.SeriLengthPrefixTypeAsUint32, func(int) error {
		epoch, err := stream.Read[iotago.EpochIndex](reader)
		if err != nil {
			return ierrors.Wrap(err, "unable to read epoch")
		}

		poolStats, err := stream.ReadObjectFromReader(reader, model.PoolStatsFromReader)
		if err != nil {
			return ierrors.Wrapf(err, "unable to read pool stats for epoch %d", epoch)
		}

		if err := t.poolStatsStore.Store(epoch, poolStats); err != nil {
			return ierrors.Wrapf(err, "unable to store pool stats for the epoch index %d", epoch)
		}

		t.LogDebug("Importing pool stats", "epoch", epoch, "poolStats", poolStats)

		return nil
	}); err != nil {
		return ierrors.Wrap(err, "unable to read pool stats collection")
	}

	return nil
}

func (t *Tracker) importCommittees(reader io.ReadSeeker) error {
	if err := stream.ReadCollection(reader, serializer.SeriLengthPrefixTypeAsUint32, func(int) error {
		epoch, err := stream.Read[iotago.EpochIndex](reader)
		if err != nil {
			return ierrors.Wrap(err, "unable to read epoch index")
		}

		// A snapshot contains only the list of committee members.
		// The committee for each epoch gets SeatIndices assigned.
		// When the node is running, and a committee member is re-elected,
		// the SeatIndex must be maintained across the two epochs
		// to prevent a single Account from incorrectly inflating the number of active
		// seats in the committee - active on seat X from the previous epoch, and on seat Y in the new epoch.
		// However, when loading past committees from the snapshot, this SeatIndex continuity is not necessary, that's why
		// SeatIndices are assigned for each epoch individually, without looking at committees from other epochs.
		committeeAccounts, err := account.AccountsFromReader(reader)
		if err != nil {
			return ierrors.Wrapf(err, "unable to read committee for the epoch %d", epoch)
		}

		isReused, err := stream.Read[bool](reader)
		if err != nil {
			return ierrors.Wrapf(err, "unable to read reused flag for the epoch %d", epoch)
		}

		committee := committeeAccounts.SeatedAccounts()
		if isReused {
			committee.SetReused()
		}

		t.LogDebug("Importing committee", "epoch", epoch, "committee", committee)

		if err = t.committeeStore.Store(epoch, committee); err != nil {
			return ierrors.Wrap(err, "unable to store committee")
		}

		return nil
	}); err != nil {
		return ierrors.Wrap(err, "unable to read committees collection")
	}

	return nil
}

func (t *Tracker) exportPerformanceFactor(writer io.WriteSeeker, startSlot iotago.SlotIndex, targetSlot iotago.SlotIndex) error {
	t.performanceFactorsMutex.RLock()
	defer t.performanceFactorsMutex.RUnlock()

	if err := stream.WriteCollection(writer, serializer.SeriLengthPrefixTypeAsUint32, func() (int, error) {
		var slotCount int

		for currentSlot := startSlot; currentSlot <= targetSlot; currentSlot++ {
			if err := stream.Write(writer, currentSlot); err != nil {
				return 0, ierrors.Wrapf(err, "unable to write slot index %d", currentSlot)
			}

			if err := stream.WriteCollection(writer, serializer.SeriLengthPrefixTypeAsUint64, func() (int, error) {
				var accountsCount int

				performanceFactors, err := t.validatorPerformancesFunc(currentSlot)
				if err != nil {
					return 0, ierrors.Wrapf(err, "unable to get performance factors for slot index %d", currentSlot)
				}

				if err = performanceFactors.Stream(func(accountID iotago.AccountID, pf *model.ValidatorPerformance) error {
					if err := stream.Write(writer, accountID); err != nil {
						return ierrors.Wrapf(err, "unable to write account id %s for slot %d", accountID, currentSlot)
					}

					if err := stream.WriteObject(writer, pf, (*model.ValidatorPerformance).Bytes); err != nil {
						return ierrors.Wrapf(err, "unable to write performance factor for accountID %s and slot index %d", accountID, currentSlot)
					}

					t.LogDebug("Exporting performance factor", "accountID", accountID, "slot", currentSlot, "performanceFactor", pf)

					accountsCount++

					return nil
				}); err != nil {
					return 0, ierrors.Wrapf(err, "unable to write performance factors for slot index %d", currentSlot)
				}

				return accountsCount, nil
			}); err != nil {
				return 0, ierrors.Wrapf(err, "unable to write accounts for slot %d", currentSlot)
			}

			slotCount++
		}

		return slotCount, nil
	}); err != nil {
		return ierrors.Wrap(err, "unable to write slot count")
	}

	return nil
}

func (t *Tracker) exportPoolRewards(writer io.WriteSeeker, targetEpoch iotago.EpochIndex) error {
	// export all stored pools
	// in theory we could save the epoch count only once, because stats and rewards should be the same length

	protocolParams := t.apiProvider.APIForEpoch(targetEpoch).ProtocolParameters()
	retentionPeriod := iotago.EpochIndex(protocolParams.RewardsParameters().RetentionPeriod)
	earliestRewardEpoch := iotago.EpochIndex(0)
	if targetEpoch > retentionPeriod {
		earliestRewardEpoch = targetEpoch - retentionPeriod
	}

	if err := stream.WriteCollection(writer, serializer.SeriLengthPrefixTypeAsUint32, func() (int, error) {
		var epochCount int
		// Start at the targest epoch and go back in time until earliestRewardEpoch or epoch 0 (included)
		epoch := targetEpoch
		for {
			rewardsMap, err := t.rewardsMap(epoch)
			if err != nil {
				return 0, ierrors.Wrapf(err, "unable to get rewards tree for epoch %d", epoch)
			}

			if rewardsMap.WasRestoredFromStorage() {
				t.LogDebug("Exporting Pool Rewards", "epoch", epoch)

				if err := stream.Write(writer, epoch); err != nil {
					return 0, ierrors.Wrapf(err, "unable to write epoch index for epoch index %d", epoch)
				}

				if err := stream.WriteCollection(writer, serializer.SeriLengthPrefixTypeAsUint64, func() (int, error) {
					var accountCount int

					if err = rewardsMap.Stream(func(key iotago.AccountID, value *model.PoolRewards) error {
						if err := stream.Write(writer, key); err != nil {
							return ierrors.Wrapf(err, "unable to write account id for epoch %d and accountID %s", epoch, key)
						}

						if err := stream.WriteObject(writer, value, (*model.PoolRewards).Bytes); err != nil {
							return ierrors.Wrapf(err, "unable to write account rewards for epoch index %d and accountID %s", epoch, key)
						}

						t.LogDebug("Exporting Pool Reward", "epoch", epoch, "accountID", key, "rewards", value)

						accountCount++

						return nil
					}); err != nil {
						return 0, ierrors.Wrapf(err, "unable to stream rewards for epoch index %d", epoch)
					}

					return accountCount, nil
				}); err != nil {
					return 0, ierrors.Wrapf(err, "unable to write rewards for epoch index %d", epoch)
				}

				epochCount++
			} else {
				// if the map was not present in storage we can skip this epoch
				t.LogDebug("Skipping epoch", "epoch", epoch, "reason", "not restored from storage")
			}

			if epoch <= earliestRewardEpoch {
				// Every reward before earliestRewardEpoch is already exported, so stop here
				break
			}
			epoch = lo.PanicOnErr(safemath.SafeSub(epoch, 1))
		}

		return epochCount, nil
	}); err != nil {
		return ierrors.Wrap(err, "unable to write pool rewards collection")
	}

	return nil
}

func (t *Tracker) exportPoolsStats(writer io.WriteSeeker, targetEpoch iotago.EpochIndex) error {
	if err := stream.WriteCollection(writer, serializer.SeriLengthPrefixTypeAsUint32, func() (int, error) {
		var epochCount int

		// export all stored pools
		if err := t.poolStatsStore.StreamBytes(func(key []byte, value []byte) error {
			epoch, _, err := iotago.EpochIndexFromBytes(key)
			if err != nil {
				return err
			}

			if epoch > targetEpoch {
				// continue
				t.LogDebug("Skipping epoch", "epoch", epoch, "reason", "epoch is greater than target epoch")
				return nil
			}

			if err := stream.WriteBytes(writer, key); err != nil {
				return ierrors.Wrapf(err, "unable to write epoch index %d", epoch)
			}

			if err := stream.WriteBytes(writer, value); err != nil {
				return ierrors.Wrapf(err, "unable to write pools stats for epoch %d", epoch)
			}

			t.LogDebug("Exporting Pool Stats", "epoch", epoch, "poolStats", lo.Return1(model.PoolsStatsFromBytes(value)))

			epochCount++

			return nil
		}); err != nil {
			return 0, ierrors.Wrap(err, "unable to iterate over pools stats")
		}

		return epochCount, nil
	}); err != nil {
		return ierrors.Wrap(err, "unable to write pool stats collection")
	}

	return nil
}

func (t *Tracker) exportCommittees(writer io.WriteSeeker, targetSlot iotago.SlotIndex) error {
	apiForSlot := t.apiProvider.APIForSlot(targetSlot)
	epochFromTargetSlot := apiForSlot.TimeProvider().EpochFromSlot(targetSlot)

	pointOfNoReturn := apiForSlot.TimeProvider().EpochEnd(epochFromTargetSlot) - apiForSlot.ProtocolParameters().MaxCommittableAge()

	if err := stream.WriteCollection(writer, serializer.SeriLengthPrefixTypeAsUint32, func() (int, error) {
		var epochCount int

		if err := t.committeeStore.StreamBytes(func(epochBytes []byte, committeeBytes []byte) error {
			epoch, _, err := iotago.EpochIndexFromBytes(epochBytes)
			if err != nil {
				return ierrors.Wrapf(err, "failed to parse epoch bytes")
			}

			committee, _, err := account.SeatedAccountsFromBytes(committeeBytes)
			if err != nil {
				return ierrors.Wrapf(err, "failed to parse committee bytes for epoch %d", epoch)
			}

			// We have a committee for an epoch higher than the targetSlot
			// 1. We trust the point of no return, we export the committee for the next epoch
			// 2. If we don't trust the point-of-no-return
			// - we were able to rotate a committee, then we export it
			// - we were not able to rotate a committee (reused), then we don't export it
			if epoch > epochFromTargetSlot && targetSlot < pointOfNoReturn && committee.IsReused() {
				t.LogDebug("Skipping committee", "epoch", epoch, "reason", "epoch is greater than target epoch and committee is reused")
				return nil
			}

			// Save only the list of committee accounts in the snapshot, so that snapshots generated on different nodes
			// that assigned SeatIndices differently can still be compared and generate equal hashes.
			// When reading the snapshot,
			// nodes will assign SeatIndex to each committee member individually
			// as this perception is completely subjective.
			committeeAccounts, err := committee.Accounts()
			if err != nil {
				return ierrors.Wrapf(err, "failed to extract accounts from committee for epoch %d", epoch)
			}

			committeeAccountsBytes, err := committeeAccounts.Bytes()
			if err != nil {
				return ierrors.Wrapf(err, "failed to serialize committee accounts for epoch %d", epoch)
			}

			if err := stream.WriteBytes(writer, epochBytes); err != nil {
				return ierrors.Wrapf(err, "unable to write epoch index %d", epoch)
			}
			if err := stream.WriteBytes(writer, committeeAccountsBytes); err != nil {
				return ierrors.Wrapf(err, "unable to write committee for epoch %d", epoch)
			}
			if err := stream.Write[bool](writer, committee.IsReused()); err != nil {
				return ierrors.Wrapf(err, "unable to write reused flag for epoch %d", epoch)
			}

			t.LogDebug("Exporting committee", "epoch", epoch, "committee", committee)

			epochCount++

			return nil
		}); err != nil {
			return 0, ierrors.Wrap(err, "unable to iterate over committee base store")
		}

		return epochCount, nil
	}); err != nil {
		return ierrors.Wrap(err, "unable to write committees collection")
	}

	return nil
}
