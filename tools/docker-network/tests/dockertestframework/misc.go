//go:build dockertests

package dockertestframework

import (
	iotago "github.com/iotaledger/iota.go/v4"
)

func GetMaxRegistrationSlot(committedAPI iotago.API, epoch iotago.EpochIndex) iotago.SlotIndex {
	epochEndSlot := committedAPI.TimeProvider().EpochEnd(epoch)
	return epochEndSlot - committedAPI.ProtocolParameters().EpochNearingThreshold()
}
