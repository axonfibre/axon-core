package accountsledger

import (
	"io"

	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/serializer/v2"
	"github.com/iotaledger/hive.go/serializer/v2/stream"
	"github.com/iotaledger/iota-core/pkg/model"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/accounts"
	iotago "github.com/iotaledger/iota.go/v4"
)

func (m *Manager) Import(reader io.ReadSeeker) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	latestCommittedSlot, err := stream.Read[iotago.SlotIndex](reader)
	if err != nil {
		return ierrors.Wrap(err, "unable to read latest committed slot")
	}
	// populate the account tree, account tree should be empty at this point
	if err := stream.ReadCollection(reader, serializer.SeriLengthPrefixTypeAsUint64, func(i int) error {
		accountData, err := stream.ReadObjectFromReader(reader, accounts.AccountDataFromReader)
		if err != nil {
			return ierrors.Wrapf(err, "unable to read account data at index %d", i)
		}

		if err := m.accountsTree.Set(accountData.ID, accountData); err != nil {
			return ierrors.Wrapf(err, "unable to set account %s", accountData.ID)
		}

		m.LogDebug("Imported account", "accountData", accountData)

		return nil
	}); err != nil {
		return ierrors.Wrap(err, "failed to read account data")
	}

	oldestSlot, err := m.readSlotDiffs(reader)
	if err != nil {
		return ierrors.Wrap(err, "unable to import slot diffs")
	}

	m.latestCommittedSlot = latestCommittedSlot
	if err := m.Rollback(oldestSlot); err != nil {
		return ierrors.Wrapf(err, "unable to rollback to slot %d", oldestSlot)
	}
	m.latestCommittedSlot = oldestSlot

	return nil
}

func (m *Manager) Export(writer io.WriteSeeker, targetIndex iotago.SlotIndex) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.LogDebug("Exporting AccountsLedger", "latestCommittedSlot", m.latestCommittedSlot, "targetIndex", targetIndex)

	if err := stream.Write[iotago.SlotIndex](writer, m.latestCommittedSlot); err != nil {
		return ierrors.Wrap(err, "unable to write latest committed slot")
	}

	if err := stream.WriteCollection(writer, serializer.SeriLengthPrefixTypeAsUint64, func() (int, error) {
		elements, err := m.exportAccountTree(writer)
		if err != nil {
			return 0, ierrors.Wrap(err, "can't write account tree")
		}

		return elements, nil
	}); err != nil {
		return ierrors.Wrapf(err, "unable to export accounts for slot %d", targetIndex)
	}

	if err := stream.WriteCollection(writer, serializer.SeriLengthPrefixTypeAsUint64, func() (elementsCount int, err error) {
		elementsCount, err = m.writeSlotDiffs(writer, targetIndex)
		if err != nil {
			return 0, ierrors.Wrap(err, "can't write slot diffs")
		}

		return elementsCount, nil
	}); err != nil {
		return ierrors.Wrapf(err, "unable to export slot diffs for slot %d", targetIndex)
	}

	return nil
}

// exportAccountTree exports the current AccountTree
func (m *Manager) exportAccountTree(writer io.WriteSeeker) (int, error) {
	var accountCount int

	if err := m.accountsTree.Stream(func(id iotago.AccountID, account *accounts.AccountData) error {
		m.LogTrace("exportAccountTree", "accountID", id, "account", account)

		if err := stream.WriteObject(writer, account, (*accounts.AccountData).Bytes); err != nil {
			return ierrors.Wrapf(err, "unable to write account %s", id)
		}
		accountCount++

		return nil
	}); err != nil {
		return 0, ierrors.Wrap(err, "error in streaming account tree")
	}

	return accountCount, nil
}

func (m *Manager) readSlotDiffs(reader io.ReadSeeker) (iotago.SlotIndex, error) {
	oldestSlot := iotago.MaxSlotIndex
	// Read all the slots.
	if err := stream.ReadCollection(reader, serializer.SeriLengthPrefixTypeAsUint64, func(i int) error {
		slot, err := stream.Read[iotago.SlotIndex](reader)
		if err != nil {
			return ierrors.Wrapf(err, "unable to read slot index at index %d", i)
		}

		if slot < oldestSlot {
			oldestSlot = slot
		}

		// Read all the slot diffs within each slot.
		if err := stream.ReadCollection(reader, serializer.SeriLengthPrefixTypeAsUint64, func(j int) error {
			diffStore, err := m.slotDiff(slot)
			if err != nil {
				return ierrors.Wrapf(err, "unable to get account diff storage for slot %d", slot)
			}

			accountID, err := stream.Read[iotago.AccountID](reader)
			if err != nil {
				return ierrors.Wrapf(err, "unable to read accountID for index %d", j)
			}

			destroyed, err := stream.Read[bool](reader)
			if err != nil {
				return ierrors.Wrapf(err, "unable to read destroyed flag for accountID %s", accountID)
			}

			var accountDiff *model.AccountDiff
			if !destroyed {
				if accountDiff, err = stream.ReadObjectFromReader(reader, model.AccountDiffFromReader); err != nil {
					return ierrors.Wrapf(err, "unable to read account diff for accountID %s", accountID)
				}
			} else {
				accountDiff = model.NewAccountDiff()
			}

			m.LogDebug("Imported account diff", "slot", slot, "accountID", accountID, "destroyed", destroyed, "accountDiff", accountDiff)

			if err := diffStore.Store(accountID, accountDiff, destroyed); err != nil {
				return ierrors.Wrapf(err, "unable to store slot diff for accountID %s", accountID)
			}

			return nil
		}); err != nil {
			return ierrors.Wrapf(err, "unable to read accounts in diff count at index %d", i)
		}

		return nil
	}); err != nil {
		return oldestSlot, ierrors.Wrap(err, "failed to read slot diffs")
	}

	return oldestSlot, nil
}

func (m *Manager) writeSlotDiffs(writer io.WriteSeeker, targetSlot iotago.SlotIndex) (int, error) {
	var slotDiffsCount int

	for slot := m.latestCommittedSlot; slot > targetSlot; slot-- {
		var accountsInDiffCount int

		if err := stream.Write(writer, slot); err != nil {
			return 0, ierrors.Wrapf(err, "unable to write slot %d", slot)
		}

		slotDiffs, err := m.slotDiff(slot)
		if err != nil {
			// if slot is already pruned, then don't write anything
			continue
		}

		if err = stream.WriteCollection(writer, serializer.SeriLengthPrefixTypeAsUint64, func() (int, error) {
			var innerErr error

			if err = slotDiffs.Stream(func(accountID iotago.AccountID, accountDiff *model.AccountDiff, destroyed bool) bool {
				if err = stream.Write(writer, accountID); err != nil {
					innerErr = ierrors.Wrapf(err, "unable to write accountID for account %s", accountID)
					return false
				}

				if err = stream.Write(writer, destroyed); err != nil {
					innerErr = ierrors.Wrapf(err, "unable to write destroyed flag for account %s", accountID)
					return false
				}

				if !destroyed {
					if err = stream.WriteObject(writer, accountDiff, (*model.AccountDiff).Bytes); err != nil {
						innerErr = ierrors.Wrapf(err, "unable to write account diff for account %s", accountID)
						return false
					}
				}

				m.LogDebug("Exported account diff", "slot", slot, "accountID", accountID, "destroyed", destroyed, "accountDiff", accountDiff)

				accountsInDiffCount++

				return true
			}); err != nil {
				return 0, ierrors.Wrapf(err, "unable to stream slot diff for index %d", slot)
			}

			if innerErr != nil {
				return 0, ierrors.Wrapf(innerErr, "unable to stream slot diff for index %d", slot)
			}

			return accountsInDiffCount, nil
		}); err != nil {
			return 0, ierrors.Wrapf(err, "unable to write slot diff %d", slot)
		}

		slotDiffsCount++
	}

	return slotDiffsCount, nil
}
