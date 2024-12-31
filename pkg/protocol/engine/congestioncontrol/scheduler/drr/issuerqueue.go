package drr

import (
	"container/heap"
	"fmt"

	"go.uber.org/atomic"

	"github.com/iotaledger/hive.go/ds/generalheap"
	"github.com/iotaledger/hive.go/ds/shrinkingmap"
	"github.com/iotaledger/hive.go/runtime/timed"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"

	iotago "github.com/iotaledger/iota.go/v4"
)

// region IssuerQueue /////////////////////////////////////////////////////////////////////////////////////////////

// IssuerQueue keeps the submitted blocks of an issuer.
type IssuerQueue struct {
	issuerID        iotago.AccountID
	sizeChangedFunc func(totalSizeDelta int64, readySizeDelta int64, workDelta int64)

	nonReadyMap *shrinkingmap.ShrinkingMap[iotago.BlockID, *blocks.Block]
	readyHeap   generalheap.Heap[timed.HeapKey, *blocks.Block]

	size atomic.Int64
	work atomic.Int64
}

// NewIssuerQueue returns a new IssuerQueue.
func NewIssuerQueue(issuerID iotago.AccountID, sizeChangedCallback func(totalSizeDelta int64, readySizeDelta int64)) *IssuerQueue {
	queue := &IssuerQueue{
		issuerID:    issuerID,
		nonReadyMap: shrinkingmap.New[iotago.BlockID, *blocks.Block](),
	}

	queue.sizeChangedFunc = func(totalSizeDelta int64, readySizeDelta int64, workDelta int64) {
		if totalSizeDelta != 0 {
			queue.size.Add(totalSizeDelta)
		}
		if workDelta != 0 {
			queue.work.Add(workDelta)
		}

		if sizeChangedCallback != nil {
			sizeChangedCallback(totalSizeDelta, readySizeDelta)
		}
	}

	return queue
}

// Size returns the total number of blocks in the queue.
// This function is thread-safe.
func (q *IssuerQueue) Size() int {
	if q == nil {
		return 0
	}

	return int(q.size.Load())
}

// Work returns the total work of the blocks in the queue.
// This function is thread-safe.
func (q *IssuerQueue) Work() iotago.WorkScore {
	if q == nil {
		return 0
	}

	return iotago.WorkScore(q.work.Load())
}

// IssuerID returns the ID of the issuer belonging to the queue.
func (q *IssuerQueue) IssuerID() iotago.AccountID {
	return q.issuerID
}

// Submit submits a block for the queue. Return true if submitted and false if it was already submitted.
func (q *IssuerQueue) Submit(element *blocks.Block) bool {
	// this is just a debugging check, it should never happen in practice and we should panic if it does.
	if blkIssuerID := element.IssuerID(); q.issuerID != blkIssuerID {
		panic(fmt.Sprintf("issuerqueue: queue issuer ID(%x) and issuer ID(%x) does not match.", q.issuerID, blkIssuerID))
	}

	if _, submitted := q.nonReadyMap.Get(element.ID()); submitted {
		return false
	}

	q.nonReadyMap.Set(element.ID(), element)
	q.sizeChangedFunc(1, 0, int64(element.WorkScore()))

	return true
}

// unsubmit removes a previously submitted block from the queue.
func (q *IssuerQueue) unsubmit(block *blocks.Block) bool {
	if _, submitted := q.nonReadyMap.Get(block.ID()); !submitted {
		return false
	}

	q.nonReadyMap.Delete(block.ID())
	q.sizeChangedFunc(-1, 0, -int64(block.WorkScore()))

	return true
}

// Ready marks a previously submitted block as ready to be scheduled.
func (q *IssuerQueue) Ready(block *blocks.Block) bool {
	if _, submitted := q.nonReadyMap.Get(block.ID()); !submitted {
		return false
	}

	q.nonReadyMap.Delete(block.ID())
	heap.Push(&q.readyHeap, &generalheap.HeapElement[timed.HeapKey, *blocks.Block]{Value: block, Key: timed.HeapKey(block.IssuingTime())})

	q.sizeChangedFunc(0, 1, 0)

	return true
}

// IDs returns the IDs of all submitted blocks (ready or not).
func (q *IssuerQueue) IDs() (ids []iotago.BlockID) {
	ids = q.nonReadyMap.Keys()

	for _, block := range q.readyHeap {
		ids = append(ids, block.Value.ID())
	}

	return ids
}

// Clear removes all blocks from the queue.
func (q *IssuerQueue) Clear() {
	readyBlocksCount := int64(q.readyHeap.Len())

	q.nonReadyMap.Clear()
	for q.readyHeap.Len() > 0 {
		_ = q.readyHeap.Pop()
	}

	q.sizeChangedFunc(-int64(q.Size()), -readyBlocksCount, -int64(q.Work()))
}

// Front returns the first ready block in the queue.
func (q *IssuerQueue) Front() *blocks.Block {
	if q == nil || q.readyHeap.Len() == 0 {
		return nil
	}

	return q.readyHeap[0].Value
}

// PopFront removes the first ready block from the queue.
func (q *IssuerQueue) PopFront() *blocks.Block {
	if q.readyHeap.Len() == 0 {
		return nil
	}

	heapElement, isHeapElement := heap.Pop(&q.readyHeap).(*generalheap.HeapElement[timed.HeapKey, *blocks.Block])
	if !isHeapElement {
		panic("unable to pop from a non-empty heap.")
	}
	blk := heapElement.Value
	q.sizeChangedFunc(-1, -1, -int64(blk.WorkScore()))

	return blk
}

// RemoveTail removes the oldest block from the queue.
func (q *IssuerQueue) RemoveTail() *blocks.Block {
	var oldestNonReadyBlock *blocks.Block
	q.nonReadyMap.ForEach(func(_ iotago.BlockID, block *blocks.Block) bool {
		if oldestNonReadyBlock == nil || oldestNonReadyBlock.IssuingTime().After(block.IssuingTime()) {
			oldestNonReadyBlock = block
		}

		return true
	})

	heapTailIndex := q.heapTail()
	// if heap tail (oldest ready block) does not exist or is newer than oldest non-ready block, unsubmit the oldest non-ready block
	if oldestNonReadyBlock != nil && (heapTailIndex < 0 || q.readyHeap[heapTailIndex].Key.CompareTo(timed.HeapKey(oldestNonReadyBlock.IssuingTime())) > 0) {
		if q.unsubmit(oldestNonReadyBlock) {
			return oldestNonReadyBlock
		}
	} else if heapTailIndex < 0 { // the heap is empty
		// should never happen that the oldest submitted block does not exist and the heap is empty.
		panic("heap tail and oldest submitted block do not exist. Trying to remove tail of an empty queue?")
	}

	// if the oldest ready block is older than the oldest non-ready block, drop it
	heapElement, isHeapElement := heap.Remove(&q.readyHeap, heapTailIndex).(*generalheap.HeapElement[timed.HeapKey, *blocks.Block])
	if !isHeapElement {
		panic("trying to remove a heap element that does not exist.")
	}
	blk := heapElement.Value
	q.sizeChangedFunc(-1, -1, -int64(blk.WorkScore()))

	return blk
}

func (q *IssuerQueue) heapTail() int {
	h := q.readyHeap
	if h.Len() <= 0 {
		return -1
	}
	tail := 0
	for i := range h {
		if !h.Less(i, tail) { // less means older issue time
			tail = i
		}
	}

	return tail
}

// endregion ///////////////////////////////////////////////////////////////////////////////////////////////////////////
