package mock

import (
	"time"

	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/lo"
	iotago "github.com/iotaledger/iota.go/v4"
)

func (i *BlockIssuer) reviveChain(issuingTime time.Time, node *Node) (*iotago.Commitment, iotago.BlockID, error) {
	lastCommittedSlot := node.Protocol.Engines.Main.Get().SyncManager.LatestCommitment().Slot()
	apiForSlot := node.Protocol.APIForSlot(lastCommittedSlot)

	issuingSlot := apiForSlot.TimeProvider().SlotFromTime(issuingTime)

	// If the chain manager is aware of a commitments on the main chain, then do not force commit.
	// The node should wait to warpsync those slots and use those commitments to avoid potentially creating a diverging commitment.
	if issuingSlot > apiForSlot.ProtocolParameters().MinCommittableAge() &&
		node.Protocol.Chains.Main.Get().LatestCommitment.Get().Slot() >= issuingSlot-apiForSlot.ProtocolParameters().MinCommittableAge() {
		return nil, iotago.EmptyBlockID, ierrors.Errorf("chain manager is aware of a newer commitment, slot: %d, minCommittableAge: %d", issuingSlot-apiForSlot.ProtocolParameters().MinCommittableAge(), apiForSlot.ProtocolParameters().MinCommittableAge())
	}

	// Force commitments until minCommittableAge relative to the block's issuing time. We basically "pretend" that
	// this block was already accepted at the time of issuing so that we have a commitment to reference.
	if issuingSlot < apiForSlot.ProtocolParameters().MinCommittableAge() { // Should never happen as we're beyond maxCommittableAge which is > minCommittableAge.
		return nil, iotago.EmptyBlockID, ierrors.Errorf("issuing slot %d is smaller than min committable age %d", issuingSlot, apiForSlot.ProtocolParameters().MinCommittableAge())
	}
	commitUntilSlot := issuingSlot - apiForSlot.ProtocolParameters().MinCommittableAge()

	if err := node.Protocol.Engines.Main.Get().Notarization.ForceCommitUntil(commitUntilSlot); err != nil {
		return nil, iotago.EmptyBlockID, ierrors.Wrapf(err, "failed to force commit until slot %d", commitUntilSlot)
	}

	commitment, err := node.Protocol.Engines.Main.Get().Storage.Commitments().Load(commitUntilSlot)
	if err != nil {
		return nil, iotago.EmptyBlockID, ierrors.Wrapf(err, "failed to commit until slot %d to revive chain", commitUntilSlot)
	}

	// Get a rootblock after force committing for the parent. This is necessary, as we might accept "pending accepted"
	// blocks when force committing so slots are not effectively empty.
	parentBlockID := lo.Return1(node.Protocol.Engines.Main.Get().EvictionState.LatestActiveRootBlock())

	return commitment.Commitment(), parentBlockID, nil
}
