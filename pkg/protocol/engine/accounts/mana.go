package accounts

import (
	"github.com/iotaledger/hive.go/runtime/syncutils"
	"github.com/iotaledger/hive.go/stringify"
	iotago "github.com/iotaledger/iota.go/v4"
)

// Mana is the stored and potential mana value of an account collected on the UTXO layer - used by the Scheduler.
type Mana struct {
	value            iotago.Mana      `serix:""`
	excessBaseTokens iotago.BaseToken `serix:""`
	updateTime       iotago.SlotIndex `serix:""`

	mutex syncutils.RWMutex
}

func NewMana(value iotago.Mana, excessBaseTokens iotago.BaseToken, updateTime iotago.SlotIndex) *Mana {
	return &Mana{
		value:            value,
		excessBaseTokens: excessBaseTokens,
		updateTime:       updateTime,
	}
}

func (m *Mana) Value() iotago.Mana {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return m.value
}

func (m *Mana) ExcessBaseTokens() iotago.BaseToken {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return m.excessBaseTokens
}

func (m *Mana) UpdateTime() iotago.SlotIndex {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return m.updateTime
}

func (m *Mana) String() string {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	return stringify.Struct("Mana",
		stringify.NewStructField("Value", uint64(m.value)),
		stringify.NewStructField("ExcessBaseTokens", uint64(m.excessBaseTokens)),
		stringify.NewStructField("UpdateTime", uint32(m.updateTime)),
	)
}
