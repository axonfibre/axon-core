package drr

import (
	"go.uber.org/atomic"

	"github.com/iotaledger/hive.go/ds/shrinkingmap"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
	iotago "github.com/iotaledger/iota.go/v4"
)

type ValidatorBuffer struct {
	buffer *shrinkingmap.ShrinkingMap[iotago.AccountID, *ValidatorQueue]
	size   atomic.Int64
}

func NewValidatorBuffer() *ValidatorBuffer {
	return &ValidatorBuffer{
		buffer: shrinkingmap.New[iotago.AccountID, *ValidatorQueue](),
	}
}

func (b *ValidatorBuffer) Size() int {
	if b == nil {
		return 0
	}

	return int(b.size.Load())
}

func (b *ValidatorBuffer) Get(accountID iotago.AccountID) (*ValidatorQueue, bool) {
	return b.buffer.Get(accountID)
}

func (b *ValidatorBuffer) GetOrCreate(accountID iotago.AccountID, onCreateCallback func(*ValidatorQueue)) *ValidatorQueue {
	return b.buffer.Compute(accountID, func(currentValue *ValidatorQueue, exists bool) *ValidatorQueue {
		if exists {
			return currentValue
		}

		queue := NewValidatorQueue(accountID, func(totalSizeDelta int64) {
			b.size.Add(totalSizeDelta)
		})
		if onCreateCallback != nil {
			onCreateCallback(queue)
		}

		return queue
	})
}

// Ready marks a previously submitted block as ready to be scheduled.
func (b *ValidatorBuffer) Ready(block *blocks.Block) {
	if validatorQueue, exists := b.Get(block.IssuerID()); exists {
		validatorQueue.Ready(block)
	}
}

// ForEachValidatorQueue iterates over all validator queues.
func (b *ValidatorBuffer) ForEachValidatorQueue(consumer func(accountID iotago.AccountID, validatorQueue *ValidatorQueue) bool) {
	b.buffer.ForEach(func(accountID iotago.AccountID, validatorQueue *ValidatorQueue) bool {
		return consumer(accountID, validatorQueue)
	})
}

// Delete removes all validator queues that match the predicate.
func (b *ValidatorBuffer) Delete(predicate func(element *ValidatorQueue) bool) {
	b.buffer.ForEach(func(accountID iotago.AccountID, validatorQueue *ValidatorQueue) bool {
		if predicate(validatorQueue) {
			// validator workers need to be shut down first, otherwise they will hang on the shutdown channel.
			validatorQueue.Shutdown()
			b.buffer.Delete(accountID)
		}

		return true
	})
}

func (b *ValidatorBuffer) Clear() {
	b.Delete(func(_ *ValidatorQueue) bool {
		// remove all
		return true
	})
}
