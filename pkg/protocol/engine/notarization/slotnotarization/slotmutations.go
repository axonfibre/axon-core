package slotnotarization

import (
	"sync"

	"github.com/iotaledger/hive.go/ads"
	"github.com/iotaledger/hive.go/ds/shrinkingmap"
	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/kvstore/mapdb"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
	iotago "github.com/iotaledger/iota.go/v4"
)

// SlotMutations is an in-memory data structure that enables the collection of mutations for uncommitted slots.
type SlotMutations struct {
	// acceptedBlocksBySlot stores the accepted blocks per slot.
	acceptedBlocksBySlot *shrinkingmap.ShrinkingMap[iotago.SlotIndex, *ads.Set[iotago.BlockID]]

	// latestCommittedIndex stores the index of the latest committed slot.
	latestCommittedIndex iotago.SlotIndex

	evictionMutex sync.RWMutex
}

// NewSlotMutations creates a new SlotMutations instance.
func NewSlotMutations(lastCommittedSlot iotago.SlotIndex) *SlotMutations {
	return &SlotMutations{
		acceptedBlocksBySlot: shrinkingmap.New[iotago.SlotIndex, *ads.Set[iotago.BlockID]](),
		latestCommittedIndex: lastCommittedSlot,
	}
}

// AddAcceptedBlock adds the given block to the set of accepted blocks.
func (m *SlotMutations) AddAcceptedBlock(block *blocks.Block) (err error) {
	m.evictionMutex.RLock()
	defer m.evictionMutex.RUnlock()

	blockID := block.ID()
	if blockID.Index() <= m.latestCommittedIndex {
		return ierrors.Errorf("cannot add block %s: slot with %d is already committed", blockID, blockID.Index())
	}

	m.AcceptedBlocks(blockID.Index(), true).Add(blockID)

	return
}

// Evict evicts the given slot and returns the corresponding mutation sets.
func (m *SlotMutations) Evict(index iotago.SlotIndex) error {
	m.evictionMutex.Lock()
	defer m.evictionMutex.Unlock()

	if index <= m.latestCommittedIndex {
		return ierrors.Errorf("cannot commit slot %d: already committed", index)
	}

	m.evictUntil(index)

	return nil
}

func (m *SlotMutations) Reset(index iotago.SlotIndex) {
	m.evictionMutex.Lock()
	defer m.evictionMutex.Unlock()

	for i := m.latestCommittedIndex; i > index; i-- {
		m.acceptedBlocksBySlot.Delete(i)
	}

	m.latestCommittedIndex = index
}

// AcceptedBlocks returns the set of accepted blocks for the given slot.
func (m *SlotMutations) AcceptedBlocks(index iotago.SlotIndex, createIfMissing ...bool) *ads.Set[iotago.BlockID] {
	if len(createIfMissing) > 0 && createIfMissing[0] {
		return lo.Return1(m.acceptedBlocksBySlot.GetOrCreate(index, newSet))
	}

	return lo.Return1(m.acceptedBlocksBySlot.Get(index))
}

// evictUntil removes all data for slots that are older than the given slot.
func (m *SlotMutations) evictUntil(index iotago.SlotIndex) {
	for i := m.latestCommittedIndex + 1; i <= index; i++ {
		m.acceptedBlocksBySlot.Delete(i)
	}

	m.latestCommittedIndex = index
}

// newSet is a helper constructor for a new in-memory ads.Set[iotago.BlockID].
func newSet() *ads.Set[iotago.BlockID] {
	return ads.NewSet(mapdb.NewMapDB(),
		iotago.SlotIdentifier.Bytes,
		iotago.SlotIdentifierFromBytes,
	)
}
