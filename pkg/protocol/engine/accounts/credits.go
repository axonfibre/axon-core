package accounts

import (
	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/runtime/syncutils"
	"github.com/iotaledger/hive.go/serializer/v2"
	"github.com/iotaledger/hive.go/serializer/v2/stream"
	"github.com/iotaledger/hive.go/stringify"
	iotago "github.com/iotaledger/iota.go/v4"
)

const BlockIssuanceCreditsBytesLength = serializer.Int64ByteSize + iotago.SlotIndexLength

// BlockIssuanceCredits is a weight annotated with the slot it was last updated in.
type BlockIssuanceCredits struct {
	mutex syncutils.RWMutex

	value      iotago.BlockIssuanceCredits
	updateSlot iotago.SlotIndex
}

// NewBlockIssuanceCredits creates a new Credits instance.
func NewBlockIssuanceCredits(value iotago.BlockIssuanceCredits, updateTime iotago.SlotIndex) (newCredits *BlockIssuanceCredits) {
	return &BlockIssuanceCredits{
		value:      value,
		updateSlot: updateTime,
	}
}

func (c *BlockIssuanceCredits) Clone() *BlockIssuanceCredits {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return NewBlockIssuanceCredits(c.value, c.updateSlot)
}

func (c *BlockIssuanceCredits) Value() iotago.BlockIssuanceCredits {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return c.value
}

func (c *BlockIssuanceCredits) UpdateSlot() iotago.SlotIndex {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return c.updateSlot
}

func (c *BlockIssuanceCredits) String() string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return stringify.Struct("BlockIssuanceCredits",
		stringify.NewStructField("Value", int64(c.value)),
		stringify.NewStructField("UpdateSlot", uint32(c.updateSlot)),
	)
}

// Bytes returns a serialized version of the Credits.
func (c *BlockIssuanceCredits) Bytes() ([]byte, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	byteBuffer := stream.NewByteBuffer()

	if err := stream.Write(byteBuffer, c.value); err != nil {
		return nil, ierrors.Wrap(err, "failed to write value")
	}

	if err := stream.Write(byteBuffer, c.updateSlot); err != nil {
		return nil, ierrors.Wrap(err, "failed to write updateTime")
	}

	return byteBuffer.Bytes()
}

func BlockIssuanceCreditsFromBytes(bytes []byte) (*BlockIssuanceCredits, int, error) {
	c := new(BlockIssuanceCredits)
	c.mutex.Lock()
	defer c.mutex.Unlock()

	var err error
	byteReader := stream.NewByteReader(bytes)

	if c.value, err = stream.Read[iotago.BlockIssuanceCredits](byteReader); err != nil {
		return nil, 0, ierrors.Wrap(err, "failed to read value")
	}

	if c.updateSlot, err = stream.Read[iotago.SlotIndex](byteReader); err != nil {
		return nil, 0, ierrors.Wrap(err, "failed to read updateTime")
	}

	return c, byteReader.BytesRead(), nil
}

// Update updates the Credits increasing Value and updateTime.
func (c *BlockIssuanceCredits) Update(change iotago.BlockIssuanceCredits, updateSlot ...iotago.SlotIndex) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.value += change
	if len(updateSlot) > 0 {
		c.updateSlot = updateSlot[0]
	}
}
