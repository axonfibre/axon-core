//go:build dockertests

package dockertestframework

import (
	"context"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/api"
)

func (d *DockerTestFramework) AwaitTransactionPayloadAccepted(ctx context.Context, txID iotago.TransactionID) {
	clt := d.defaultWallet.Client

	d.Eventually(func() error {
		resp, err := clt.TransactionMetadata(ctx, txID)
		if err != nil {
			return err
		}

		if resp.TransactionState == api.TransactionStateAccepted ||
			resp.TransactionState == api.TransactionStateCommitted ||
			resp.TransactionState == api.TransactionStateFinalized {
			if resp.TransactionFailureReason == api.TxFailureNone {
				return nil
			}
		}

		return ierrors.Errorf("transaction %s is pending or having errors, state: %s, failure reason: %s, failure details: %s", txID.ToHex(), resp.TransactionState, resp.TransactionFailureReason, resp.TransactionFailureDetails)
	})
}

func (d *DockerTestFramework) AwaitTransactionState(ctx context.Context, txID iotago.TransactionID, expectedState api.TransactionState) {
	d.Eventually(func() error {
		resp, err := d.defaultWallet.Client.TransactionMetadata(ctx, txID)
		if err != nil {
			return err
		}

		if expectedState == resp.TransactionState {
			return nil
		} else {
			if resp.TransactionState == api.TransactionStateFailed {
				return ierrors.Errorf("expected transaction %s to have state '%s', got '%s' instead, failure reason: %s, failure details: %s", txID, expectedState, resp.TransactionState, resp.TransactionFailureReason, resp.TransactionFailureDetails)
			}
			return ierrors.Errorf("expected transaction %s to have state '%s', got '%s' instead", txID, expectedState, resp.TransactionState)
		}
	})
}

func (d *DockerTestFramework) AwaitTransactionFailure(ctx context.Context, txID iotago.TransactionID, expectedReason api.TransactionFailureReason) {
	d.Eventually(func() error {
		resp, err := d.defaultWallet.Client.TransactionMetadata(ctx, txID)
		if err != nil {
			return err
		}

		if expectedReason == resp.TransactionFailureReason {
			return nil
		}

		return ierrors.Errorf("expected transaction %s to have failure reason '%s', got '%s' instead, failure details: %s", txID, expectedReason, resp.TransactionFailureReason, resp.TransactionFailureDetails)
	})
}

func (d *DockerTestFramework) AwaitSlot(targetSlot iotago.SlotIndex) {
	currentSlot := d.NodeStatus("V1").LatestAcceptedBlockSlot

	if targetSlot <= currentSlot {
		return
	}

	// we wait at max "targetSlot - currentSlot" times * slot duration
	deadline := time.Duration(d.defaultWallet.Client.CommittedAPI().ProtocolParameters().SlotDurationInSeconds()) * time.Second
	if currentSlot < targetSlot {
		deadline *= time.Duration(targetSlot - currentSlot)
	}

	// give some extra time for peering etc
	deadline += 30 * time.Second

	d.EventuallyWithDurations(func() error {
		latestAcceptedSlot := d.NodeStatus("V1").LatestAcceptedBlockSlot
		if targetSlot > latestAcceptedSlot {
			return ierrors.Errorf("current slot %d is not reached yet, current slot %d", targetSlot, latestAcceptedSlot)
		}

		return nil
	}, deadline, 1*time.Second)
}

func (d *DockerTestFramework) AwaitCommitment(targetSlot iotago.SlotIndex) {
	currentCommittedSlot := d.NodeStatus("V1").LatestCommitmentID.Slot()

	if targetSlot <= currentCommittedSlot {
		return
	}

	// we wait at max "targetSlot - currentCommittedSlot" times * slot duration
	deadline := time.Duration(d.defaultWallet.Client.CommittedAPI().ProtocolParameters().SlotDurationInSeconds()) * time.Second
	if currentCommittedSlot < targetSlot {
		deadline *= time.Duration(targetSlot - currentCommittedSlot)
	}

	// give some extra time for peering etc
	deadline += 30 * time.Second

	d.EventuallyWithDurations(func() error {
		latestCommittedSlot := d.NodeStatus("V1").LatestCommitmentID.Slot()
		if targetSlot > latestCommittedSlot {
			return ierrors.Errorf("committed slot %d is not reached yet, current committed slot %d", targetSlot, latestCommittedSlot)
		}

		return nil
	}, deadline, 1*time.Second)
}

func (d *DockerTestFramework) AwaitFinalization(targetSlot iotago.SlotIndex) {
	currentFinalizedSlot := d.NodeStatus("V1").LatestFinalizedSlot

	// we wait at max "targetSlot - currentFinalizedSlot" times * slot duration
	deadline := time.Duration(d.defaultWallet.Client.CommittedAPI().ProtocolParameters().SlotDurationInSeconds()) * time.Second
	if currentFinalizedSlot < targetSlot {
		deadline *= time.Duration(targetSlot - currentFinalizedSlot)
	}

	// give some extra time for peering etc
	deadline += 30 * time.Second

	d.EventuallyWithDurations(func() error {
		currentFinalisedSlot := d.NodeStatus("V1").LatestFinalizedSlot
		if targetSlot > currentFinalisedSlot {
			return ierrors.Errorf("finalized slot %d is not reached yet", targetSlot)
		}

		return nil
	}, deadline, 1*time.Second)
}

func (d *DockerTestFramework) AwaitEpochFinalized() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := d.defaultWallet.Client.Info(ctx)
	require.NoError(d.Testing, err)

	currentEpoch := d.defaultWallet.Client.CommittedAPI().TimeProvider().EpochFromSlot(info.Status.LatestFinalizedSlot)

	// await the start slot of the next epoch
	d.AwaitFinalization(d.defaultWallet.Client.CommittedAPI().TimeProvider().EpochStart(currentEpoch + 1))
}

func (d *DockerTestFramework) AwaitAddressUnspentOutputAccepted(ctx context.Context, wallet *mock.Wallet, addr iotago.Address) (outputID iotago.OutputID, output iotago.Output, err error) {
	indexerClt, err := wallet.Client.Indexer(ctx)
	require.NoError(d.Testing, err)
	addrBech := addr.Bech32(d.defaultWallet.Client.CommittedAPI().ProtocolParameters().Bech32HRP())

	for t := time.Now(); time.Since(t) < d.optsWaitFor; time.Sleep(d.optsTick) {
		res, err := indexerClt.Outputs(ctx, &api.BasicOutputsQuery{
			AddressBech32: addrBech,
		})
		if err != nil {
			return iotago.EmptyOutputID, nil, ierrors.Wrap(err, "indexer request failed in request faucet funds")
		}

		for res.Next() {
			unspents, err := res.Outputs(ctx)
			if err != nil {
				return iotago.EmptyOutputID, nil, ierrors.Wrap(err, "failed to get faucet unspent outputs")
			}

			if len(unspents) == 0 {
				break
			}

			return lo.Return1(res.Response.Items.OutputIDs())[0], unspents[0], nil
		}
	}

	return iotago.EmptyOutputID, nil, ierrors.Errorf("no unspent outputs found for address %s due to timeout", addrBech)
}
