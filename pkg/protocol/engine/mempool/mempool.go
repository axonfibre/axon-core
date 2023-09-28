package mempool

import (
	"github.com/iotaledger/hive.go/runtime/event"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/mempool/conflictdag"
	iotago "github.com/iotaledger/iota.go/v4"
)

type MemPool[VoteRank conflictdag.VoteRankType[VoteRank]] interface {
	AttachTransaction(transaction Transaction, blockID iotago.BlockID) (storedTransaction TransactionMetadata, err error)

	OnTransactionAttached(callback func(metadata TransactionMetadata), opts ...event.Option)

	MarkAttachmentOrphaned(blockID iotago.BlockID) bool

	MarkAttachmentIncluded(blockID iotago.BlockID) bool

	OutputStateMetadata(reference *iotago.UTXOInput) (state OutputStateMetadata, err error)

	TransactionMetadata(id iotago.TransactionID) (transaction TransactionMetadata, exists bool)

	PublishCommitmentState(commitment *iotago.Commitment)

	TransactionMetadataByAttachment(blockID iotago.BlockID) (transaction TransactionMetadata, exists bool)

	StateDiff(slot iotago.SlotIndex) StateDiff

	Evict(slot iotago.SlotIndex)
}
