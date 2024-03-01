package mempool

import (
	"github.com/iotaledger/hive.go/ierrors"
)

var (
	ErrStateNotFound = ierrors.New("state not found")
	ErrRequestFailed = ierrors.New("request failed")
)
