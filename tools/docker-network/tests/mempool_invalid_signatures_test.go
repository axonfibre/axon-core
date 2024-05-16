//go:build dockertests

package tests

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	"github.com/iotaledger/iota-core/tools/docker-network/tests/dockertestframework"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/api"
)

func Test_MempoolInvalidSignatures(t *testing.T) {
	d := dockertestframework.NewDockerTestFramework(t,
		dockertestframework.WithProtocolParametersOptions(dockertestframework.ShortSlotsAndEpochsProtocolParametersOptions...),
	)
	defer d.Stop()

	d.AddValidatorNode("V1", "docker-network-inx-validator-1-1", "http://localhost:8050", "rms1pzg8cqhfxqhq7pt37y8cs4v5u4kcc48lquy2k73ehsdhf5ukhya3y5rx2w6")
	d.AddValidatorNode("V2", "docker-network-inx-validator-2-1", "http://localhost:8060", "rms1pqm4xk8e9ny5w5rxjkvtp249tfhlwvcshyr3pc0665jvp7g3hc875k538hl")
	d.AddValidatorNode("V3", "docker-network-inx-validator-3-1", "http://localhost:8070", "rms1pp4wuuz0y42caz48vv876qfpmffswsvg40zz8v79sy8cp0jfxm4kunflcgt")
	d.AddValidatorNode("V4", "docker-network-inx-validator-4-1", "http://localhost:8040", "rms1pr8cxs3dzu9xh4cduff4dd4cxdthpjkpwmz2244f75m0urslrsvtsshrrjw")
	d.AddNode("node5", "docker-network-node-5-1", "http://localhost:8080")

	err := d.Run()
	require.NoError(t, err)

	d.WaitUntilNetworkReady()

	account := d.CreateAccountFromFaucet("account-1")
	wallet := account.Wallet()

	ctx := context.Background()
	fundsOutputData := d.RequestFaucetFunds(ctx, wallet, iotago.AddressEd25519)
	validTX := wallet.CreateBasicOutputFromInput(fundsOutputData)
	invalidTX := validTX.Clone().(*iotago.SignedTransaction)

	// Make validTX invalid by replacing the first unlock with an empty signature unlock.
	_, is := invalidTX.Unlocks[0].(*iotago.SignatureUnlock)
	require.Truef(t, is, "expected signature unlock as first unlock")
	invalidTX.Unlocks[0] = &iotago.SignatureUnlock{
		Signature: &iotago.Ed25519Signature{},
	}

	fmt.Println("Submitting block with invalid TX")
	wallet.CreateAndSubmitBasicBlock(ctx, "invalid_tx_block", mock.WithPayload(invalidTX))

	d.AwaitTransactionState(ctx, invalidTX.Transaction.MustID(), api.TransactionStateFailed)
	d.AwaitTransactionFailure(ctx, invalidTX.Transaction.MustID(), api.TxFailureUnlockSignatureInvalid)

	fmt.Println("Submitting block with valid TX")
	wallet.CreateAndSubmitBasicBlock(ctx, "valid_tx_block", mock.WithPayload(validTX))

	fmt.Println("Submitting block with invalid TX (again)")
	wallet.CreateAndSubmitBasicBlock(ctx, "invalid_tx_block_2", mock.WithPayload(invalidTX))

	d.AwaitTransactionPayloadAccepted(ctx, validTX.Transaction.MustID())
}
