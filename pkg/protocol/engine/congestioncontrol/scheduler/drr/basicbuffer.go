package drr

import (
	"container/ring"
	"fmt"
	"math"
	"time"

	"go.uber.org/atomic"

	"github.com/iotaledger/hive.go/ds/shrinkingmap"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"

	iotago "github.com/iotaledger/iota.go/v4"
)

// region BasicBuffer /////////////////////////////////////////////////////////////////////////////////////////////

// BasicBuffer represents a buffer of IssuerQueue.
type BasicBuffer struct {
	activeIssuers *shrinkingmap.ShrinkingMap[iotago.AccountID, *ring.Ring]
	ring          *ring.Ring

	readyBlocksCount atomic.Int64
	totalBlocksCount atomic.Int64

	tokenBucket      float64
	lastScheduleTime time.Time

	blockChan chan *blocks.Block
}

// NewBasicBuffer returns a new BasicBuffer.
func NewBasicBuffer() *BasicBuffer {
	return &BasicBuffer{
		activeIssuers:    shrinkingmap.New[iotago.AccountID, *ring.Ring](),
		ring:             nil,
		lastScheduleTime: time.Now(),
		blockChan:        make(chan *blocks.Block, 1),
	}
}

// NumActiveIssuers returns the number of active issuers in b.
func (b *BasicBuffer) NumActiveIssuers() int {
	return b.activeIssuers.Size()
}

func (b *BasicBuffer) Clear() {
	select {
	case <-b.blockChan:
	default:
	}

	b.activeIssuers.ForEachKey(func(issuedID iotago.AccountID) bool {
		b.RemoveIssuerQueue(issuedID)

		return true
	})
}

// IssuerQueue returns the queue for the corresponding issuer.
func (b *BasicBuffer) IssuerQueue(issuerID iotago.AccountID) *IssuerQueue {
	element, exists := b.activeIssuers.Get(issuerID)
	if !exists {
		return nil
	}
	issuerQueue, isIQ := element.Value.(*IssuerQueue)
	if !isIQ {
		panic("buffer contains elements that are not issuer queues")
	}

	return issuerQueue
}

// IssuerQueueWork returns the total WorkScore of block in the queue for the corresponding issuer.
func (b *BasicBuffer) IssuerQueueWork(issuerID iotago.AccountID) iotago.WorkScore {
	issuerQueue := b.IssuerQueue(issuerID)
	if issuerQueue == nil {
		return 0
	}

	return issuerQueue.Work()
}

// IssuerQueueSize returns the number of blocks in the queue for the corresponding issuer.
func (b *BasicBuffer) IssuerQueueBlockCount(issuerID iotago.AccountID) int {
	issuerQueue := b.IssuerQueue(issuerID)
	if issuerQueue == nil {
		return 0
	}

	return issuerQueue.Size()
}

func (b *BasicBuffer) CreateIssuerQueue(issuerID iotago.AccountID) *IssuerQueue {
	element := b.activeIssuers.Compute(issuerID, func(_ *ring.Ring, exists bool) *ring.Ring {
		if exists {
			panic(fmt.Sprintf("issuer queue already exists: %s", issuerID.String()))
		}

		return b.ringInsert(NewIssuerQueue(issuerID, func(totalSizeDelta int64, readySizeDelta int64) {
			if totalSizeDelta != 0 {
				b.totalBlocksCount.Add(totalSizeDelta)
			}
			if readySizeDelta != 0 {
				b.readyBlocksCount.Add(readySizeDelta)
			}
		}))
	})

	issuerQueue, isIQ := element.Value.(*IssuerQueue)
	if !isIQ {
		panic("buffer contains elements that are not issuer queues")
	}

	return issuerQueue
}

func (b *BasicBuffer) GetOrCreateIssuerQueue(issuerID iotago.AccountID) *IssuerQueue {
	element, issuerActive := b.activeIssuers.Get(issuerID)
	if !issuerActive {
		// create new issuer queue
		return b.CreateIssuerQueue(issuerID)
	}
	issuerQueue, isIQ := element.Value.(*IssuerQueue)
	if !isIQ {
		panic("buffer contains elements that are not issuer queues")
	}

	return issuerQueue
}

// RemoveIssuerQueue removes all blocks (submitted and ready) for the given issuer and deletes the issuer queue.
func (b *BasicBuffer) RemoveIssuerQueue(issuerID iotago.AccountID) {
	element, exists := b.activeIssuers.Get(issuerID)
	if !exists {
		return
	}
	issuerQueue, isIQ := element.Value.(*IssuerQueue)
	if !isIQ {
		panic("buffer contains elements that are not issuer queues")
	}
	issuerQueue.Clear()

	b.ringRemove(element)
	b.activeIssuers.Delete(issuerID)
}

// RemoveIssuerQueueIfEmpty removes all blocks (submitted and ready) for the given issuer and deletes the issuer queue if it is empty.
func (b *BasicBuffer) RemoveIssuerQueueIfEmpty(issuerID iotago.AccountID) {
	element, exists := b.activeIssuers.Get(issuerID)
	if !exists {
		return
	}
	issuerQueue, isIQ := element.Value.(*IssuerQueue)
	if !isIQ {
		panic("buffer contains elements that are not issuer queues")
	}

	if issuerQueue.Size() == 0 {
		b.ringRemove(element)
		b.activeIssuers.Delete(issuerID)
	}
}

// Submit submits a block. Return blocks dropped from the scheduler to make room for the submitted block.
// The submitted block can also be returned as dropped if the issuer does not have enough mana.
func (b *BasicBuffer) Submit(blk *blocks.Block, issuerQueue *IssuerQueue, quantumFunc func(iotago.AccountID) Deficit, maxBuffer int) ([]*blocks.Block, bool) {
	// first we submit the block, and if it turns out that the issuer doesn't have enough bandwidth to submit, it will be removed by dropTail
	if !issuerQueue.Submit(blk) {
		return nil, false
	}

	// if max buffer size exceeded, drop from tail of the longest mana-scaled queue
	if b.TotalBlocksCount() > maxBuffer {
		return b.dropTail(quantumFunc, maxBuffer), true
	}

	return nil, true
}

// Ready marks a previously submitted block as ready to be scheduled.
func (b *BasicBuffer) Ready(block *blocks.Block) bool {
	issuerQueue := b.IssuerQueue(block.IssuerID())
	if issuerQueue == nil {
		return false
	}

	return issuerQueue.Ready(block)
}

// TotalBlocksCount returns the number of blocks in the buffer.
func (b *BasicBuffer) TotalBlocksCount() (blocksCount int) {
	return int(b.totalBlocksCount.Load())
}

// ReadyBlocksCount returns the number of ready blocks in the buffer.
func (b *BasicBuffer) ReadyBlocksCount() (readyBlocksCount int) {
	return int(b.readyBlocksCount.Load())
}

// Next returns the next IssuerQueue in round-robin order.
func (b *BasicBuffer) Next() *IssuerQueue {
	if b.ring != nil {
		b.ring = b.ring.Next()
		if issuerQueue, isIQ := b.ring.Value.(*IssuerQueue); isIQ {
			return issuerQueue
		}
	}

	return nil
}

// ForEach applies a consumer function to each IssuerQueue in the BasicBuffer.
func (b *BasicBuffer) ForEach(consumer func(*IssuerQueue) bool) {
	if b.ring == nil {
		return
	}

	// Create a temporary slice to hold the IssuerQueues
	var queues []*IssuerQueue

	// Start at the current ring position
	start := b.ring

	for {
		if issuerQueue, isIQ := b.ring.Value.(*IssuerQueue); isIQ {
			queues = append(queues, issuerQueue)
		}

		// Move to the next position in the ring
		b.ring = b.ring.Next()

		// If we've looped back to the start, break out of the loop
		if b.ring == start {
			break
		}
	}

	// Apply the consumer function to each IssuerQueue
	for _, queue := range queues {
		if !consumer(queue) {
			return
		}
	}
}

// Current returns the current IssuerQueue in round-robin order.
func (b *BasicBuffer) Current() *IssuerQueue {
	if b.ring == nil {
		return nil
	}
	if issuerQueue, isIQ := b.ring.Value.(*IssuerQueue); isIQ {
		return issuerQueue
	}

	return nil
}

// PopFront removes the first ready block from the queue of the current issuer.
func (b *BasicBuffer) PopFront() *blocks.Block {
	q := b.Current()
	if q == nil {
		return nil
	}

	block := q.PopFront()
	if block == nil {
		return nil
	}

	return block
}

// IssuerIDs returns the issuerIDs of all issuers.
func (b *BasicBuffer) IssuerIDs() []iotago.AccountID {
	var issuerIDs []iotago.AccountID
	start := b.Current()
	if start == nil {
		return nil
	}
	for q := start; ; {
		issuerIDs = append(issuerIDs, q.IssuerID())
		q = b.Next()
		if q == start {
			break
		}
	}

	return issuerIDs
}

func (b *BasicBuffer) dropTail(quantumFunc func(iotago.AccountID) Deficit, maxBuffer int) (droppedBlocks []*blocks.Block) {
	// remove as many blocks as necessary to stay within max buffer size
	for b.TotalBlocksCount() > maxBuffer {
		// find the longest mana-scaled queue
		maxIssuerID := b.mustLongestQueueIssuerID(quantumFunc)
		longestQueue := b.IssuerQueue(maxIssuerID)
		if longestQueue == nil {
			panic("buffer is full, but longest queue does not exist")
		}

		tail := longestQueue.RemoveTail()
		if tail == nil {
			panic("buffer is full, but tail of longest queue does not exist")
		}

		droppedBlocks = append(droppedBlocks, tail)
	}

	return droppedBlocks
}

// mustLongestQueueIssuerID returns the issuerID of the longest queue in the buffer.
// This function panics if no longest queue is found.
func (b *BasicBuffer) mustLongestQueueIssuerID(quantumFunc func(iotago.AccountID) Deficit) iotago.AccountID {
	start := b.Current()
	ringStart := b.ring
	maxScale := math.Inf(-1)
	maxIssuerID := iotago.EmptyAccountID
	for q := start; ; {
		if issuerQuantum := quantumFunc(q.IssuerID()); issuerQuantum > 0 {
			if scale := float64(q.Work()) / float64(issuerQuantum); scale > maxScale {
				maxScale = scale
				maxIssuerID = q.IssuerID()
			}
		} else if q.Size() > 0 {
			// if the issuer has no quantum, then this is the max queue size
			maxIssuerID = q.IssuerID()
			b.ring = ringStart

			break
		}
		q = b.Next()
		if q == start {
			break
		}
	}

	if maxIssuerID == iotago.EmptyAccountID {
		panic("no longest queue determined")
	}

	return maxIssuerID
}

func (b *BasicBuffer) ringRemove(r *ring.Ring) {
	n := b.ring.Next()
	if r == b.ring {
		if n == b.ring {
			b.ring = nil
			return
		}
		b.ring = n
	}
	r.Prev().Link(n)
}

func (b *BasicBuffer) ringInsert(v interface{}) *ring.Ring {
	p := ring.New(1)
	p.Value = v
	if b.ring == nil {
		b.ring = p
		return p
	}

	return p.Link(b.ring)
}

func (b *BasicBuffer) waitTime(rate float64, block *blocks.Block) time.Duration {
	tokensRequired := float64(block.WorkScore()) - (b.tokenBucket + rate*time.Since(b.lastScheduleTime).Seconds())

	return lo.Max(0, time.Duration(tokensRequired/rate)*time.Second)
}

func (b *BasicBuffer) updateTokenBucket(rate float64, tokenBucketSize float64) {
	b.tokenBucket = lo.Min(
		tokenBucketSize,
		b.tokenBucket+rate*time.Since(b.lastScheduleTime).Seconds(),
	)
	b.lastScheduleTime = time.Now()
}

func (b *BasicBuffer) deductTokens(tokens float64) {
	b.tokenBucket -= tokens
}

// endregion ///////////////////////////////////////////////////////////////////////////////////////////////////////////
