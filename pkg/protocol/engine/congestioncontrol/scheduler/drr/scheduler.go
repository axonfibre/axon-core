package drr

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iotaledger/hive.go/core/safemath"
	"github.com/iotaledger/hive.go/ds/shrinkingmap"
	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/runtime/module"
	"github.com/iotaledger/hive.go/runtime/options"
	"github.com/iotaledger/hive.go/runtime/syncutils"
	"github.com/iotaledger/iota-core/pkg/model"
	"github.com/iotaledger/iota-core/pkg/protocol/engine"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/congestioncontrol/scheduler"
	"github.com/iotaledger/iota-core/pkg/protocol/sybilprotection/seatmanager"
	iotago "github.com/iotaledger/iota.go/v4"
)

type Deficit int64

type SubSlotIndex int

type Scheduler struct {
	events *scheduler.Events

	quantumFunc func(iotago.AccountID, iotago.SlotIndex) (Deficit, error)

	latestCommittedSlot func() iotago.SlotIndex

	apiProvider iotago.APIProvider

	seatManager seatmanager.SeatManager

	basicBuffer     *BasicBuffer
	validatorBuffer *ValidatorBuffer
	bufferMutex     syncutils.RWMutex

	deficits *shrinkingmap.ShrinkingMap[iotago.AccountID, Deficit]

	workersWg      sync.WaitGroup
	shutdownSignal chan struct{}
	// isShutdown is true if the scheduler was shutdown.
	isShutdown atomic.Bool

	blockCache *blocks.Blocks

	errorHandler func(error)

	module.Module
}

func NewProvider(opts ...options.Option[Scheduler]) module.Provider[*engine.Engine, scheduler.Scheduler] {
	return module.Provide(func(e *engine.Engine) scheduler.Scheduler {
		s := New(e.NewSubModule("Scheduler"), e, opts...)
		s.errorHandler = e.ErrorHandler("scheduler")
		s.basicBuffer = NewBasicBuffer()

		e.ConstructedEvent().OnTrigger(func() {
			s.latestCommittedSlot = func() iotago.SlotIndex {
				return e.SyncManager.LatestCommitment().Slot()
			}
			s.blockCache = e.BlockCache
			e.SybilProtection.InitializedEvent().OnTrigger(func() {
				s.seatManager = e.SybilProtection.SeatManager()
			})
			e.Events.Notarization.LatestCommitmentUpdated.Hook(func(commitment *model.Commitment) {
				// when the last slot of an epoch is committed, remove the queues of validators that are no longer in the committee.
				if s.apiProvider.APIForSlot(commitment.Slot()).TimeProvider().SlotsBeforeNextEpoch(commitment.Slot()) == 0 {
					if s.IsShutdown() {
						// if the scheduler is already shutdown, we don't need to do anything.
						return
					}

					s.bufferMutex.Lock()
					defer s.bufferMutex.Unlock()

					committee, exists := s.seatManager.CommitteeInSlot(commitment.Slot() + 1)
					if !exists {
						s.errorHandler(ierrors.Errorf("committee does not exist in committed slot %d", commitment.Slot()+1))

						return
					}

					s.validatorBuffer.Delete(func(validatorQueue *ValidatorQueue) bool {
						return !committee.HasAccount(validatorQueue.AccountID())
					})
				}
			})
			e.Ledger.InitializedEvent().OnTrigger(func() {
				// quantum retrieve function gets the account's Mana and returns the quantum for that account
				s.quantumFunc = func(accountID iotago.AccountID, manaSlot iotago.SlotIndex) (Deficit, error) {
					mana, err := e.Ledger.ManaManager().GetManaOnAccount(accountID, manaSlot)
					if err != nil {
						return 0, err
					}

					mana = lo.Min(mana, iotago.Mana(s.maxDeficit()-1))

					return 1 + Deficit(mana), nil
				}
			})
			e.Events.Booker.BlockBooked.Hook(func(block *blocks.Block) {
				s.AddBlock(block)
				s.selectBlockToScheduleWithLocking()
			})
			e.Events.Ledger.AccountCreated.Hook(func(accountID iotago.AccountID) {
				if s.IsShutdown() {
					// if the scheduler is already shutdown, we don't need to do anything.
					return
				}

				s.bufferMutex.Lock()
				defer s.bufferMutex.Unlock()

				s.createIssuer(accountID)
			})
			e.Events.Ledger.AccountDestroyed.Hook(func(accountID iotago.AccountID) {
				if s.IsShutdown() {
					// if the scheduler is already shutdown, we don't need to do anything.
					return
				}

				s.bufferMutex.Lock()
				defer s.bufferMutex.Unlock()

				s.removeIssuer(accountID, ierrors.New("account destroyed"))
			})

			e.InitializedEvent().OnTrigger(s.Start)

			e.Events.Scheduler.LinkTo(s.events)

			s.InitializedEvent().Trigger()
		})

		return s
	})
}

func New(subModule module.Module, apiProvider iotago.APIProvider, opts ...options.Option[Scheduler]) *Scheduler {
	return options.Apply(
		&Scheduler{
			Module:          subModule,
			events:          scheduler.NewEvents(),
			deficits:        shrinkingmap.New[iotago.AccountID, Deficit](),
			apiProvider:     apiProvider,
			validatorBuffer: NewValidatorBuffer(),
		}, opts, func(s *Scheduler) {
			s.ShutdownEvent().OnTrigger(s.shutdown)

			s.ConstructedEvent().Trigger()
		},
	)
}

func (s *Scheduler) shutdown() {
	if s.isShutdown.Swap(true) {
		return
	}

	s.bufferMutex.Lock()
	s.validatorBuffer.Clear()
	s.bufferMutex.Unlock()

	close(s.shutdownSignal)

	s.workersWg.Wait()

	s.StoppedEvent().Trigger()
}

func (s *Scheduler) IsShutdown() bool {
	return s.isShutdown.Load()
}

// Start starts the scheduler.
func (s *Scheduler) Start() {
	s.shutdownSignal = make(chan struct{}, 1)

	s.workersWg.Add(1)
	go s.basicBlockLoop()

	s.InitializedEvent().Trigger()
}

// IssuerQueueBlockCount returns the number of blocks in the queue of the given issuer.
func (s *Scheduler) IssuerQueueBlockCount(issuerID iotago.AccountID) int {
	return s.basicBuffer.IssuerQueueBlockCount(issuerID)
}

// IssuerQueueWork returns the queue size of the given issuer in work units.
func (s *Scheduler) IssuerQueueWork(issuerID iotago.AccountID) iotago.WorkScore {
	return s.basicBuffer.IssuerQueueWork(issuerID)
}

// ValidatorQueueBlockCount returns the number of validation blocks in the validator queue of the given issuer.
func (s *Scheduler) ValidatorQueueBlockCount(issuerID iotago.AccountID) int {
	validatorQueue, exists := s.validatorBuffer.Get(issuerID)
	if !exists {
		return 0
	}

	return validatorQueue.Size()
}

// BasicBufferSize returns the current buffer size of the Scheduler as block count.
func (s *Scheduler) BasicBufferSize() int {
	return s.basicBuffer.TotalBlocksCount()
}

func (s *Scheduler) ValidatorBufferSize() int {
	return s.validatorBuffer.Size()
}

// MaxBufferSize returns the max buffer size of the Scheduler as block count.
func (s *Scheduler) MaxBufferSize() int {
	return int(s.apiProvider.CommittedAPI().ProtocolParameters().CongestionControlParameters().MaxBufferSize)
}

// ReadyBlocksCount returns the number of ready blocks.
func (s *Scheduler) ReadyBlocksCount() int {
	if s.IsShutdown() {
		// if the scheduler is already shutdown, we return 0.
		return 0
	}

	s.bufferMutex.RLock()
	defer s.bufferMutex.RUnlock()

	return s.basicBuffer.ReadyBlocksCount()
}

func (s *Scheduler) IsBlockIssuerReady(accountID iotago.AccountID, workScores ...iotago.WorkScore) bool {
	if s.IsShutdown() {
		// if the scheduler is already shutdown, we return false.
		return false
	}

	s.bufferMutex.RLock()
	defer s.bufferMutex.RUnlock()

	// if the buffer is completely empty, any issuer can issue a block.
	if s.basicBuffer.TotalBlocksCount() == 0 {
		return true
	}

	var work iotago.WorkScore
	if len(workScores) > 0 {
		for _, workScore := range workScores {
			work += workScore
		}
	} else {
		// if no specific work score is provided, assume max block work score.
		work = s.apiProvider.CommittedAPI().MaxBlockWork()
	}

	deficit, exists := s.deficits.Get(accountID)
	if !exists {
		return false
	}

	return deficit >= s.deficitFromWork(work+s.basicBuffer.IssuerQueueWork(accountID))
}

func (s *Scheduler) AddBlock(block *blocks.Block) {
	if block.IsValidationBlock() {
		s.enqueueValidationBlock(block)
	} else if block.IsBasicBlock() {
		s.enqueueBasicBlock(block)
	}
}

// Reset resets the component to a clean state as if it was created at the last commitment.
func (s *Scheduler) Reset() {
	if s.IsShutdown() {
		// if the scheduler is already shutdown, we don't need to do anything.
		return
	}

	s.bufferMutex.Lock()
	defer s.bufferMutex.Unlock()

	s.basicBuffer.Clear()
	s.validatorBuffer.Clear()
}

func (s *Scheduler) enqueueBasicBlock(block *blocks.Block) {
	if s.IsShutdown() {
		// if the scheduler is already shutdown, we don't need to do anything.
		return
	}

	s.bufferMutex.Lock()
	defer s.bufferMutex.Unlock()

	slot := s.latestCommittedSlot()

	issuerID := block.IssuerID()
	issuerQueue := s.getOrCreateIssuer(issuerID)

	droppedBlocks, submitted := s.basicBuffer.Submit(
		block,
		issuerQueue,
		func(issuerID iotago.AccountID) Deficit {
			quantum, quantumErr := s.quantumFunc(issuerID, slot)
			if quantumErr != nil {
				s.errorHandler(ierrors.Wrapf(quantumErr, "failed to retrieve deficit for issuerID %s in slot %d when submitting a block", issuerID, slot))

				return 0
			}

			return quantum
		},
		int(s.apiProvider.CommittedAPI().ProtocolParameters().CongestionControlParameters().MaxBufferSize),
	)
	// error submitting indicates that the block was already submitted so we do nothing else.
	if !submitted {
		return
	}
	for _, b := range droppedBlocks {
		b.SetDropped()
		s.events.BlockDropped.Trigger(b, ierrors.New("basic block dropped from buffer"))
	}
	if block.SetEnqueued() {
		s.events.BlockEnqueued.Trigger(block)
		s.tryReady(block)
	}
}

func (s *Scheduler) enqueueValidationBlock(block *blocks.Block) {
	if s.IsShutdown() {
		// if the scheduler is already shutdown, we don't need to do anything.
		return
	}

	s.bufferMutex.Lock()
	defer s.bufferMutex.Unlock()

	droppedBlock, submitted := s.getOrCreateValidatorQueue(block.IssuerID()).Submit(block, int(s.apiProvider.CommittedAPI().ProtocolParameters().CongestionControlParameters().MaxValidationBufferSize))
	if !submitted {
		return
	}
	if droppedBlock != nil {
		droppedBlock.SetDropped()
		s.events.BlockDropped.Trigger(droppedBlock, ierrors.New("validation block dropped from buffer"))
	}

	if block.SetEnqueued() {
		s.events.BlockEnqueued.Trigger(block)
		s.tryReadyValidationBlock(block)
	}
}

func (s *Scheduler) basicBlockLoop() {
	defer s.workersWg.Done()
	var blockToSchedule *blocks.Block
loop:
	for {
		select {
		// on close, exit the loop
		case <-s.shutdownSignal:
			break loop
		// when a block is pushed by the buffer
		case blockToSchedule = <-s.basicBuffer.blockChan:
			currentAPI := s.apiProvider.CommittedAPI()
			rate := currentAPI.ProtocolParameters().CongestionControlParameters().SchedulerRate
			for waitTime := s.basicBuffer.waitTime(float64(rate), blockToSchedule); waitTime > 0; {
				timer := time.NewTimer(waitTime)
				<-timer.C
			}

			s.basicBuffer.updateTokenBucket(float64(rate), float64(currentAPI.MaxBlockWork()))

			s.scheduleBasicBlock(blockToSchedule)
		}
	}
}

func (s *Scheduler) validatorLoop(validatorQueue *ValidatorQueue) {
	defer s.workersWg.Done()
	var blockToSchedule *blocks.Block
loop:
	for {
		select {
		// on close, exit the loop
		case <-s.shutdownSignal:
			break loop
		// on close, exit the loop
		case <-validatorQueue.shutdownSignal:
			break loop
		// when a block is pushed by this validator queue.
		case blockToSchedule = <-validatorQueue.blockChan:
			currentAPI := s.apiProvider.CommittedAPI()
			validationBlocksPerSlot := float64(currentAPI.ProtocolParameters().ValidationBlocksPerSlot())
			rate := validationBlocksPerSlot / float64(currentAPI.TimeProvider().SlotDurationSeconds())
			if waitTime := validatorQueue.waitTime(rate); waitTime > 0 {
				timer := time.NewTimer(waitTime)
				<-timer.C
			}
			// allow a maximum burst of validationBlocksPerSlot by setting this as max token bucket size.
			validatorQueue.updateTokenBucket(rate, validationBlocksPerSlot)

			s.scheduleValidationBlock(blockToSchedule, validatorQueue)
		}
	}
}

func (s *Scheduler) scheduleBasicBlock(block *blocks.Block) {
	if block.SetScheduled() {
		// deduct tokens from the token bucket according to the scheduled block's work.
		s.basicBuffer.deductTokens(float64(block.WorkScore()))

		// check for another block ready to schedule
		s.updateChildrenWithLocking(block)
		s.selectBlockToScheduleWithLocking()

		s.events.BlockScheduled.Trigger(block)
	}
}

func (s *Scheduler) scheduleValidationBlock(block *blocks.Block, validatorQueue *ValidatorQueue) {
	if block.SetScheduled() {
		// deduct 1 token from the token bucket of this validator's queue.
		validatorQueue.deductTokens(1)

		// check for another block ready to schedule
		s.updateChildrenWithLocking(block)
		s.selectBlockToScheduleWithLocking()

		s.events.BlockScheduled.Trigger(block)
	}
}

func (s *Scheduler) selectBlockToScheduleWithLocking() {
	if s.IsShutdown() {
		// if the scheduler is already shutdown, we don't need to do anything.
		return
	}

	s.bufferMutex.Lock()
	defer s.bufferMutex.Unlock()

	s.validatorBuffer.ForEachValidatorQueue(func(_ iotago.AccountID, validatorQueue *ValidatorQueue) bool {
		validatorQueue.ScheduleNext()
		return true
	})

	s.selectBasicBlockWithoutLocking()
}

func (s *Scheduler) selectBasicBlockWithoutLocking() {
	slot := s.latestCommittedSlot()

	// already a block selected to be scheduled.
	if len(s.basicBuffer.blockChan) > 0 {
		return
	}

	rounds, schedulingIssuer := s.selectIssuer(slot)

	// if there is no issuer with a ready block, we cannot schedule anything
	if schedulingIssuer == nil {
		return
	}

	if rounds > 0 {
		// increment every issuer's deficit for the required number of rounds
		s.basicBuffer.ForEach(func(queue *IssuerQueue) bool {
			issuerID := queue.IssuerID()

			if _, err := s.incrementDeficit(issuerID, rounds, slot); err != nil {
				s.LogPanic(ierrors.Wrapf(err, "failed to increment deficit for issuerID %s in slot %d", issuerID, slot).Error())
			}

			return true
		})
	}

	start := s.basicBuffer.Current()
	if start == nil {
		// if there are no queues in the buffer, we cannot schedule anything
		return
	}

	// increment the deficit for all issuers before schedulingIssuer one more time
	for q := start; q != schedulingIssuer; q = s.basicBuffer.Next() {
		issuerID := q.IssuerID()
		newDeficit, err := s.incrementDeficit(issuerID, 1, slot)
		if err != nil {
			s.LogPanic(ierrors.Wrapf(err, "failed to increment deficit for issuerID %s in slot %d", issuerID, slot).Error())

			return
		}

		// remove empty issuer queues of issuers with max deficit.
		if newDeficit == s.maxDeficit() {
			s.basicBuffer.RemoveIssuerQueueIfEmpty(issuerID)
		}
	}

	// remove the block from the buffer and adjust issuer's deficit
	block := s.basicBuffer.PopFront()
	issuerID := block.IssuerID()
	if _, err := s.updateDeficit(issuerID, -s.deficitFromWork(block.WorkScore())); err != nil {
		// if something goes wrong with deficit update, drop the block instead of scheduling it.
		block.SetDropped()
		s.events.BlockDropped.Trigger(block, err)

		return
	}

	// schedule the block
	s.basicBuffer.blockChan <- block
}

func (s *Scheduler) selectIssuer(slot iotago.SlotIndex) (Deficit, *IssuerQueue) {
	minRounds := Deficit(math.MaxInt64)
	var schedulingIssuer *IssuerQueue

	s.basicBuffer.ForEach(func(queue *IssuerQueue) bool {
		block := queue.Front()

		for block != nil && time.Now().After(block.IssuingTime()) {
			// if the block is already committed, we can skip it.
			if block.IsCommitted() {
				if block.SetSkipped() {
					// the block was accepted and therefore committed already, so we can mark the children as ready.
					s.updateChildrenWithoutLocking(block)
					s.events.BlockSkipped.Trigger(block)
				}

				// remove the skipped block from the queue
				_ = queue.PopFront()

				// take the next block in the queue
				block = queue.Front()

				// continue to check the next block in the queue
				continue
			}

			issuerID := block.IssuerID()

			// compute how often the deficit needs to be incremented until the block can be scheduled
			deficit, exists := s.deficits.Get(issuerID)
			if !exists {
				panic("deficit not found for issuer")
			}

			remainingDeficit := s.deficitFromWork(block.WorkScore()) - deficit

			// calculate how many rounds we need to skip to accumulate enough deficit.
			quantum, err := s.quantumFunc(issuerID, slot)
			if err != nil {
				s.LogPanic(ierrors.Wrapf(err, "failed to retrieve quantum for issuerID %s in slot %d during issuer selection", issuerID, slot).Error())

				break
			}

			numerator, err := safemath.SafeAdd(remainingDeficit, quantum-1)
			if err != nil {
				numerator = math.MaxInt64
			}

			rounds, err := safemath.SafeDiv(numerator, quantum)
			if err != nil {
				panic(err)
			}

			// find the first issuer that will be allowed to schedule a block
			if rounds < minRounds {
				minRounds = rounds
				schedulingIssuer = queue
			}

			break
		}

		// we need to go through all issuers once
		return true
	})

	return minRounds, schedulingIssuer
}

func (s *Scheduler) removeIssuer(issuerID iotago.AccountID, err error) {
	q := s.basicBuffer.IssuerQueue(issuerID)
	if q != nil {
		q.nonReadyMap.ForEach(func(_ iotago.BlockID, block *blocks.Block) bool {
			block.SetDropped()
			s.events.BlockDropped.Trigger(block, err)

			return true
		})

		for i := range q.readyHeap.Len() {
			block := q.readyHeap[i].Value
			block.SetDropped()
			s.events.BlockDropped.Trigger(block, err)
		}
	}

	s.deficits.Delete(issuerID)
	s.basicBuffer.RemoveIssuerQueue(issuerID)
}

func (s *Scheduler) getOrCreateIssuer(accountID iotago.AccountID) *IssuerQueue {
	issuerQueue := s.basicBuffer.GetOrCreateIssuerQueue(accountID)
	s.deficits.GetOrCreate(accountID, func() Deficit { return 0 })

	return issuerQueue
}

func (s *Scheduler) createIssuer(accountID iotago.AccountID) *IssuerQueue {
	issuerQueue := s.basicBuffer.CreateIssuerQueue(accountID)
	s.deficits.Compute(accountID, func(_ Deficit, exists bool) Deficit {
		if exists {
			panic(fmt.Sprintf("issuer already exists: %s", accountID.String()))
		}

		// if the issuer is new, we need to set the deficit to 0.
		return 0
	})

	return issuerQueue
}

func (s *Scheduler) updateDeficit(accountID iotago.AccountID, delta Deficit) (Deficit, error) {
	var updateErr error
	updatedDeficit := s.deficits.Compute(accountID, func(currentValue Deficit, exists bool) Deficit {
		if !exists {
			updateErr = ierrors.Errorf("could not get deficit for issuer %s", accountID)
			return 0
		}
		newDeficit, err := safemath.SafeAdd(currentValue, delta)
		// It can only overflow. We never allow the value to go below 0, so underflow is impossible.
		if err != nil || newDeficit >= s.maxDeficit() {
			return s.maxDeficit()
		}

		// If the new deficit is negative, it could only be a result of subtraction and an error should be returned.
		if newDeficit < 0 {
			updateErr = ierrors.Errorf("deficit for issuer %s decreased below zero", accountID)
			return 0
		}

		return newDeficit
	})

	if updateErr != nil {
		s.LogPanic(updateErr.Error())

		return 0, updateErr
	}

	return updatedDeficit, nil
}

func (s *Scheduler) incrementDeficit(issuerID iotago.AccountID, rounds Deficit, slot iotago.SlotIndex) (Deficit, error) {
	quantum, err := s.quantumFunc(issuerID, slot)
	if err != nil {
		return 0, ierrors.Wrap(err, "failed to retrieve quantum")
	}

	delta, err := safemath.SafeMul(quantum, rounds)
	if err != nil {
		// overflow, set to max deficit
		delta = s.maxDeficit()
	}

	return s.updateDeficit(issuerID, delta)
}

func (s *Scheduler) isEligible(block *blocks.Block) (eligible bool) {
	return block.IsSkipped() || block.IsScheduled() || block.IsAccepted()
}

// isReady returns true if the given blockID's parents are eligible.
func (s *Scheduler) isReady(block *blocks.Block) bool {
	ready := true
	block.ForEachParent(func(parent iotago.Parent) {
		if parentBlock, parentExists := s.blockCache.Block(parent.ID); !parentExists || !s.isEligible(parentBlock) {
			// if parents are evicted and orphaned (not root blocks), or have not been received yet they will not exist.
			// if parents are evicted, they will be returned as root blocks with scheduled==true here.
			ready = false

			return
		}
	})

	return ready
}

// tryReady tries to set the given block as ready.
func (s *Scheduler) tryReady(block *blocks.Block) {
	if s.isReady(block) {
		s.basicBuffer.Ready(block)
	}
}

// tryReadyValidator tries to set the given validation block as ready.
func (s *Scheduler) tryReadyValidationBlock(block *blocks.Block) {
	if s.isReady(block) {
		s.validatorBuffer.Ready(block)
	}
}

// updateChildrenWithLocking locks the buffer mutex and iterates over the direct children of the given blockID and
// tries to mark them as ready.
func (s *Scheduler) updateChildrenWithLocking(block *blocks.Block) {
	if s.IsShutdown() {
		// if the scheduler is already shutdown, we don't need to do anything.
		return
	}

	s.bufferMutex.Lock()
	defer s.bufferMutex.Unlock()

	s.updateChildrenWithoutLocking(block)
}

// updateChildrenWithoutLocking iterates over the direct children of the given blockID and
// tries to mark them as ready.
func (s *Scheduler) updateChildrenWithoutLocking(block *blocks.Block) {
	block.Children().Range(func(childBlock *blocks.Block) {
		if _, childBlockExists := s.blockCache.Block(childBlock.ID()); childBlockExists && childBlock.IsEnqueued() {
			switch {
			case childBlock.IsBasicBlock():
				s.tryReady(childBlock)
			case childBlock.IsValidationBlock():
				s.tryReadyValidationBlock(childBlock)
			default:
				panic("invalid block type")
			}
		}
	})
}

func (s *Scheduler) maxDeficit() Deficit {
	return Deficit(math.MaxInt64 / 2)
}

func (s *Scheduler) deficitFromWork(work iotago.WorkScore) Deficit {
	// max workscore block should occupy the full range of the deficit
	deficitScaleFactor := s.maxDeficit() / Deficit(s.apiProvider.CommittedAPI().MaxBlockWork())
	return Deficit(work) * deficitScaleFactor
}

func (s *Scheduler) getOrCreateValidatorQueue(accountID iotago.AccountID) *ValidatorQueue {
	validatorQueue := s.validatorBuffer.GetOrCreate(accountID, func(queue *ValidatorQueue) {
		s.workersWg.Add(1)
		go s.validatorLoop(queue)
	})

	return validatorQueue
}
