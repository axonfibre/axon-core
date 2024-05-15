//go:build dockertests

package tests

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/ds/types"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	"github.com/iotaledger/iota-core/tools/docker-network/tests/dockertestframework"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/api"
	"github.com/iotaledger/iota.go/v4/tpkg"
)

type coreAPIAssets map[iotago.SlotIndex]*coreAPISlotAssets

func (a coreAPIAssets) setupAssetsForSlot(slot iotago.SlotIndex) {
	_, ok := a[slot]
	if !ok {
		a[slot] = newAssetsPerSlot()
	}
}

func (a coreAPIAssets) assertCommitments(t *testing.T) {
	for _, asset := range a {
		asset.assertCommitments(t)
	}
}

func (a coreAPIAssets) assertBICs(t *testing.T) {
	for _, asset := range a {
		asset.assertBICs(t)
	}
}

func (a coreAPIAssets) forEachBlock(t *testing.T, f func(*testing.T, *iotago.Block)) {
	for _, asset := range a {
		for _, block := range asset.dataBlocks {
			f(t, block)
		}
		for _, block := range asset.valueBlocks {
			f(t, block)
		}
	}
}

func (a coreAPIAssets) forEachTransaction(t *testing.T, f func(*testing.T, *iotago.SignedTransaction, iotago.BlockID)) {
	for _, asset := range a {
		for i, tx := range asset.transactions {
			blockID := asset.valueBlocks[i].MustID()
			f(t, tx, blockID)
		}
	}
}

func (a coreAPIAssets) forEachReattachment(t *testing.T, f func(*testing.T, iotago.BlockID)) {
	for _, asset := range a {
		for _, reattachment := range asset.reattachments {
			f(t, reattachment)
		}
	}
}

func (a coreAPIAssets) forEachOutput(t *testing.T, f func(*testing.T, iotago.OutputID, iotago.Output)) {
	for _, asset := range a {
		for outID, out := range asset.basicOutputs {
			f(t, outID, out)
		}
		for outID, out := range asset.faucetOutputs {
			f(t, outID, out)
		}
		for outID, out := range asset.delegationOutputs {
			f(t, outID, out)
		}
	}
}

func (a coreAPIAssets) forEachSlot(t *testing.T, f func(*testing.T, iotago.SlotIndex, map[string]iotago.CommitmentID)) {
	for slot, slotAssets := range a {
		f(t, slot, slotAssets.commitmentPerNode)
	}
}

func (a coreAPIAssets) forEachCommitment(t *testing.T, f func(*testing.T, map[string]iotago.CommitmentID)) {
	for _, asset := range a {
		f(t, asset.commitmentPerNode)
	}
}

func (a coreAPIAssets) forEachAccountAddress(t *testing.T, f func(t *testing.T, accountAddress *iotago.AccountAddress, commitmentPerNode map[string]iotago.CommitmentID, bicPerNode map[string]iotago.BlockIssuanceCredits)) {
	for _, asset := range a {
		if asset.accountAddress == nil {
			// no account created in this slot
			continue
		}
		f(t, asset.accountAddress, asset.commitmentPerNode, asset.bicPerNode)
	}
}

func (a coreAPIAssets) assertUTXOOutputIDsInSlot(t *testing.T, slot iotago.SlotIndex, createdOutputs iotago.OutputIDs, spentOutputs iotago.OutputIDs) {
	created := make(map[iotago.OutputID]types.Empty)
	spent := make(map[iotago.OutputID]types.Empty)
	for _, outputID := range createdOutputs {
		created[outputID] = types.Void
	}

	for _, outputID := range spentOutputs {
		spent[outputID] = types.Void
	}

	for outID := range a[slot].basicOutputs {
		_, ok := created[outID]
		require.True(t, ok, "Output ID not found in created outputs: %s, for slot %d", outID, slot)
	}

	for outID := range a[slot].faucetOutputs {
		_, ok := spent[outID]
		require.True(t, ok, "Output ID not found in spent outputs: %s, for slot %d", outID, slot)
	}
}

func (a coreAPIAssets) assertUTXOOutputsInSlot(t *testing.T, slot iotago.SlotIndex, created []*api.OutputWithID, spent []*api.OutputWithID) {
	createdMap := make(map[iotago.OutputID]iotago.Output)
	spentMap := make(map[iotago.OutputID]iotago.Output)
	for _, output := range created {
		createdMap[output.OutputID] = output.Output
	}
	for _, output := range spent {
		spentMap[output.OutputID] = output.Output
	}

	for outID, out := range a[slot].basicOutputs {
		_, ok := createdMap[outID]
		require.True(t, ok, "Output ID not found in created outputs: %s, for slot %d", outID, slot)
		require.Equal(t, out, createdMap[outID], "Output not equal for ID: %s, for slot %d", outID, slot)
	}

	for outID, out := range a[slot].faucetOutputs {
		_, ok := spentMap[outID]
		require.True(t, ok, "Output ID not found in spent outputs: %s, for slot %d", outID, slot)
		require.Equal(t, out, spentMap[outID], "Output not equal for ID: %s, for slot %d", outID, slot)
	}
}

type coreAPISlotAssets struct {
	accountAddress    *iotago.AccountAddress
	dataBlocks        []*iotago.Block
	valueBlocks       []*iotago.Block
	transactions      []*iotago.SignedTransaction
	reattachments     []iotago.BlockID
	basicOutputs      map[iotago.OutputID]iotago.Output
	faucetOutputs     map[iotago.OutputID]iotago.Output
	delegationOutputs map[iotago.OutputID]iotago.Output

	commitmentPerNode map[string]iotago.CommitmentID
	bicPerNode        map[string]iotago.BlockIssuanceCredits
}

func (a *coreAPISlotAssets) assertCommitments(t *testing.T) {
	prevCommitment := a.commitmentPerNode["V1"]
	for _, commitmentID := range a.commitmentPerNode {
		if prevCommitment == iotago.EmptyCommitmentID {
			require.Fail(t, "commitment is empty")
		}

		require.Equal(t, commitmentID, prevCommitment)
		prevCommitment = commitmentID
	}
}

func (a *coreAPISlotAssets) assertBICs(t *testing.T) {
	prevBIC := a.bicPerNode["V1"]
	for _, bic := range a.bicPerNode {
		require.Equal(t, bic, prevBIC)
		prevBIC = bic
	}
}

func newAssetsPerSlot() *coreAPISlotAssets {
	return &coreAPISlotAssets{
		dataBlocks:        make([]*iotago.Block, 0),
		valueBlocks:       make([]*iotago.Block, 0),
		transactions:      make([]*iotago.SignedTransaction, 0),
		reattachments:     make([]iotago.BlockID, 0),
		basicOutputs:      make(map[iotago.OutputID]iotago.Output),
		faucetOutputs:     make(map[iotago.OutputID]iotago.Output),
		delegationOutputs: make(map[iotago.OutputID]iotago.Output),
		commitmentPerNode: make(map[string]iotago.CommitmentID),
		bicPerNode:        make(map[string]iotago.BlockIssuanceCredits),
	}
}

func prepareAssets(d *dockertestframework.DockerTestFramework, totalAssetsNum int) (coreAPIAssets, iotago.SlotIndex) {
	assets := make(coreAPIAssets)
	ctx := context.Background()

	latestSlot := iotago.SlotIndex(0)

	for i := 0; i < totalAssetsNum; i++ {
		// account
		wallet, account := d.CreateAccountFromFaucet()
		assets.setupAssetsForSlot(account.OutputID.Slot())
		assets[account.OutputID.Slot()].accountAddress = account.Address

		// data block
		block := d.CreateTaggedDataBlock(wallet, []byte("tag"))
		blockSlot := lo.PanicOnErr(block.ID()).Slot()
		assets.setupAssetsForSlot(blockSlot)
		assets[blockSlot].dataBlocks = append(assets[blockSlot].dataBlocks, block)
		d.SubmitBlock(ctx, block)

		// transaction
		valueBlock, signedTx, faucetOutput := d.CreateBasicOutputBlock(wallet)
		valueBlockSlot := valueBlock.MustID().Slot()
		assets.setupAssetsForSlot(valueBlockSlot)
		// transaction and outputs are stored with the earliest included block
		assets[valueBlockSlot].valueBlocks = append(assets[valueBlockSlot].valueBlocks, valueBlock)
		assets[valueBlockSlot].transactions = append(assets[valueBlockSlot].transactions, signedTx)
		basicOutputID := iotago.OutputIDFromTransactionIDAndIndex(signedTx.Transaction.MustID(), 0)
		assets[valueBlockSlot].basicOutputs[basicOutputID] = signedTx.Transaction.Outputs[0]
		assets[valueBlockSlot].faucetOutputs[faucetOutput.ID] = faucetOutput.Output
		d.SubmitBlock(ctx, valueBlock)
		d.AwaitTransactionPayloadAccepted(ctx, signedTx.Transaction.MustID())

		// issue reattachment after the first one is already included
		secondAttachment, err := wallet.CreateAndSubmitBasicBlock(ctx, "second_attachment", mock.WithPayload(signedTx))
		require.NoError(d.Testing, err)
		assets[valueBlockSlot].reattachments = append(assets[valueBlockSlot].reattachments, secondAttachment.ID())

		// delegation
		//nolint:forcetypeassert
		delegationOutputData := d.DelegateToValidator(wallet, d.Node("V1").AccountAddress(d.Testing))
		assets.setupAssetsForSlot(delegationOutputData.ID.CreationSlot())
		assets[delegationOutputData.ID.CreationSlot()].delegationOutputs[delegationOutputData.ID] = delegationOutputData.Output.(*iotago.DelegationOutput)

		latestSlot = lo.Max[iotago.SlotIndex](latestSlot, blockSlot, valueBlockSlot, delegationOutputData.ID.CreationSlot(), secondAttachment.ID().Slot())

		fmt.Printf("Assets for slot %d\n: dataBlock: %s block: %s\ntx: %s\nbasic output: %s, faucet output: %s\n delegation output: %s\n",
			valueBlockSlot, block.MustID().String(), valueBlock.MustID().String(), signedTx.MustID().String(),
			basicOutputID.String(), faucetOutput.ID.String(), delegationOutputData.ID.String())
	}

	return assets, latestSlot
}

// Test_ValidatorsAPI tests if the validators API returns the expected validators.
// 1. Run docker network.
// 2. Create 50 new accounts with staking feature.
// 3. Wait until next epoch then issue candidacy payload for each account.
// 4. Check if all 54 validators are returned from the validators API with pageSize 10, the pagination of api is also tested.
// 5. Wait until next epoch then check again if the results remain.
func Test_ValidatorsAPI(t *testing.T) {
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

	hrp := d.DefaultWallet().Client.CommittedAPI().ProtocolParameters().Bech32HRP()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
	})

	// Create registered validators
	clt := d.DefaultWallet().Client

	// get the initial validators
	expectedValidators := d.AccountsFromNodes(d.Nodes()...)

	validatorCount := 50

	implicitAccountsValidators := d.CreateImplicitAccounts(ctx, validatorCount)

	blockIssuance, err := clt.BlockIssuance(ctx)
	require.NoError(t, err)

	latestCommitmentSlot := blockIssuance.LatestCommitment.Slot
	fullAccountCreationEpoch := clt.CommittedAPI().TimeProvider().CurrentEpoch()
	stakingStartEpoch := d.DefaultWallet().StakingStartEpochFromSlot(latestCommitmentSlot)

	// create a new wait group for the next step
	var wg sync.WaitGroup

	// create accounts with staking feature
	for i := range validatorCount {
		wg.Add(1)

		go func(validatorNr int) {
			defer wg.Done()

			// create account with staking feature for every validator
			accountWithWallet := d.CreateAccountFromImplicitAccount(implicitAccountsValidators[validatorNr],
				blockIssuance,
				dockertestframework.WithStakingFeature(100, 1, stakingStartEpoch),
			)

			expectedValidators = append(expectedValidators, accountWithWallet.Account().Address.Bech32(hrp))
		}(i)
	}
	wg.Wait()

	d.AwaitCommitment(clt.CommittedAPI().TimeProvider().EpochEnd(fullAccountCreationEpoch))

	// check if all validators are returned from the validators API with pageSize 10
	actualValidators := getAllValidatorsOnEpoch(t, clt, 0, 10)
	require.ElementsMatch(t, expectedValidators, actualValidators)

	// wait until fullAccountCreationEpoch+1 and check the results again
	targetSlot := clt.CommittedAPI().TimeProvider().EpochEnd(fullAccountCreationEpoch + 1)
	d.AwaitCommitment(targetSlot)
	actualValidators = getAllValidatorsOnEpoch(t, clt, fullAccountCreationEpoch+1, 10)
	require.ElementsMatch(t, expectedValidators, actualValidators)
}

func Test_CoreAPI_ValidRequests(t *testing.T) {
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

	assetsPerSlot, lastSlot := prepareAssets(d, 5)

	fmt.Println("Await finalisation of slot", lastSlot)
	d.AwaitFinalization(lastSlot)

	tests := []struct {
		name     string
		testFunc func(t *testing.T, node *dockertestframework.Node, client mock.Client)
	}{
		{
			name: "Test_Info",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				resp, err := client.Info(context.Background())
				require.NoError(t, err)
				require.NotNil(t, resp)
			},
		},
		{
			name: "Test_BlockByBlockID",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachBlock(t, func(t *testing.T, block *iotago.Block) {
					respBlock, err := client.BlockByBlockID(context.Background(), block.MustID())
					require.NoError(t, err)
					require.NotNil(t, respBlock)
					require.Equal(t, block.MustID(), respBlock.MustID(), "BlockID of retrieved block does not match: %s != %s", block.MustID(), respBlock.MustID())
				})
			},
		},
		{
			name: "Test_BlockMetadataByBlockID",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachBlock(t, func(t *testing.T, block *iotago.Block) {
					resp, err := client.BlockMetadataByBlockID(context.Background(), block.MustID())
					require.NoError(t, err)
					require.NotNil(t, resp)
					require.Equal(t, block.MustID(), resp.BlockID, "BlockID of retrieved block does not match: %s != %s", block.MustID(), resp.BlockID)
					require.Equal(t, api.BlockStateFinalized, resp.BlockState)
				})

				assetsPerSlot.forEachReattachment(t, func(t *testing.T, blockID iotago.BlockID) {
					resp, err := client.BlockMetadataByBlockID(context.Background(), blockID)
					require.NoError(t, err)
					require.NotNil(t, resp)
					require.Equal(t, blockID, resp.BlockID, "BlockID of retrieved block does not match: %s != %s", blockID, resp.BlockID)
					require.Equal(t, api.BlockStateFinalized, resp.BlockState)
				})
			},
		},
		{
			name: "Test_BlockWithMetadata",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachBlock(t, func(t *testing.T, block *iotago.Block) {
					resp, err := client.BlockWithMetadataByBlockID(context.Background(), block.MustID())
					require.NoError(t, err)
					require.NotNil(t, resp)
					require.Equal(t, block.MustID(), resp.Block.MustID(), "BlockID of retrieved block does not match: %s != %s", block.MustID(), resp.Block.MustID())
					require.Equal(t, api.BlockStateFinalized, resp.Metadata.BlockState)
				})
			},
		},
		{
			name: "Test_BlockIssuance",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				resp, err := client.BlockIssuance(context.Background())
				require.NoError(t, err)
				require.NotNil(t, resp)

				require.GreaterOrEqual(t, len(resp.StrongParents), 1, "There should be at least 1 strong parent provided")
			},
		},
		{
			name: "Test_CommitmentBySlot",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachSlot(t, func(t *testing.T, slot iotago.SlotIndex, commitmentsPerNode map[string]iotago.CommitmentID) {
					resp, err := client.CommitmentBySlot(context.Background(), slot)
					require.NoError(t, err)
					require.NotNil(t, resp)
					commitmentsPerNode[node.Name] = resp.MustID()
				})
			},
		},
		{
			name: "Test_CommitmentByID",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachCommitment(t, func(t *testing.T, commitmentsPerNode map[string]iotago.CommitmentID) {
					resp, err := client.CommitmentByID(context.Background(), commitmentsPerNode[node.Name])
					require.NoError(t, err)
					require.NotNil(t, resp)
					require.Equal(t, commitmentsPerNode[node.Name], resp.MustID(), "Commitment does not match commitment got for the same slot from the same node: %s != %s", commitmentsPerNode[node.Name], resp.MustID())
				})
			},
		},
		{
			name: "Test_CommitmentUTXOChangesByID",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachCommitment(t, func(t *testing.T, commitmentsPerNode map[string]iotago.CommitmentID) {
					resp, err := client.CommitmentUTXOChangesByID(context.Background(), commitmentsPerNode[node.Name])
					require.NoError(t, err)
					require.NotNil(t, resp)
					assetsPerSlot.assertUTXOOutputIDsInSlot(t, commitmentsPerNode[node.Name].Slot(), resp.CreatedOutputs, resp.ConsumedOutputs)
					require.Equal(t, commitmentsPerNode[node.Name], resp.CommitmentID, "CommitmentID of retrieved UTXO changes does not match: %s != %s", commitmentsPerNode[node.Name], resp.CommitmentID)
				})
			},
		},
		{
			"Test_CommitmentUTXOChangesFullByID",
			func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachCommitment(t, func(t *testing.T, commitmentsPerNode map[string]iotago.CommitmentID) {
					resp, err := client.CommitmentUTXOChangesFullByID(context.Background(), commitmentsPerNode[node.Name])
					require.NoError(t, err)
					require.NotNil(t, resp)
					assetsPerSlot.assertUTXOOutputsInSlot(t, commitmentsPerNode[node.Name].Slot(), resp.CreatedOutputs, resp.ConsumedOutputs)
					require.Equal(t, commitmentsPerNode[node.Name], resp.CommitmentID, "CommitmentID of retrieved UTXO changes does not match: %s != %s", commitmentsPerNode[node.Name], resp.CommitmentID)
				})
			},
		},
		{
			name: "Test_CommitmentUTXOChangesBySlot",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachCommitment(t, func(t *testing.T, commitmentsPerNode map[string]iotago.CommitmentID) {
					resp, err := client.CommitmentUTXOChangesBySlot(context.Background(), commitmentsPerNode[node.Name].Slot())
					require.NoError(t, err)
					require.NotNil(t, resp)
					assetsPerSlot.assertUTXOOutputIDsInSlot(t, commitmentsPerNode[node.Name].Slot(), resp.CreatedOutputs, resp.ConsumedOutputs)
					require.Equal(t, commitmentsPerNode[node.Name], resp.CommitmentID, "CommitmentID of retrieved UTXO changes does not match: %s != %s", commitmentsPerNode[node.Name], resp.CommitmentID)
				})
			},
		},
		{
			name: "Test_CommitmentUTXOChangesFullBySlot",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachCommitment(t, func(t *testing.T, commitmentsPerNode map[string]iotago.CommitmentID) {
					resp, err := client.CommitmentUTXOChangesFullBySlot(context.Background(), commitmentsPerNode[node.Name].Slot())
					require.NoError(t, err)
					require.NotNil(t, resp)
					assetsPerSlot.assertUTXOOutputsInSlot(t, commitmentsPerNode[node.Name].Slot(), resp.CreatedOutputs, resp.ConsumedOutputs)
					require.Equal(t, commitmentsPerNode[node.Name], resp.CommitmentID, "CommitmentID of retrieved UTXO changes does not match: %s != %s", commitmentsPerNode[node.Name], resp.CommitmentID)
				})
			},
		},
		{
			name: "Test_OutputByID",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachOutput(t, func(t *testing.T, outputID iotago.OutputID, output iotago.Output) {
					resp, err := client.OutputByID(context.Background(), outputID)
					require.NoError(t, err)
					require.NotNil(t, resp)
					require.EqualValues(t, output, resp, "Output created is different than retrieved from the API: %s != %s", output, resp)
				})
			},
		},
		{
			name: "Test_OutputMetadata",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachOutput(t, func(t *testing.T, outputID iotago.OutputID, output iotago.Output) {
					resp, err := client.OutputMetadataByID(context.Background(), outputID)
					require.NoError(t, err)
					require.NotNil(t, resp)
					require.EqualValues(t, outputID, resp.OutputID, "OutputID of retrieved output does not match: %s != %s", outputID, resp.OutputID)
					require.EqualValues(t, outputID.TransactionID(), resp.Included.TransactionID, "TransactionID of retrieved output does not match: %s != %s", outputID.TransactionID(), resp.Included.TransactionID)
				})
			},
		},
		{
			name: "Test_OutputWithMetadata",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachOutput(t, func(t *testing.T, outputID iotago.OutputID, output iotago.Output) {
					out, outMetadata, err := client.OutputWithMetadataByID(context.Background(), outputID)
					require.NoError(t, err)
					require.NotNil(t, outMetadata)
					require.NotNil(t, out)
					require.EqualValues(t, outputID, outMetadata.OutputID, "OutputID of retrieved output does not match: %s != %s", outputID, outMetadata.OutputID)
					require.EqualValues(t, outputID.TransactionID(), outMetadata.Included.TransactionID, "TransactionID of retrieved output does not match: %s != %s", outputID.TransactionID(), outMetadata.Included.TransactionID)
					require.EqualValues(t, output, out, "OutputID of retrieved output does not match: %s != %s", output, out)
				})
			},
		},
		{
			name: "Test_TransactionByID",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachTransaction(t, func(t *testing.T, transaction *iotago.SignedTransaction, firstAttachmentID iotago.BlockID) {
					txID := transaction.Transaction.MustID()
					resp, err := client.TransactionByID(context.Background(), txID)
					require.NoError(t, err)
					require.NotNil(t, resp)
					require.EqualValues(t, txID, resp.MustID())
				})
			},
		},
		{
			name: "Test_TransactionsIncludedBlock",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachTransaction(t, func(t *testing.T, transaction *iotago.SignedTransaction, firstAttachmentID iotago.BlockID) {
					resp, err := client.TransactionIncludedBlock(context.Background(), transaction.Transaction.MustID())
					require.NoError(t, err)
					require.NotNil(t, resp)
					require.EqualValues(t, firstAttachmentID, resp.MustID())
				})
			},
		},
		{
			name: "Test_TransactionsIncludedBlockMetadata",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachTransaction(t, func(t *testing.T, transaction *iotago.SignedTransaction, firstAttachmentID iotago.BlockID) {
					resp, err := client.TransactionIncludedBlockMetadata(context.Background(), transaction.Transaction.MustID())
					require.NoError(t, err)
					require.NotNil(t, resp)
					require.EqualValues(t, api.BlockStateFinalized, resp.BlockState)
					require.EqualValues(t, firstAttachmentID, resp.BlockID, "Inclusion BlockID of retrieved transaction does not match: %s != %s", firstAttachmentID, resp.BlockID)
				})
			},
		},
		{
			name: "Test_TransactionsMetadata",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachTransaction(t, func(t *testing.T, transaction *iotago.SignedTransaction, firstAttachmentID iotago.BlockID) {
					resp, err := client.TransactionMetadata(context.Background(), transaction.Transaction.MustID())
					require.NoError(t, err)
					require.NotNil(t, resp)
					require.Equal(t, api.TransactionStateFinalized, resp.TransactionState)
					require.EqualValues(t, resp.EarliestAttachmentSlot, firstAttachmentID.Slot())
				})
			},
		},
		{
			name: "Test_Congestion",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachAccountAddress(t, func(
					t *testing.T,
					accountAddress *iotago.AccountAddress,
					commitmentPerNode map[string]iotago.CommitmentID,
					bicPerNoode map[string]iotago.BlockIssuanceCredits,
				) {
					resp, err := client.Congestion(context.Background(), accountAddress, 0)
					require.NoError(t, err)
					require.NotNil(t, resp)

					// node allows to get account only for the slot newer than lastCommittedSlot - MCA, we need fresh commitment
					infoRes, err := client.Info(context.Background())
					require.NoError(t, err)
					commitment, err := client.CommitmentBySlot(context.Background(), infoRes.Status.LatestCommitmentID.Slot())
					require.NoError(t, err)

					resp, err = client.Congestion(context.Background(), accountAddress, 0, commitment.MustID())
					require.NoError(t, err)
					require.NotNil(t, resp)
					// later we check if all nodes have returned the same BIC value for this account
					bicPerNoode[node.Name] = resp.BlockIssuanceCredits
				})
			},
		},
		{
			name: "Test_Validators",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				pageSize := uint64(3)
				resp, err := client.Validators(context.Background(), pageSize)
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.Equal(t, int(pageSize), len(resp.Validators), "There should be exactly %d validators returned on the first page", pageSize)

				resp, err = client.Validators(context.Background(), pageSize, resp.Cursor)
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.Equal(t, 1, len(resp.Validators), "There should be only one validator returned on the last page")
			},
		},
		{
			name: "Test_ValidatorsAll",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				resp, all, err := client.ValidatorsAll(context.Background())
				require.NoError(t, err)
				require.True(t, all)
				require.Equal(t, 4, len(resp.Validators))
			},
		},
		{
			name: "Test_Rewards",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				assetsPerSlot.forEachOutput(t, func(t *testing.T, outputID iotago.OutputID, output iotago.Output) {
					if output.Type() != iotago.OutputDelegation {
						return
					}

					resp, err := client.Rewards(context.Background(), outputID)
					require.NoError(t, err)
					require.NotNil(t, resp)

					timeProvider := d.DefaultWallet().Client.CommittedAPI().TimeProvider()
					outputCreationEpoch := timeProvider.EpochFromSlot(outputID.Slot())

					if outputCreationEpoch == timeProvider.CurrentEpoch() {
						// rewards are zero, because we do not wait for the epoch end
						require.EqualValues(t, 0, resp.Rewards)
					} else {
						// rewards can be greater or equal to 0, since the delegation happened earlier
						require.GreaterOrEqual(t, resp.Rewards, iotago.Mana(0))
					}
				})
			},
		},
		{
			name: "Test_Committee",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				resp, err := client.Committee(context.Background())
				require.NoError(t, err)
				require.NotNil(t, resp)
				require.EqualValues(t, 4, len(resp.Committee))
			},
		},
		{
			name: "Test_CommitteeWithEpoch",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				resp, err := client.Committee(context.Background(), 0)
				require.NoError(t, err)
				require.Equal(t, iotago.EpochIndex(0), resp.Epoch)
				require.Equal(t, 4, len(resp.Committee))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, node := range d.Nodes() {
				test.testFunc(d.Testing, node, d.Client(node.Name))
			}
		})
	}

	// check if the same values were returned by all nodes for the same slot
	assetsPerSlot.assertCommitments(t)
	assetsPerSlot.assertBICs(t)
}

func Test_CoreAPI_BadRequests(t *testing.T) {
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

	tests := []struct {
		name     string
		testFunc func(t *testing.T, node *dockertestframework.Node, client mock.Client)
	}{
		{
			name: "Test_BlockByBlockID_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				blockID := tpkg.RandBlockID()
				respBlock, err := client.BlockByBlockID(context.Background(), blockID)
				require.Error(t, err)
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
				require.Nil(t, respBlock)
			},
		},
		{
			name: "Test_BlockMetadataByBlockID_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				blockID := tpkg.RandBlockID()
				resp, err := client.BlockMetadataByBlockID(context.Background(), blockID)
				require.Error(t, err)
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
				require.Nil(t, resp)
			},
		},
		{
			name: "Test_BlockWithMetadata_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				blockID := tpkg.RandBlockID()
				resp, err := client.BlockWithMetadataByBlockID(context.Background(), blockID)
				require.Error(t, err)
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
				require.Nil(t, resp)
			},
		},
		{
			name: "Test_CommitmentBySlot_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				slot := iotago.SlotIndex(1000_000_000)
				resp, err := client.CommitmentBySlot(context.Background(), slot)
				require.Error(t, err)
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
				require.Nil(t, resp)
			},
		},
		{
			name: "Test_CommitmentByID_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				committmentID := tpkg.RandCommitmentID()
				resp, err := client.CommitmentByID(context.Background(), committmentID)
				require.Error(t, err)
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
				require.Nil(t, resp)
			},
		},
		{
			name: "Test_CommitmentUTXOChangesByID_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				committmentID := tpkg.RandCommitmentID()
				resp, err := client.CommitmentUTXOChangesByID(context.Background(), committmentID)
				require.Error(t, err)
				// commitmentID is valid, but the UTXO changes does not exist in the storage
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
				require.Nil(t, resp)
			},
		},
		{
			"Test_CommitmentUTXOChangesFullByID_Failure",
			func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				committmentID := tpkg.RandCommitmentID()

				resp, err := client.CommitmentUTXOChangesFullByID(context.Background(), committmentID)
				require.Error(t, err)
				// commitmentID is valid, but the UTXO changes does not exist in the storage
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
				require.Nil(t, resp)
			},
		},
		{
			name: "Test_CommitmentUTXOChangesBySlot_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				slot := iotago.SlotIndex(1000_000_000)
				resp, err := client.CommitmentUTXOChangesBySlot(context.Background(), slot)
				require.Error(t, err)
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
				require.Nil(t, resp)
			},
		},
		{
			name: "Test_CommitmentUTXOChangesFullBySlot_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				slot := iotago.SlotIndex(1000_000_000)

				resp, err := client.CommitmentUTXOChangesFullBySlot(context.Background(), slot)
				require.Error(t, err)
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
				require.Nil(t, resp)
			},
		},
		{
			name: "Test_OutputByID_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				outputID := tpkg.RandOutputID(0)
				resp, err := client.OutputByID(context.Background(), outputID)
				require.Error(t, err)
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
				require.Nil(t, resp)
			},
		},
		{
			name: "Test_OutputMetadata_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				outputID := tpkg.RandOutputID(0)

				resp, err := client.OutputMetadataByID(context.Background(), outputID)
				require.Error(t, err)
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
				require.Nil(t, resp)
			},
		},
		{
			name: "Test_OutputWithMetadata_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				outputID := tpkg.RandOutputID(0)

				out, outMetadata, err := client.OutputWithMetadataByID(context.Background(), outputID)
				require.Error(t, err)
				require.Nil(t, out)
				require.Nil(t, outMetadata)
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
			},
		},
		{
			name: "Test_TransactionsIncludedBlock_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				txID := tpkg.RandTransactionID()
				resp, err := client.TransactionIncludedBlock(context.Background(), txID)
				require.Error(t, err)
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
				require.Nil(t, resp)
			},
		},
		{
			name: "Test_TransactionsIncludedBlockMetadata_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				txID := tpkg.RandTransactionID()

				resp, err := client.TransactionIncludedBlockMetadata(context.Background(), txID)
				require.Error(t, err)
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
				require.Nil(t, resp)
			},
		},
		{
			name: "Test_TransactionsMetadata_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				txID := tpkg.RandTransactionID()

				resp, err := client.TransactionMetadata(context.Background(), txID)
				require.Error(t, err)
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
				require.Nil(t, resp)
			},
		},
		{
			name: "Test_Congestion_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				accountAddress := tpkg.RandAccountAddress()
				commitmentID := tpkg.RandCommitmentID()
				resp, err := client.Congestion(context.Background(), accountAddress, 0, commitmentID)
				require.Error(t, err)
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
				require.Nil(t, resp)
			},
		},
		{
			name: "Test_Committee_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				resp, err := client.Committee(context.Background(), 4000)
				require.Error(t, err)
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusBadRequest))
				require.Nil(t, resp)
			},
		},
		{
			name: "Test_Rewards_Failure",
			testFunc: func(t *testing.T, node *dockertestframework.Node, client mock.Client) {
				outputID := tpkg.RandOutputID(0)
				resp, err := client.Rewards(context.Background(), outputID)
				require.Error(t, err)
				require.True(t, dockertestframework.IsStatusCode(err, http.StatusNotFound))
				require.Nil(t, resp)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, node := range d.Nodes() {
				test.testFunc(d.Testing, node, d.Client(node.Name))
			}
		})
	}
}

func getAllValidatorsOnEpoch(t *testing.T, clt mock.Client, epoch iotago.EpochIndex, pageSize uint64) []string {
	actualValidators := make([]string, 0)
	cursor := ""
	if epoch != 0 {
		cursor = fmt.Sprintf("%d,%d", epoch, 0)
	}

	for {
		resp, err := clt.Validators(context.Background(), pageSize, cursor)
		require.NoError(t, err)

		for _, v := range resp.Validators {
			actualValidators = append(actualValidators, v.AddressBech32)
		}

		cursor = resp.Cursor
		if cursor == "" {
			break
		}
	}

	return actualValidators
}
