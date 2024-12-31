//go:build dockertests

package dockertestframework

import (
	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	iotago "github.com/iotaledger/iota.go/v4"
)

type DockerWalletClock struct {
	client mock.Client
}

func (c *DockerWalletClock) SetCurrentSlot(slot iotago.SlotIndex) {
	panic("Cannot set current slot in DockerWalletClock, the slot is set by time.Now()")
}

func (c *DockerWalletClock) CurrentSlot() iotago.SlotIndex {
	return c.client.LatestAPI().TimeProvider().CurrentSlot()
}
