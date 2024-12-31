//go:build dockertests

package tests

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

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

func (a coreAPIAssets) forEach(consumer func(slot iotago.SlotIndex, asset *coreAPISlotAssets)) {
	for slot, asset := range a {
		consumer(slot, asset)
	}
}

func (a coreAPIAssets) forEachSlot(consumer func(slot iotago.SlotIndex)) {
	for slot := range a {
		consumer(slot)
	}
}

func (a coreAPIAssets) forEachBlock(consumer func(block *iotago.Block)) {
	for _, asset := range a {
		for _, block := range asset.dataBlocks {
			consumer(block)
		}
		for _, block := range asset.valueBlocks {
			consumer(block)
		}
	}
}

func (a coreAPIAssets) forEachTransaction(consumer func(signedTx *iotago.SignedTransaction, blockID iotago.BlockID)) {
	for _, asset := range a {
		for i, signedTx := range asset.transactions {
			blockID := asset.valueBlocks[i].MustID()
			consumer(signedTx, blockID)
		}
	}
}

func (a coreAPIAssets) forEachReattachment(consumer func(blockID iotago.BlockID)) {
	for _, asset := range a {
		for _, blockID := range asset.reattachments {
			consumer(blockID)
		}
	}
}

func (a coreAPIAssets) forEachOutput(consumer func(outputID iotago.OutputID, output iotago.Output)) {
	for _, asset := range a {
		for outputID, output := range asset.basicOutputs {
			consumer(outputID, output)
		}
		for outputID, output := range asset.faucetOutputs {
			consumer(outputID, output)
		}
		for outputID, output := range asset.delegationOutputs {
			consumer(outputID, output)
		}
	}
}

func (a coreAPIAssets) forEachAccountAddress(consumer func(accountAddress *iotago.AccountAddress)) {
	for _, asset := range a {
		if asset.accountAddress == nil {
			// no account created in this slot
			continue
		}

		consumer(asset.accountAddress)
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

	// set later in the test by the default wallet
	commitmentID iotago.CommitmentID
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
		commitmentID:      iotago.EmptyCommitmentID,
	}
}

func prepareAssets(d *dockertestframework.DockerTestFramework, totalAssetsNum int) (coreAPIAssets, iotago.SlotIndex) {
	assets := make(coreAPIAssets)
	ctx := context.Background()

	latestSlot := iotago.SlotIndex(0)

	// create accounts
	accounts := d.CreateAccountsFromFaucet(ctx, totalAssetsNum)

	for i := 0; i < totalAssetsNum; i++ {
		// account
		wallet, account := accounts[i].Wallet(), accounts[i].Account()

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
		fmt.Printf(`Assets for slot %d:
    dataBlock ID:          %s
    txblock ID:            %s
    signedTx ID:           %s
    basic output ID:       %s
    faucet output ID:      %s
    delegation output ID:  %s`,
			valueBlockSlot,
			block.MustID().String(),
			valueBlock.MustID().String(),
			signedTx.MustID().String(),
			basicOutputID.String(),
			faucetOutput.ID.String(),
			delegationOutputData.ID.String())
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

	ctx, cancel := context.WithCancel(context.Background())

	// cancel the context when the test is done
	t.Cleanup(cancel)

	defaultClient := d.DefaultWallet().Client

	hrp := defaultClient.CommittedAPI().ProtocolParameters().Bech32HRP()

	// get the initial validators (those are the nodes added via AddValidatorNode)
	// they should be returned by the validators API
	initialValidators := d.AccountsFromNodes(d.Nodes()...)

	// copy the initial validators, so we can append the new validators
	expectedValidators := make([]string, len(initialValidators))
	copy(expectedValidators, initialValidators)

	// create 50 new validators
	validatorCount := 50
	implicitAccounts := d.CreateImplicitAccounts(ctx, validatorCount)

	blockIssuance, err := defaultClient.BlockIssuance(ctx)
	require.NoError(t, err)

	latestCommitmentSlot := blockIssuance.LatestCommitment.Slot
	// we can't set the staking start epoch too much in the future, because it is bound to the latest commitment slot plus MaxCommittableAge
	stakingStartEpoch := d.DefaultWallet().StakingStartEpochFromSlot(latestCommitmentSlot)

	// create accounts with staking feature for the validators
	var wg sync.WaitGroup
	validators := make([]*mock.AccountWithWallet, validatorCount)
	for i := range validatorCount {
		wg.Add(1)

		go func(validatorNr int) {
			defer wg.Done()

			// create account with staking feature for every validator
			validators[validatorNr] = d.CreateAccountFromImplicitAccount(implicitAccounts[validatorNr],
				blockIssuance,
				dockertestframework.WithStakingFeature(100, 1, stakingStartEpoch),
			)
			expectedValidators = append(expectedValidators, validators[validatorNr].Account().Address.Bech32(hrp))
		}(i)
	}
	wg.Wait()

	annoucementEpoch := stakingStartEpoch

	// check if we missed to announce the candidacy during the staking start epoch because it takes time to create the account.
	latestAcceptedBlockSlot := d.NodeStatus("V1").LatestAcceptedBlockSlot
	currentEpoch := defaultClient.CommittedAPI().TimeProvider().EpochFromSlot(latestAcceptedBlockSlot)
	if annoucementEpoch < currentEpoch {
		annoucementEpoch = currentEpoch
	}

	maxRegistrationSlot := dockertestframework.GetMaxRegistrationSlot(defaultClient.CommittedAPI(), annoucementEpoch)

	// the candidacy announcement needs to be done before the nearing threshold of the epoch
	// and we shouldn't start trying in the last possible slot, otherwise the tests might be wonky
	if latestAcceptedBlockSlot >= maxRegistrationSlot {
		// we are already too late, we can't issue candidacy payloads anymore, so lets start with the next epoch
		annoucementEpoch++
	}

	// the candidacy announcement needs to be done before the nearing threshold
	maxRegistrationSlot = dockertestframework.GetMaxRegistrationSlot(defaultClient.CommittedAPI(), annoucementEpoch)

	// now wait until the start of the announcement epoch
	d.AwaitLatestAcceptedBlockSlot(defaultClient.CommittedAPI().TimeProvider().EpochStart(annoucementEpoch), true)

	// issue candidacy payload for each account
	wg = sync.WaitGroup{}
	for i := range validatorCount {
		wg.Add(1)

		go func(validatorNr int) {
			defer wg.Done()

			candidacyBlockID := d.IssueCandidacyPayloadFromAccount(ctx, validators[validatorNr].Wallet())
			fmt.Println("Issuing candidacy payload for account", validators[validatorNr].Account().ID, "in epoch", annoucementEpoch, "...", "blockID:", candidacyBlockID.ToHex())
			require.LessOrEqualf(d.Testing, candidacyBlockID.Slot(), maxRegistrationSlot, "candidacy announcement block slot is greater than max registration slot for the epoch (%d>%d)", candidacyBlockID.Slot(), maxRegistrationSlot)
		}(i)
	}
	wg.Wait()

	// wait until the end of the announcement epoch
	d.AwaitEpochFinalized()

	// check if all validators are returned from the validators API with pageSize 10
	actualValidators := getAllValidatorsOnEpoch(t, defaultClient, annoucementEpoch, 10)
	require.ElementsMatch(t, expectedValidators, actualValidators)

	// wait until the end of the next epoch, the newly added validators should be offline again
	// because they haven't issued candidacy annoucement for the next epoch
	d.AwaitEpochFinalized()

	actualValidators = getAllValidatorsOnEpoch(t, defaultClient, annoucementEpoch+1, 10)
	require.ElementsMatch(t, initialValidators, actualValidators)

	// the initital validators should be returned for epoch 0
	actualValidators = getAllValidatorsOnEpoch(t, defaultClient, 0, 10)
	require.ElementsMatch(t, initialValidators, actualValidators)
}

func Test_CoreAPI_ValidRequests(t *testing.T) {
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

	assetsPerSlot, lastSlot := prepareAssets(d, 5)

	d.AwaitFinalizedSlot(lastSlot, true)

	defaultClient := d.DefaultWallet().Client

	forEachNodeClient := func(consumer func(nodeName string, client mock.Client)) {
		for _, node := range d.Nodes() {
			client := d.Client(node.Name)
			consumer(node.Name, client)
		}
	}

	tests := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "Test_Info",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					resp, err := client.Info(context.Background())
					require.NoErrorf(t, err, "node %s", nodeName)
					require.NotNilf(t, resp, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_BlockByBlockID",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEachBlock(func(block *iotago.Block) {
						respBlock, err := client.BlockByBlockID(context.Background(), block.MustID())
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, respBlock, "node %s", nodeName)
						require.Equalf(t, block.MustID(), respBlock.MustID(), "node %s: BlockID of retrieved block does not match: %s != %s", nodeName, block.MustID(), respBlock.MustID())
					})
				})
			},
		},
		{
			name: "Test_BlockMetadataByBlockID",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEachBlock(func(block *iotago.Block) {
						resp, err := client.BlockMetadataByBlockID(context.Background(), block.MustID())
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)
						require.Equalf(t, block.MustID(), resp.BlockID, "node %s: BlockID of retrieved block does not match: %s != %s", nodeName, block.MustID(), resp.BlockID)
						require.Equalf(t, api.BlockStateFinalized, resp.BlockState, "node %s", nodeName)
					})

					assetsPerSlot.forEachReattachment(func(blockID iotago.BlockID) {
						resp, err := client.BlockMetadataByBlockID(context.Background(), blockID)
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)
						require.Equalf(t, blockID, resp.BlockID, "node %s: BlockID of retrieved block does not match: %s != %s", nodeName, blockID, resp.BlockID)
						require.Equalf(t, api.BlockStateFinalized, resp.BlockState, "node %s", nodeName)
					})
				})
			},
		},
		{
			name: "Test_BlockWithMetadata",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEachBlock(func(block *iotago.Block) {
						resp, err := client.BlockWithMetadataByBlockID(context.Background(), block.MustID())
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)
						require.Equalf(t, block.MustID(), resp.Block.MustID(), "node %s: BlockID of retrieved block does not match: %s != %s", nodeName, block.MustID(), resp.Block.MustID())
						require.Equalf(t, api.BlockStateFinalized, resp.Metadata.BlockState, "node %s", nodeName)
					})
				})
			},
		},
		{
			name: "Test_BlockIssuance",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					resp, err := client.BlockIssuance(context.Background())
					require.NoErrorf(t, err, "node %s", nodeName)
					require.NotNilf(t, resp, "node %s", nodeName)
					require.GreaterOrEqualf(t, len(resp.StrongParents), 1, "node %s: there should be at least 1 strong parent provided", nodeName)
				})
			},
		},
		{
			name: "Test_CommitmentBySlot",
			testFunc: func(t *testing.T) {
				// first we get the commitment IDs for each slot from the default wallet
				// this step is necessary to get the commitment IDs for each slot for the following tests
				assetsPerSlot.forEach(func(slot iotago.SlotIndex, assets *coreAPISlotAssets) {
					resp, err := defaultClient.CommitmentBySlot(context.Background(), slot)
					require.NoError(t, err)
					require.NotNil(t, resp)

					commitmentID := resp.MustID()
					if commitmentID == iotago.EmptyCommitmentID {
						require.Failf(t, "commitment is empty", "slot %d", slot)
					}

					assets.commitmentID = commitmentID
				})

				// now we check if the commitment IDs are the same for each node
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEach(func(slot iotago.SlotIndex, assets *coreAPISlotAssets) {
						resp, err := client.CommitmentBySlot(context.Background(), slot)
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)

						// check if the commitment ID is the same as the one from the default wallet
						require.Equalf(t, assets.commitmentID, resp.MustID(), "node %s: commitment in slot %d does not match the default wallet: %s != %s", nodeName, slot, assets.commitmentID, resp.MustID())
					})
				})
			},
		},
		{
			name: "Test_CommitmentByID",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEach(func(slot iotago.SlotIndex, assets *coreAPISlotAssets) {
						resp, err := client.CommitmentByID(context.Background(), assets.commitmentID)
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)
						require.Equalf(t, assets.commitmentID, resp.MustID(), "node %s: commitment in slot %d does not match the default wallet: %s != %s", nodeName, slot, assets.commitmentID, resp.MustID())
					})
				})
			},
		},
		{
			name: "Test_CommitmentUTXOChangesByID",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEach(func(slot iotago.SlotIndex, assets *coreAPISlotAssets) {
						resp, err := client.CommitmentUTXOChangesByID(context.Background(), assets.commitmentID)
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)
						assetsPerSlot.assertUTXOOutputIDsInSlot(t, assets.commitmentID.Slot(), resp.CreatedOutputs, resp.ConsumedOutputs)
						require.Equalf(t, assets.commitmentID, resp.CommitmentID, "node %s: CommitmentID of retrieved UTXO changes does not match: %s != %s", nodeName, assets.commitmentID, resp.CommitmentID)
					})
				})
			},
		},
		{
			name: "Test_CommitmentUTXOChangesFullByID",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEach(func(slot iotago.SlotIndex, assets *coreAPISlotAssets) {
						resp, err := client.CommitmentUTXOChangesFullByID(context.Background(), assets.commitmentID)
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)
						assetsPerSlot.assertUTXOOutputsInSlot(t, assets.commitmentID.Slot(), resp.CreatedOutputs, resp.ConsumedOutputs)
						require.Equalf(t, assets.commitmentID, resp.CommitmentID, "node %s: CommitmentID of retrieved UTXO changes does not match: %s != %s", nodeName, assets.commitmentID, resp.CommitmentID)
					})
				})
			},
		},
		{
			name: "Test_CommitmentUTXOChangesBySlot",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEach(func(slot iotago.SlotIndex, assets *coreAPISlotAssets) {
						resp, err := client.CommitmentUTXOChangesBySlot(context.Background(), assets.commitmentID.Slot())
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)
						assetsPerSlot.assertUTXOOutputIDsInSlot(t, assets.commitmentID.Slot(), resp.CreatedOutputs, resp.ConsumedOutputs)
						require.Equalf(t, assets.commitmentID, resp.CommitmentID, "node %s: CommitmentID of retrieved UTXO changes does not match: %s != %s", nodeName, assets.commitmentID, resp.CommitmentID)
					})
				})
			},
		},
		{
			name: "Test_CommitmentUTXOChangesFullBySlot",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEach(func(slot iotago.SlotIndex, assets *coreAPISlotAssets) {
						resp, err := client.CommitmentUTXOChangesFullBySlot(context.Background(), assets.commitmentID.Slot())
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)
						assetsPerSlot.assertUTXOOutputsInSlot(t, assets.commitmentID.Slot(), resp.CreatedOutputs, resp.ConsumedOutputs)
						require.Equalf(t, assets.commitmentID, resp.CommitmentID, "node %s: CommitmentID of retrieved UTXO changes does not match: %s != %s", nodeName, assets.commitmentID, resp.CommitmentID)
					})
				})
			},
		},
		{
			name: "Test_OutputByID",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEachOutput(func(outputID iotago.OutputID, output iotago.Output) {
						resp, err := client.OutputByID(context.Background(), outputID)
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)
						require.EqualValuesf(t, output, resp, "node %s: Output created is different than retrieved from the API: %s != %s", nodeName, output, resp)
					})
				})
			},
		},
		{
			name: "Test_OutputMetadata",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEachOutput(func(outputID iotago.OutputID, output iotago.Output) {
						resp, err := client.OutputMetadataByID(context.Background(), outputID)
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)
						require.EqualValuesf(t, outputID, resp.OutputID, "node %s: OutputID of retrieved output does not match: %s != %s", nodeName, outputID, resp.OutputID)
						require.EqualValuesf(t, outputID.TransactionID(), resp.Included.TransactionID, "node %s: TransactionID of retrieved output does not match: %s != %s", nodeName, outputID.TransactionID(), resp.Included.TransactionID)
					})
				})
			},
		},
		{
			name: "Test_OutputWithMetadata",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEachOutput(func(outputID iotago.OutputID, output iotago.Output) {
						out, outMetadata, err := client.OutputWithMetadataByID(context.Background(), outputID)
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, outMetadata, "node %s", nodeName)
						require.NotNilf(t, out, "node %s", nodeName)
						require.EqualValuesf(t, outputID, outMetadata.OutputID, "node %s: OutputID of retrieved output does not match: %s != %s", nodeName, outputID, outMetadata.OutputID)
						require.EqualValuesf(t, outputID.TransactionID(), outMetadata.Included.TransactionID, "node %s: TransactionID of retrieved output does not match: %s != %s", nodeName, outputID.TransactionID(), outMetadata.Included.TransactionID)
						require.EqualValuesf(t, output, out, "node %s: OutputID of retrieved output does not match: %s != %s", nodeName, output, out)
					})
				})
			},
		},
		{
			name: "Test_TransactionByID",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEachTransaction(func(transaction *iotago.SignedTransaction, firstAttachmentID iotago.BlockID) {
						txID := transaction.Transaction.MustID()
						resp, err := client.TransactionByID(context.Background(), txID)
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)
						require.EqualValuesf(t, txID, resp.MustID(), "node %s: TransactionID of retrieved transaction does not match: %s != %s", nodeName, txID, resp.MustID())
					})
				})
			},
		},
		{
			name: "Test_TransactionsIncludedBlock",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEachTransaction(func(transaction *iotago.SignedTransaction, firstAttachmentID iotago.BlockID) {
						resp, err := client.TransactionIncludedBlock(context.Background(), transaction.Transaction.MustID())
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)
						require.EqualValuesf(t, firstAttachmentID, resp.MustID(), "node %s: BlockID of retrieved transaction does not match: %s != %s", nodeName, firstAttachmentID, resp.MustID())
					})
				})
			},
		},
		{
			name: "Test_TransactionsIncludedBlockMetadata",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEachTransaction(func(transaction *iotago.SignedTransaction, firstAttachmentID iotago.BlockID) {
						resp, err := client.TransactionIncludedBlockMetadata(context.Background(), transaction.Transaction.MustID())
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)
						require.EqualValuesf(t, api.BlockStateFinalized, resp.BlockState, "node %s: BlockState of retrieved transaction does not match: %s != %s", nodeName, api.BlockStateFinalized, resp.BlockState)
						require.EqualValuesf(t, firstAttachmentID, resp.BlockID, "node %s: BlockID of retrieved transaction does not match: %s != %s", nodeName, firstAttachmentID, resp.BlockID)
					})
				})
			},
		},
		{
			name: "Test_TransactionsMetadata",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEachTransaction(func(transaction *iotago.SignedTransaction, firstAttachmentID iotago.BlockID) {
						resp, err := client.TransactionMetadata(context.Background(), transaction.Transaction.MustID())
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)
						require.Equalf(t, api.TransactionStateFinalized, resp.TransactionState, "node %s: TransactionState of retrieved transaction does not match: %s != %s", nodeName, api.TransactionStateFinalized, resp.TransactionState)
						require.EqualValuesf(t, resp.EarliestAttachmentSlot, firstAttachmentID.Slot(), "node %s: EarliestAttachmentSlot of retrieved transaction does not match: %s != %s", nodeName, resp.EarliestAttachmentSlot, firstAttachmentID.Slot())
					})
				})
			},
		},
		{
			name: "Test_Congestion",
			testFunc: func(t *testing.T) {
				// node allows to get account only for the slot newer than lastCommittedSlot - MCA, we need fresh commitment
				infoRes, err := defaultClient.Info(context.Background())
				require.NoError(t, err)

				commitment, err := defaultClient.CommitmentBySlot(context.Background(), infoRes.Status.LatestCommitmentID.Slot())
				require.NoError(t, err)

				commitmentID := commitment.MustID()

				// wait a bit to make sure the commitment is available on all nodes
				time.Sleep(1 * time.Second)

				assetsPerSlot.forEachAccountAddress(func(accountAddress *iotago.AccountAddress) {
					// get the BIC for the account from the default wallet
					congestionResponse, err := defaultClient.Congestion(context.Background(), accountAddress, 0, commitmentID)
					require.NoError(t, err)
					require.NotNil(t, congestionResponse)

					bic := congestionResponse.BlockIssuanceCredits

					// check if all nodes have the same BIC for this account
					forEachNodeClient(func(nodeName string, client mock.Client) {
						resp, err := client.Congestion(context.Background(), accountAddress, 0, commitmentID)
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)

						require.Equalf(t, bic, resp.BlockIssuanceCredits, "node %s: BIC for account %s does not match: %d != %d", nodeName, accountAddress.Bech32(iotago.PrefixTestnet), bic, resp.BlockIssuanceCredits)
					})
				})
			},
		},
		{
			name: "Test_Validators",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					pageSize := uint64(3)
					resp, err := client.Validators(context.Background(), pageSize)
					require.NoErrorf(t, err, "node %s", nodeName)
					require.NotNilf(t, resp, "node %s", nodeName)
					require.Equalf(t, int(pageSize), len(resp.Validators), "node %s: There should be exactly %d validators returned on the first page", nodeName, pageSize)

					resp, err = client.Validators(context.Background(), pageSize, resp.Cursor)
					require.NoErrorf(t, err, "node %s", nodeName)
					require.NotNilf(t, resp, "node %s", nodeName)
					require.Equalf(t, 1, len(resp.Validators), "node %s: There should be only one validator returned on the last page", nodeName)
				})
			},
		},
		{
			name: "Test_ValidatorsAll",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					resp, all, err := client.ValidatorsAll(context.Background())
					require.NoErrorf(t, err, "node %s", nodeName)
					require.Truef(t, all, "node %s: All validators should be returned", nodeName)
					require.Equalf(t, 4, len(resp.Validators), "node %s: There should be exactly 4 validators returned", nodeName)
				})
			},
		},
		{
			name: "Test_Rewards",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					assetsPerSlot.forEachOutput(func(outputID iotago.OutputID, output iotago.Output) {
						if output.Type() != iotago.OutputDelegation {
							return
						}

						resp, err := client.Rewards(context.Background(), outputID)
						require.NoErrorf(t, err, "node %s", nodeName)
						require.NotNilf(t, resp, "node %s", nodeName)

						timeProvider := client.CommittedAPI().TimeProvider()
						outputCreationEpoch := timeProvider.EpochFromSlot(outputID.Slot())

						if outputCreationEpoch == timeProvider.CurrentEpoch() {
							// rewards are zero, because we do not wait for the epoch end
							require.EqualValuesf(t, 0, resp.Rewards, "node %s: Rewards should be zero", nodeName)
						} else {
							// rewards can be greater or equal to 0, since the delegation happened earlier
							require.GreaterOrEqualf(t, resp.Rewards, iotago.Mana(0), "node %s: Rewards should be greater or equal to zero", nodeName)
						}
					})
				})
			},
		},
		{
			name: "Test_Committee",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					resp, err := client.Committee(context.Background())
					require.NoErrorf(t, err, "node %s", nodeName)
					require.NotNilf(t, resp, "node %s", nodeName)
					require.EqualValuesf(t, 4, len(resp.Committee), "node %s: Committee length should be 4", nodeName)
				})
			},
		},
		{
			name: "Test_CommitteeWithEpoch",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					resp, err := client.Committee(context.Background(), 0)
					require.NoErrorf(t, err, "node %s", nodeName)
					require.Equalf(t, iotago.EpochIndex(0), resp.Epoch, "node %s: Epoch should be 0", nodeName)
					require.Equalf(t, 4, len(resp.Committee), "node %s: Committee length should be 4", nodeName)
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.testFunc(d.Testing)
		})
	}
}

func Test_CoreAPI_BadRequests(t *testing.T) {
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

	forEachNodeClient := func(consumer func(nodeName string, client mock.Client)) {
		for _, node := range d.Nodes() {
			client := d.Client(node.Name)
			consumer(node.Name, client)
		}
	}

	tests := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "Test_BlockByBlockID_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					blockID := tpkg.RandBlockID()
					respBlock, err := client.BlockByBlockID(context.Background(), blockID)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
					require.Nilf(t, respBlock, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_BlockMetadataByBlockID_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					blockID := tpkg.RandBlockID()
					resp, err := client.BlockMetadataByBlockID(context.Background(), blockID)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
					require.Nilf(t, resp, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_BlockWithMetadata_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					blockID := tpkg.RandBlockID()
					resp, err := client.BlockWithMetadataByBlockID(context.Background(), blockID)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
					require.Nilf(t, resp, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_CommitmentBySlot_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					slot := iotago.SlotIndex(1000_000_000)
					resp, err := client.CommitmentBySlot(context.Background(), slot)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
					require.Nilf(t, resp, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_CommitmentByID_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					committmentID := tpkg.RandCommitmentID()
					resp, err := client.CommitmentByID(context.Background(), committmentID)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
					require.Nilf(t, resp, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_CommitmentUTXOChangesByID_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					committmentID := tpkg.RandCommitmentID()
					resp, err := client.CommitmentUTXOChangesByID(context.Background(), committmentID)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
					require.Nilf(t, resp, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_CommitmentUTXOChangesFullByID_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					committmentID := tpkg.RandCommitmentID()

					resp, err := client.CommitmentUTXOChangesFullByID(context.Background(), committmentID)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
					require.Nilf(t, resp, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_CommitmentUTXOChangesBySlot_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					slot := iotago.SlotIndex(1000_000_000)
					resp, err := client.CommitmentUTXOChangesBySlot(context.Background(), slot)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
					require.Nilf(t, resp, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_CommitmentUTXOChangesFullBySlot_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					slot := iotago.SlotIndex(1000_000_000)

					resp, err := client.CommitmentUTXOChangesFullBySlot(context.Background(), slot)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
					require.Nilf(t, resp, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_OutputByID_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					outputID := tpkg.RandOutputID(0)
					resp, err := client.OutputByID(context.Background(), outputID)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
					require.Nilf(t, resp, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_OutputMetadata_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					outputID := tpkg.RandOutputID(0)

					resp, err := client.OutputMetadataByID(context.Background(), outputID)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
					require.Nilf(t, resp, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_OutputWithMetadata_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					outputID := tpkg.RandOutputID(0)

					out, outMetadata, err := client.OutputWithMetadataByID(context.Background(), outputID)
					require.Errorf(t, err, "node %s", nodeName)
					require.Nilf(t, out, "node %s", nodeName)
					require.Nilf(t, outMetadata, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_TransactionsIncludedBlock_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					txID := tpkg.RandTransactionID()
					resp, err := client.TransactionIncludedBlock(context.Background(), txID)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
					require.Nilf(t, resp, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_TransactionsIncludedBlockMetadata_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					txID := tpkg.RandTransactionID()

					resp, err := client.TransactionIncludedBlockMetadata(context.Background(), txID)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
					require.Nilf(t, resp, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_TransactionsMetadata_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					txID := tpkg.RandTransactionID()

					resp, err := client.TransactionMetadata(context.Background(), txID)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
					require.Nilf(t, resp, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_Congestion_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					accountAddress := tpkg.RandAccountAddress()
					commitmentID := tpkg.RandCommitmentID()
					resp, err := client.Congestion(context.Background(), accountAddress, 0, commitmentID)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
					require.Nilf(t, resp, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_Committee_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					resp, err := client.Committee(context.Background(), 4000)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusBadRequest), "node %s", nodeName)
					require.Nilf(t, resp, "node %s", nodeName)
				})
			},
		},
		{
			name: "Test_Rewards_Failure",
			testFunc: func(t *testing.T) {
				forEachNodeClient(func(nodeName string, client mock.Client) {
					outputID := tpkg.RandOutputID(0)
					resp, err := client.Rewards(context.Background(), outputID)
					require.Errorf(t, err, "node %s", nodeName)
					require.Truef(t, dockertestframework.IsStatusCode(err, http.StatusNotFound), "node %s", nodeName)
					require.Nilf(t, resp, "node %s", nodeName)
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.testFunc(d.Testing)
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
