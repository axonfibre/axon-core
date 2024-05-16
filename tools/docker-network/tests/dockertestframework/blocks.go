//go:build dockertests

package dockertestframework

import (
	"context"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/runtime/options"
	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/builder"
)

// CreateTaggedDataBlock creates and submits a block of a tagged data payload.
func (d *DockerTestFramework) CreateTaggedDataBlock(wallet *mock.Wallet, tag []byte) *iotago.Block {
	ctx := context.TODO()

	return lo.PanicOnErr(wallet.CreateBasicBlock(ctx, "", mock.WithPayload(&iotago.TaggedData{
		Tag: tag,
	}))).ProtocolBlock()
}

func (d *DockerTestFramework) CreateBasicOutputBlock(wallet *mock.Wallet) (*iotago.Block, *iotago.SignedTransaction, *mock.OutputData) {
	fundsOutputData := d.RequestFaucetFunds(context.Background(), wallet, iotago.AddressEd25519)

	signedTx := wallet.CreateBasicOutputFromInput(fundsOutputData)
	block, err := wallet.CreateBasicBlock(context.Background(), "", mock.WithPayload(signedTx))
	require.NoError(d.Testing, err)

	return block.ProtocolBlock(), signedTx, fundsOutputData
}

// CreateDelegationBlockFromInput consumes the given basic output, then build a block of a transaction that includes a delegation output, in order to delegate the given validator.
func (d *DockerTestFramework) CreateDelegationBlockFromInput(wallet *mock.Wallet, accountAdddress *iotago.AccountAddress, input *mock.OutputData) (iotago.DelegationID, iotago.OutputID, *iotago.Block) {
	ctx := context.TODO()
	clt := wallet.Client

	signedTx := wallet.CreateDelegationFromInput(
		"",
		input,
		mock.WithDelegatedValidatorAddress(accountAdddress),
		mock.WithDelegationStartEpoch(GetDelegationStartEpoch(clt.LatestAPI(), wallet.GetNewBlockIssuanceResponse().LatestCommitment.Slot)),
	)
	outputID := iotago.OutputIDFromTransactionIDAndIndex(signedTx.Transaction.MustID(), 0)

	return iotago.DelegationIDFromOutputID(outputID),
		outputID,
		lo.PanicOnErr(wallet.CreateBasicBlock(ctx, "", mock.WithPayload(signedTx))).ProtocolBlock()
}

// CreateFoundryBlockFromInput consumes the given basic output, then build a block of a transaction that includes a foundry output with the given mintedAmount and maxSupply.
func (d *DockerTestFramework) CreateFoundryBlockFromInput(wallet *mock.Wallet, inputID iotago.OutputID, mintedAmount iotago.BaseToken, maxSupply iotago.BaseToken) (iotago.FoundryID, iotago.OutputID, *iotago.Block) {
	input := wallet.Output(inputID)
	signedTx := wallet.CreateFoundryAndNativeTokensFromInput(input, mintedAmount, maxSupply)
	txID, err := signedTx.Transaction.ID()
	require.NoError(d.Testing, err)

	//nolint:forcetypeassert
	return signedTx.Transaction.Outputs[1].(*iotago.FoundryOutput).MustFoundryID(),
		iotago.OutputIDFromTransactionIDAndIndex(txID, 1),
		lo.PanicOnErr(wallet.CreateBasicBlock(context.Background(), "", mock.WithPayload(signedTx))).ProtocolBlock()
}

// CreateFoundryTransitionBlockFromInput consumes the given foundry output, then build block by increasing the minted amount by 1.
func (d *DockerTestFramework) CreateFoundryTransitionBlockFromInput(issuerID iotago.AccountID, foundryInput, accountInput *mock.OutputData) (iotago.FoundryID, iotago.OutputID, *iotago.Block) {
	signedTx := d.defaultWallet.TransitionFoundry("", foundryInput, accountInput)
	txID, err := signedTx.Transaction.ID()
	require.NoError(d.Testing, err)

	//nolint:forcetypeassert
	return signedTx.Transaction.Outputs[1].(*iotago.FoundryOutput).MustFoundryID(),
		iotago.OutputIDFromTransactionIDAndIndex(txID, 1),
		lo.PanicOnErr(d.defaultWallet.CreateAndSubmitBasicBlock(context.Background(), "foundry_transition", mock.WithPayload(signedTx))).ProtocolBlock()
}

// CreateNFTBlockFromInput consumes the given basic output, then build a block of a transaction that includes a NFT output with the given NFT output options.
func (d *DockerTestFramework) CreateNFTBlockFromInput(wallet *mock.Wallet, input *mock.OutputData, opts ...options.Option[builder.NFTOutputBuilder]) (iotago.NFTID, iotago.OutputID, *iotago.Block) {
	signedTx := wallet.CreateTaggedNFTFromInput("", input, opts...)
	outputID := iotago.OutputIDFromTransactionIDAndIndex(signedTx.Transaction.MustID(), 0)

	return iotago.NFTIDFromOutputID(outputID),
		outputID,
		lo.PanicOnErr(wallet.CreateBasicBlock(context.Background(), "", mock.WithPayload(signedTx))).ProtocolBlock()
}

func (d *DockerTestFramework) SubmitBlock(ctx context.Context, blk *iotago.Block) {
	_, err := d.defaultWallet.Client.SubmitBlock(ctx, blk)
	require.NoError(d.Testing, err)
}
