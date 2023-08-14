package validator

import (
	"context"
	"sync/atomic"
	"time"

	"go.uber.org/dig"

	"github.com/iotaledger/hive.go/app"
	"github.com/iotaledger/hive.go/runtime/event"
	"github.com/iotaledger/hive.go/runtime/timed"
	"github.com/iotaledger/iota-core/components/blockissuer"
	"github.com/iotaledger/iota-core/pkg/blockfactory"
	"github.com/iotaledger/iota-core/pkg/daemon"
	"github.com/iotaledger/iota-core/pkg/protocol"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/notarization"
	iotago "github.com/iotaledger/iota.go/v4"
)

func init() {
	Component = &app.Component{
		Name:     "Validator",
		DepsFunc: func(cDeps dependencies) { deps = cDeps },
		Params:   params,
		Run:      run,
		IsEnabled: func(_ *dig.Container) bool {
			return ParamsValidator.Enabled && blockissuer.ParamsBlockIssuer.Enabled
		},
	}
}

var (
	Component *app.Component
	deps      dependencies

	isValidator atomic.Bool
	executor    *timed.TaskExecutor[iotago.AccountID]
	accountID   iotago.AccountID
)

type dependencies struct {
	dig.In

	Protocol    *protocol.Protocol
	BlockIssuer *blockfactory.BlockIssuer
}

func run() error {
	accountID = deps.BlockIssuer.Account.ID()
	// If issuer is part of the current committee, and current time is in the epoch, issue validator blocks.

	// If issuer is not part of the committee and current time is in the time range for registering candidates, issue candidate blocks.

	executor = timed.NewTaskExecutor[iotago.AccountID](1)

	return Component.Daemon().BackgroundWorker(Component.Name, func(ctx context.Context) {
		Component.LogInfof("Starting Validator with IssuerID: %s", accountID)

		checkValidatorStatus(ctx)

		deps.Protocol.Events.Engine.Notarization.SlotCommitted.Hook(func(details *notarization.SlotCommittedDetails) {
			checkValidatorStatus(ctx)
		}, event.WithWorkerPool(Component.WorkerPool))

		<-ctx.Done()

		executor.Shutdown()

		Component.LogInfo("Stopping Validator... done")
	}, daemon.PriorityActivity)
}

func checkValidatorStatus(ctx context.Context) {
	account, exists, err := deps.Protocol.MainEngineInstance().Ledger.Account(accountID, deps.Protocol.MainEngineInstance().Storage.Settings().LatestCommitment().Index())
	if err != nil {
		Component.LogErrorf("error when retrieving BlockIssuer account %s: %w", accountID, err)

		return
	}

	if !exists || account.StakeEndEpoch <= deps.Protocol.CurrentAPI().TimeProvider().EpochFromSlot(deps.Protocol.CurrentAPI().TimeProvider().SlotFromTime(time.Now())) {
		if prevValue := isValidator.Swap(false); prevValue {
			// If the account stops being a validator, don't issue any blocks.
			Component.LogInfof("BlockIssuer account %s stopped being a validator", accountID)
			executor.Cancel(accountID)
		}

		return
	}

	if prevValue := isValidator.Swap(true); !prevValue {
		Component.LogInfof("BlockIssuer account %s became a validator", accountID)
		// If the account becomes a validator, start issue validator blocks.
		executor.ExecuteAfter(accountID, func() { issueValidatorBlock(ctx) }, ParamsValidator.CommitteeBroadcastInterval)
	}
}
