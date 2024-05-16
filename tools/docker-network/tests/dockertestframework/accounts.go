//go:build dockertests

package dockertestframework

import (
	"context"
	"fmt"
	"sync"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/runtime/options"
	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/api"
	"github.com/iotaledger/iota.go/v4/builder"
)

func (d *DockerTestFramework) AccountsFromNodes(nodes ...*Node) []string {
	var accounts []string
	for _, node := range nodes {
		if node.AccountAddressBech32 != "" {
			accounts = append(accounts, node.AccountAddressBech32)
		}
	}

	return accounts
}

func (d *DockerTestFramework) CheckAccountStatus(ctx context.Context, blkID iotago.BlockID, txID iotago.TransactionID, creationOutputID iotago.OutputID, accountAddress *iotago.AccountAddress, checkIndexer ...bool) {
	// request by blockID if provided, otherwise use txID
	// we take the slot from the blockID in case the tx is created earlier than the block.
	clt := d.defaultWallet.Client
	slot := blkID.Slot()

	if blkID == iotago.EmptyBlockID {
		blkMetadata, err := clt.TransactionIncludedBlockMetadata(ctx, txID)
		require.NoError(d.Testing, err)

		blkID = blkMetadata.BlockID
		slot = blkMetadata.BlockID.Slot()
	}

	d.AwaitTransactionPayloadAccepted(ctx, txID)

	// wait for the account to be committed
	d.AwaitCommittedSlot(slot, false)

	// Check the indexer
	if len(checkIndexer) > 0 && checkIndexer[0] {
		indexerClt, err := d.defaultWallet.Client.Indexer(ctx)
		require.NoError(d.Testing, err)

		_, _, _, err = indexerClt.Account(ctx, accountAddress)
		require.NoError(d.Testing, err)
	}

	// check if the creation output exists
	_, err := clt.OutputByID(ctx, creationOutputID)
	require.NoError(d.Testing, err)
}

type ImplicitAccount struct {
	wallet     *mock.Wallet
	outputData *mock.OutputData
}

func (i *ImplicitAccount) Wallet() *mock.Wallet {
	return i.wallet
}

func (i *ImplicitAccount) OutputData() *mock.OutputData {
	return i.outputData
}

// CreateImplicitAccount requests faucet funds and creates an implicit account. It already wait until the transaction is committed and the created account is useable.
func (d *DockerTestFramework) CreateImplicitAccount(ctx context.Context, name string) *ImplicitAccount {
	wallet := mock.NewWallet(d.Testing, name, d.defaultWallet.Client, &DockerWalletClock{client: d.defaultWallet.Client})
	outputData := d.RequestFaucetFunds(ctx, wallet, iotago.AddressImplicitAccountCreation)

	accountID := iotago.AccountIDFromOutputID(outputData.ID)
	accountAddress, ok := accountID.ToAddress().(*iotago.AccountAddress)
	require.True(d.Testing, ok)

	// make sure an implicit account is committed
	d.CheckAccountStatus(ctx, iotago.EmptyBlockID, outputData.ID.TransactionID(), outputData.ID, accountAddress)

	// update the wallet with the new account data
	wallet.SetBlockIssuer(&mock.AccountData{
		ID:           accountID,
		Address:      accountAddress,
		OutputID:     outputData.ID,
		AddressIndex: outputData.AddressIndex,
	})

	return &ImplicitAccount{
		wallet:     wallet,
		outputData: outputData,
	}
}

func (d *DockerTestFramework) CreateImplicitAccounts(ctx context.Context, count int, names ...string) []*ImplicitAccount {
	var wg sync.WaitGroup

	implicitAccounts := make([]*ImplicitAccount, count)

	for i := range count {
		wg.Add(1)

		// first create all implicit accounts in parallel
		go func(nr int) {
			defer wg.Done()

			// create implicit accounts
			name := fmt.Sprintf("account-%d", nr)
			if len(names) > nr {
				name = names[nr]
			}
			implicitAccounts[nr] = d.CreateImplicitAccount(ctx, name)
		}(i)
	}

	// wait until all implicit accounts are created
	wg.Wait()

	return implicitAccounts
}

// TransitionImplicitAccountToAccountOutputBlock consumes the given implicit account, then build the account transition block with the given account output options.
func (d *DockerTestFramework) TransitionImplicitAccountToAccountOutputBlock(implicitAccount *ImplicitAccount, blockIssuance *api.IssuanceBlockHeaderResponse, opts ...options.Option[builder.AccountOutputBuilder]) (*mock.AccountData, *iotago.SignedTransaction, *iotago.Block) {
	ctx := context.TODO()

	var implicitBlockIssuerKey iotago.BlockIssuerKey = iotago.Ed25519PublicKeyHashBlockIssuerKeyFromImplicitAccountCreationAddress(implicitAccount.Wallet().ImplicitAccountCreationAddress())
	opts = append(opts, mock.WithBlockIssuerFeature(
		iotago.NewBlockIssuerKeys(implicitBlockIssuerKey),
		iotago.MaxSlotIndex,
	))

	signedTx := implicitAccount.Wallet().TransitionImplicitAccountToAccountOutputWithBlockIssuance("", []*mock.OutputData{implicitAccount.OutputData()}, blockIssuance, opts...)

	// The account transition block should be issued by the implicit account block issuer key.
	block, err := implicitAccount.Wallet().CreateBasicBlock(ctx, "", mock.WithPayload(signedTx))
	require.NoError(d.Testing, err)
	accOutputID := iotago.OutputIDFromTransactionIDAndIndex(signedTx.Transaction.MustID(), 0)
	accOutput := signedTx.Transaction.Outputs[0].(*iotago.AccountOutput)
	accAddress := (accOutput.AccountID).ToAddress().(*iotago.AccountAddress)

	accountOutputData := &mock.AccountData{
		ID:           accOutput.AccountID,
		Address:      accAddress,
		Output:       accOutput,
		OutputID:     accOutputID,
		AddressIndex: implicitAccount.OutputData().AddressIndex,
	}

	return accountOutputData, signedTx, block.ProtocolBlock()
}

// CreateAccountFromImplicitAccount transitions an account from the given implicit one to full one, it already wait until the transaction is committed and the created account is useable.
func (d *DockerTestFramework) CreateAccountFromImplicitAccount(implicitAccount *ImplicitAccount, blockIssuance *api.IssuanceBlockHeaderResponse, opts ...options.Option[builder.AccountOutputBuilder]) *mock.AccountWithWallet {
	ctx := context.TODO()

	accountData, signedTx, block := d.TransitionImplicitAccountToAccountOutputBlock(implicitAccount, blockIssuance, opts...)

	d.SubmitBlock(ctx, block)
	d.CheckAccountStatus(ctx, block.MustID(), signedTx.Transaction.MustID(), accountData.OutputID, accountData.Address, true)

	// update the wallet with the new account data
	implicitAccount.Wallet().SetBlockIssuer(accountData)

	fmt.Printf("Account created, Bech addr: %s\n", accountData.Address.Bech32(implicitAccount.Wallet().Client.CommittedAPI().ProtocolParameters().Bech32HRP()))

	return mock.NewAccountWithWallet(implicitAccount.Wallet().Account(accountData.ID), implicitAccount.Wallet())
}

// CreateAccountFromFaucet creates a new account by requesting faucet funds to an implicit account address and then transitioning the new output to a full account output.
// It already waits until the transaction is committed and the created account is useable.
func (d *DockerTestFramework) CreateAccountFromFaucet(name string) (*mock.Wallet, *mock.AccountData) {
	ctx := context.TODO()

	implicitAccount := d.CreateImplicitAccount(ctx, name)

	accountData, signedTx, block := d.TransitionImplicitAccountToAccountOutputBlock(implicitAccount, d.defaultWallet.GetNewBlockIssuanceResponse())

	d.SubmitBlock(ctx, block)
	d.CheckAccountStatus(ctx, block.MustID(), signedTx.Transaction.MustID(), accountData.OutputID, accountData.Address, true)

	// update the wallet with the new account data
	implicitAccount.Wallet().SetBlockIssuer(accountData)

	fmt.Printf("Account created, Bech addr: %s\n", accountData.Address.Bech32(implicitAccount.Wallet().Client.CommittedAPI().ProtocolParameters().Bech32HRP()))

	return implicitAccount.Wallet(), implicitAccount.Wallet().Account(accountData.ID)
}

// CreateNativeToken request faucet funds then use it to create native token for the account, and returns the updated Account.
func (d *DockerTestFramework) CreateNativeToken(fromWallet *mock.Wallet, mintedAmount iotago.BaseToken, maxSupply iotago.BaseToken) {
	require.GreaterOrEqual(d.Testing, maxSupply, mintedAmount)

	ctx := context.TODO()

	// requesting faucet funds for native token creation
	fundsOutputData := d.RequestFaucetFunds(ctx, fromWallet, iotago.AddressEd25519)

	signedTx := fromWallet.CreateFoundryAndNativeTokensFromInput(fundsOutputData, mintedAmount, maxSupply)

	block, err := fromWallet.CreateAndSubmitBasicBlock(ctx, "native_token", mock.WithPayload(signedTx))
	require.NoError(d.Testing, err)

	txID := signedTx.Transaction.MustID()
	d.AwaitTransactionPayloadAccepted(ctx, txID)

	fmt.Println("Create native tokens transaction sent, blkID:", block.ID().ToHex(), ", txID:", signedTx.Transaction.MustID().ToHex(), ", slot:", block.ID().Slot())

	// wait for the account to be committed
	d.AwaitCommittedSlot(block.ID().Slot(), false)

	d.AssertIndexerAccount(fromWallet.BlockIssuer.AccountData)
	//nolint:forcetypeassert
	d.AssertIndexerFoundry(signedTx.Transaction.Outputs[1].(*iotago.FoundryOutput).MustFoundryID())
}
