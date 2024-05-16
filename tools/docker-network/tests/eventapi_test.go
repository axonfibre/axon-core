//go:build dockertests

package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	"github.com/iotaledger/iota-core/tools/docker-network/tests/dockertestframework"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/api"
)

func Test_MQTTTopics(t *testing.T) {
	d := dockertestframework.NewDockerTestFramework(t,
		dockertestframework.WithProtocolParametersOptions(dockertestframework.ShortSlotsAndEpochsProtocolParametersOptionsFunc()...),
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

	e := dockertestframework.NewEventAPIDockerTestFramework(t, d)

	// prepare accounts to speed up tests
	accounts := d.CreateAccountsFromFaucet(context.Background(), 2, "account-1", "account-2")

	type test struct {
		name     string
		testFunc func(t *testing.T, e *dockertestframework.EventAPIDockerTestFramework, account *mock.AccountWithWallet)
		account  *mock.AccountWithWallet
	}

	tests := []*test{
		{
			name:     "Test_Commitments",
			testFunc: test_Commitments,
			account:  nil,
		},
		{
			name:     "Test_ValidationBlocks",
			testFunc: test_ValidationBlocks,
			account:  nil,
		},
		{
			name:     "Test_BasicTaggedDataBlocks",
			testFunc: test_BasicTaggedDataBlocks,
			account:  accounts[0],
		},
		{
			name:     "Test_DelegationTransactionBlocks",
			testFunc: test_DelegationTransactionBlocks,
			account:  accounts[1],
		},
		{
			name:     "Test_AccountTransactionBlocks",
			testFunc: test_AccountTransactionBlocks,
			account:  nil,
		},
		{
			name:     "Test_FoundryTransactionBlocks",
			testFunc: test_FoundryTransactionBlocks,
			account:  nil,
		},
		{
			name:     "Test_NFTTransactionBlocks",
			testFunc: test_NFTTransactionBlocks,
			account:  nil,
		},
		{
			name:     "Test_BlockMetadataMatchedCoreAPI",
			testFunc: test_BlockMetadataMatchedCoreAPI,
			account:  nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.testFunc(t, e, test.account)
		})
	}
}

func test_Commitments(t *testing.T, e *dockertestframework.EventAPIDockerTestFramework, _ *mock.AccountWithWallet) {

	// get event API client ready
	ctx, cancel := context.WithCancel(context.Background())
	eventClt := e.ConnectEventAPIClient(ctx)
	defer eventClt.Close()

	infoResp, err := e.DefaultClient.Info(ctx)
	require.NoError(t, err)

	// prepare the expected commitments to be received
	expectedLatestSlots := make([]iotago.SlotIndex, 0)
	for i := infoResp.Status.LatestCommitmentID.Slot() + 2; i < infoResp.Status.LatestCommitmentID.Slot()+6; i++ {
		expectedLatestSlots = append(expectedLatestSlots, i)
	}

	expectedFinalizedSlots := make([]iotago.SlotIndex, 0)
	for i := infoResp.Status.LatestFinalizedSlot + 2; i < infoResp.Status.LatestFinalizedSlot+6; i++ {
		expectedFinalizedSlots = append(expectedFinalizedSlots, i)
	}

	assertions := []func(){
		func() { e.AssertLatestCommitments(ctx, eventClt, expectedLatestSlots) },
		func() { e.AssertFinalizedCommitments(ctx, eventClt, expectedFinalizedSlots) },
	}

	totalTopics := len(assertions)
	for _, assertion := range assertions {
		assertion()
	}

	// wait until all topics receives all expected objects
	err = e.AwaitEventAPITopics(t, cancel, totalTopics)
	require.NoError(t, err)
}

func test_ValidationBlocks(t *testing.T, e *dockertestframework.EventAPIDockerTestFramework, _ *mock.AccountWithWallet) {

	// get event API client ready
	ctx, cancel := context.WithCancel(context.Background())
	eventClt := e.ConnectEventAPIClient(ctx)
	defer eventClt.Close()

	// prepare the expected commitments to be received
	validators := make(map[string]struct{}, 0)
	nodes := e.DockerTestFramework().Nodes("V1", "V2", "V3", "V4")
	for _, node := range nodes {
		validators[node.AccountAddressBech32] = struct{}{}
	}

	assertions := []func(){
		func() {
			e.AssertValidationBlocks(ctx, eventClt, e.DefaultClient.CommittedAPI().ProtocolParameters().Bech32HRP(), validators)
		},
	}

	totalTopics := len(assertions)
	for _, assertion := range assertions {
		assertion()
	}

	// wait until all topics receives all expected objects
	err := e.AwaitEventAPITopics(t, cancel, totalTopics)
	require.NoError(t, err)
}

func test_BasicTaggedDataBlocks(t *testing.T, e *dockertestframework.EventAPIDockerTestFramework, account *mock.AccountWithWallet) {
	// get event API client ready
	ctx, cancel := context.WithCancel(context.Background())
	eventClt := e.ConnectEventAPIClient(ctx)
	defer eventClt.Close()

	// prepare data blocks to send
	expectedBlocks := make(map[string]*iotago.Block)
	for i := 0; i < 10; i++ {
		blk := e.DockerTestFramework().CreateTaggedDataBlock(account.Wallet(), []byte("tag"))
		expectedBlocks[blk.MustID().ToHex()] = blk
	}

	assertions := []func(){
		func() { e.AssertBlocks(ctx, eventClt, expectedBlocks) },
		func() { e.AssertBasicBlocks(ctx, eventClt, expectedBlocks) },
		func() { e.AssertTaggedDataBlocks(ctx, eventClt, expectedBlocks) },
		func() { e.AssertTaggedDataBlocksByTag(ctx, eventClt, expectedBlocks, []byte("tag")) },
		func() { e.AssertBlockMetadataAcceptedBlocks(ctx, eventClt, expectedBlocks) },
		func() { e.AssertBlockMetadataConfirmedBlocks(ctx, eventClt, expectedBlocks) },
	}

	totalTopics := len(assertions)
	for _, assertion := range assertions {
		assertion()
	}

	// wait until all topics starts listening
	err := e.AwaitEventAPITopics(t, cancel, totalTopics)
	require.NoError(t, err)

	// issue blocks
	go func() {
		for _, blk := range expectedBlocks {
			if ctx.Err() != nil {
				// context is canceled
				return
			}

			fmt.Println("submitting a block: ", blk.MustID().ToHex())
			e.DockerTestFramework().SubmitBlock(context.Background(), blk)
		}
	}()

	// wait until all topics receives all expected objects
	err = e.AwaitEventAPITopics(t, cancel, totalTopics)
	require.NoError(t, err)
}

func test_DelegationTransactionBlocks(t *testing.T, e *dockertestframework.EventAPIDockerTestFramework, account *mock.AccountWithWallet) {
	// get event API client ready
	ctx, cancel := context.WithCancel(context.Background())
	eventClt := e.ConnectEventAPIClient(ctx)
	defer eventClt.Close()

	// create an account to issue blocks
	fundsOutputData := e.DockerTestFramework().RequestFaucetFunds(ctx, account.Wallet(), iotago.AddressEd25519)

	// prepare data blocks to send
	delegationId, outputId, blk := e.DockerTestFramework().CreateDelegationBlockFromInput(account.Wallet(), e.DockerTestFramework().Node("V2").AccountAddress(t), fundsOutputData)
	expectedBlocks := map[string]*iotago.Block{
		blk.MustID().ToHex(): blk,
	}
	delegationOutput := account.Wallet().Output(outputId)

	asserts := []func(){
		func() { e.AssertTransactionBlocks(ctx, eventClt, expectedBlocks) },
		func() { e.AssertBasicBlocks(ctx, eventClt, expectedBlocks) },
		func() { e.AssertBlockMetadataAcceptedBlocks(ctx, eventClt, expectedBlocks) },
		func() { e.AssertBlockMetadataConfirmedBlocks(ctx, eventClt, expectedBlocks) },
		func() { e.AssertTransactionBlocksByTag(ctx, eventClt, expectedBlocks, []byte("delegation")) },
		func() { e.AssertTransactionMetadataByTransactionID(ctx, eventClt, outputId.TransactionID()) },
		func() { e.AssertTransactionMetadataIncludedBlocks(ctx, eventClt, outputId.TransactionID()) },
		func() { e.AssertDelegationOutput(ctx, eventClt, delegationId) },
		func() { e.AssertOutput(ctx, eventClt, outputId) },
		func() {
			e.AssertOutputsWithMetadataByUnlockConditionAndAddress(ctx, eventClt, api.EventAPIUnlockConditionAny, delegationOutput.Address)
		},
		func() {
			e.AssertOutputsWithMetadataByUnlockConditionAndAddress(ctx, eventClt, api.EventAPIUnlockConditionAddress, delegationOutput.Address)
		},
	}

	totalTopics := len(asserts)
	for _, assert := range asserts {
		assert()
	}

	// wait until all topics starts listening
	err := e.AwaitEventAPITopics(t, cancel, totalTopics)
	require.NoError(t, err)

	// issue blocks
	go func() {
		for _, blk := range expectedBlocks {
			if ctx.Err() != nil {
				// context is canceled
				return
			}

			fmt.Println("submitting a block: ", blk.MustID().ToHex())
			e.DockerTestFramework().SubmitBlock(context.Background(), blk)
		}
	}()

	// wait until all topics receives all expected objects
	err = e.AwaitEventAPITopics(t, cancel, totalTopics)
	require.NoError(t, err)
}

func test_AccountTransactionBlocks(t *testing.T, e *dockertestframework.EventAPIDockerTestFramework, _ *mock.AccountWithWallet) {
	// get event API client ready
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	eventClt := e.ConnectEventAPIClient(ctx)
	defer eventClt.Close()

	// implicit account transition
	{
		// create an implicit account by requesting faucet funds
		implicitAccount := e.DockerTestFramework().CreateImplicitAccount(ctx, "account-tx-blocks")

		// prepare account transition block
		accountData, _, blk := e.DockerTestFramework().TransitionImplicitAccountToAccountOutputBlock(implicitAccount, implicitAccount.Wallet().GetNewBlockIssuanceResponse())

		expectedBlocks := map[string]*iotago.Block{
			blk.MustID().ToHex(): blk,
		}
		accountOutputData := implicitAccount.Wallet().Output(accountData.OutputID)

		assertions := []func(){
			func() { e.AssertTransactionBlocks(ctx, eventClt, expectedBlocks) },
			func() { e.AssertBasicBlocks(ctx, eventClt, expectedBlocks) },
			func() { e.AssertBlockMetadataAcceptedBlocks(ctx, eventClt, expectedBlocks) },
			func() { e.AssertBlockMetadataConfirmedBlocks(ctx, eventClt, expectedBlocks) },
			func() { e.AssertTransactionBlocksByTag(ctx, eventClt, expectedBlocks, []byte("account")) },
			func() {
				e.AssertTransactionMetadataByTransactionID(ctx, eventClt, accountData.OutputID.TransactionID())
			},
			func() { e.AssertTransactionMetadataIncludedBlocks(ctx, eventClt, accountData.OutputID.TransactionID()) },
			func() { e.AssertAccountOutput(ctx, eventClt, accountData.ID) },
			func() { e.AssertOutput(ctx, eventClt, accountData.OutputID) },
			func() {
				e.AssertOutputsWithMetadataByUnlockConditionAndAddress(ctx, eventClt, api.EventAPIUnlockConditionAny, accountOutputData.Address)
			},
			func() {
				e.AssertOutputsWithMetadataByUnlockConditionAndAddress(ctx, eventClt, api.EventAPIUnlockConditionAddress, accountOutputData.Address)
			},
		}

		totalTopics := len(assertions)
		for _, assertion := range assertions {
			assertion()
		}

		// wait until all topics starts listening
		err := e.AwaitEventAPITopics(t, cancel, totalTopics)
		require.NoError(t, err)

		// issue blocks
		go func() {
			for _, blk := range expectedBlocks {
				if ctx.Err() != nil {
					// context is canceled
					return
				}

				fmt.Println("submitting a block: ", blk.MustID().ToHex())
				e.DockerTestFramework().SubmitBlock(context.Background(), blk)
			}
		}()

		// wait until all topics receives all expected objects
		err = e.AwaitEventAPITopics(t, cancel, totalTopics)
		require.NoError(t, err)
	}
}

func test_FoundryTransactionBlocks(t *testing.T, e *dockertestframework.EventAPIDockerTestFramework, _ *mock.AccountWithWallet) {
	// get event API client ready
	ctx, cancel := context.WithCancel(context.Background())
	eventClt := e.ConnectEventAPIClient(ctx)
	defer eventClt.Close()

	{
		account := e.DockerTestFramework().CreateAccountFromFaucet("account-foundry")
		fundsOutputData := e.DockerTestFramework().RequestFaucetFunds(ctx, account.Wallet(), iotago.AddressEd25519)

		// prepare foundry output block
		foundryId, outputId, blk := e.DockerTestFramework().CreateFoundryBlockFromInput(account.Wallet(), fundsOutputData.ID, 5_000_000, 10_000_000_000)
		expectedBlocks := map[string]*iotago.Block{
			blk.MustID().ToHex(): blk,
		}
		foundryOutput := account.Wallet().Output(outputId)

		assertions := []func(){
			func() { e.AssertTransactionBlocks(ctx, eventClt, expectedBlocks) },
			func() { e.AssertBasicBlocks(ctx, eventClt, expectedBlocks) },
			func() { e.AssertBlockMetadataAcceptedBlocks(ctx, eventClt, expectedBlocks) },
			func() { e.AssertBlockMetadataConfirmedBlocks(ctx, eventClt, expectedBlocks) },
			func() { e.AssertTransactionBlocksByTag(ctx, eventClt, expectedBlocks, []byte("foundry")) },
			func() { e.AssertTransactionMetadataByTransactionID(ctx, eventClt, outputId.TransactionID()) },
			func() { e.AssertTransactionMetadataIncludedBlocks(ctx, eventClt, outputId.TransactionID()) },
			func() { e.AssertAccountOutput(ctx, eventClt, account.Account().ID) },
			func() { e.AssertFoundryOutput(ctx, eventClt, foundryId) },
			func() { e.AssertOutput(ctx, eventClt, outputId) },
			func() { e.AssertOutput(ctx, eventClt, account.Account().OutputID) },
			func() {
				e.AssertOutputsWithMetadataByUnlockConditionAndAddress(ctx, eventClt, api.EventAPIUnlockConditionAny, foundryOutput.Address)
			},
			func() {
				e.AssertOutputsWithMetadataByUnlockConditionAndAddress(ctx, eventClt, api.EventAPIUnlockConditionImmutableAccount, foundryOutput.Address)
			},
		}

		totalTopics := len(assertions)
		for _, assertion := range assertions {
			assertion()
		}

		// wait until all topics starts listening
		err := e.AwaitEventAPITopics(t, cancel, totalTopics)
		require.NoError(t, err)

		// issue blocks
		go func() {
			for _, blk := range expectedBlocks {
				if ctx.Err() != nil {
					// context is canceled
					return
				}

				fmt.Println("submitting a block: ", blk.MustID().ToHex())
				e.DockerTestFramework().SubmitBlock(context.Background(), blk)
			}
		}()

		// wait until all topics receives all expected objects
		err = e.AwaitEventAPITopics(t, cancel, totalTopics)
		require.NoError(t, err)
	}
}

func test_NFTTransactionBlocks(t *testing.T, e *dockertestframework.EventAPIDockerTestFramework, _ *mock.AccountWithWallet) {
	// get event API client ready
	ctx, cancel := context.WithCancel(context.Background())
	eventClt := e.ConnectEventAPIClient(ctx)
	defer eventClt.Close()

	{
		account := e.DockerTestFramework().CreateAccountFromFaucet("account-nft")
		fundsOutputData := e.DockerTestFramework().RequestFaucetFunds(ctx, account.Wallet(), iotago.AddressEd25519)

		// prepare NFT output block
		nftId, outputId, blk := e.DockerTestFramework().CreateNFTBlockFromInput(account.Wallet(), fundsOutputData)
		expectedBlocks := map[string]*iotago.Block{
			blk.MustID().ToHex(): blk,
		}
		nftOutput := account.Wallet().Output(outputId)

		assertions := []func(){
			func() { e.AssertTransactionBlocks(ctx, eventClt, expectedBlocks) },
			func() { e.AssertBasicBlocks(ctx, eventClt, expectedBlocks) },
			func() { e.AssertBlockMetadataAcceptedBlocks(ctx, eventClt, expectedBlocks) },
			func() { e.AssertBlockMetadataConfirmedBlocks(ctx, eventClt, expectedBlocks) },
			func() { e.AssertTransactionBlocksByTag(ctx, eventClt, expectedBlocks, []byte("nft")) },
			func() { e.AssertTransactionMetadataByTransactionID(ctx, eventClt, outputId.TransactionID()) },
			func() { e.AssertTransactionMetadataIncludedBlocks(ctx, eventClt, outputId.TransactionID()) },
			func() { e.AssertNFTOutput(ctx, eventClt, nftId) },
			func() { e.AssertOutput(ctx, eventClt, outputId) },
			func() {
				e.AssertOutputsWithMetadataByUnlockConditionAndAddress(ctx, eventClt, api.EventAPIUnlockConditionAny, nftOutput.Address)
			},
			func() {
				e.AssertOutputsWithMetadataByUnlockConditionAndAddress(ctx, eventClt, api.EventAPIUnlockConditionAddress, nftOutput.Address)
			},
		}

		totalTopics := len(assertions)
		for _, assertion := range assertions {
			assertion()
		}

		// wait until all topics starts listening
		err := e.AwaitEventAPITopics(t, cancel, totalTopics)
		require.NoError(t, err)

		// issue blocks
		go func() {
			for _, blk := range expectedBlocks {
				if ctx.Err() != nil {
					// context is canceled
					return
				}

				fmt.Println("submitting a block: ", blk.MustID().ToHex())
				e.DockerTestFramework().SubmitBlock(context.Background(), blk)
			}
		}()

		// wait until all topics receives all expected objects
		err = e.AwaitEventAPITopics(t, cancel, totalTopics)
		require.NoError(t, err)
	}
}

func test_BlockMetadataMatchedCoreAPI(t *testing.T, e *dockertestframework.EventAPIDockerTestFramework, _ *mock.AccountWithWallet) {
	// get event API client ready
	ctx, cancel := context.WithCancel(context.Background())
	eventClt := e.ConnectEventAPIClient(ctx)
	defer eventClt.Close()

	{
		account := e.DockerTestFramework().CreateAccountFromFaucet("account-block-metadata")

		assertions := []func(){
			func() { e.AssertBlockMetadataStateAcceptedBlocks(ctx, eventClt) },
			func() { e.AssertBlockMetadataStateConfirmedBlocks(ctx, eventClt) },
		}

		totalTopics := len(assertions)
		for _, assertion := range assertions {
			assertion()
		}

		// wait until all topics starts listening
		err := e.AwaitEventAPITopics(t, cancel, totalTopics)
		require.NoError(t, err)

		// issue blocks
		e.SubmitDataBlockStream(account.Wallet(), 5*time.Minute)

		cancel()
	}
}
