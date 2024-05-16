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
)

func Test_SyncFromSnapshot(t *testing.T) {
	d := dockertestframework.NewDockerTestFramework(t,
		dockertestframework.WithProtocolParametersOptions(
			append(
				dockertestframework.ShortSlotsAndEpochsProtocolParametersOptionsFunc(),
				iotago.WithTargetCommitteeSize(3),
			)...,
		),
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

	ctx := context.Background()
	clt := d.DefaultWallet().Client

	createAccountAndDelegateTo := func(receiver *dockertestframework.Node, name string) (*mock.Wallet, *mock.AccountData, *mock.OutputData) {
		delegatorAccount := d.CreateAccountFromFaucet(name)
		clt := delegatorAccount.Wallet().Client

		// delegate funds to receiver
		delegationOutputData := d.DelegateToValidator(delegatorAccount.Wallet(), receiver.AccountAddress(t))
		d.AwaitCommittedSlot(delegationOutputData.ID.CreationSlot(), true)

		// check if receiver received the delegator stake
		resp, err := clt.Validator(ctx, receiver.AccountAddress(t))
		require.NoError(t, err)
		require.Greater(t, resp.PoolStake, resp.ValidatorStake)

		return delegatorAccount.Wallet(), delegatorAccount.Account(), delegationOutputData
	}

	v1DelegatorWallet, v1DelegatorAccountData, v1DelegationOutputData := createAccountAndDelegateTo(d.Node("V1"), "account-1")
	v2DelegatorWallet, v2DelegatorAccountData, v2DelegationOutputData := createAccountAndDelegateTo(d.Node("V2"), "account-2")

	//nolint:forcetypeassert
	currentEpoch := clt.CommittedAPI().TimeProvider().CurrentEpoch()
	expectedEpoch := v2DelegationOutputData.Output.(*iotago.DelegationOutput).StartEpoch + 2
	for range expectedEpoch - currentEpoch {
		d.AwaitEpochFinalized()
	}
	d.AssertCommittee(expectedEpoch, d.AccountsFromNodes(d.Nodes("V1", "V2", "V4")...))

	// claim rewards for v1 delegator
	v1DelegatorRewardsOutputID := d.ClaimRewardsForDelegator(ctx, v1DelegatorWallet, v1DelegationOutputData)

	// check if the mana increased as expected
	node5Clt := d.Client("node5")
	outputFromAPI, err := node5Clt.OutputByID(ctx, v1DelegatorRewardsOutputID)
	require.NoError(t, err)

	rewardsOutput := v1DelegatorWallet.Output(v1DelegatorRewardsOutputID)
	require.Equal(t, rewardsOutput.Output.StoredMana(), outputFromAPI.StoredMana())

	d.AwaitEpochFinalized()

	managementClient, err := clt.Management(getContextWithTimeout(5 * time.Second))
	require.NoError(t, err)

	// take the snapshot and restart node5
	{
		fmt.Println("Create the first snapshot...")
		response, err := managementClient.CreateSnapshot(getContextWithTimeout(5 * time.Second))
		require.NoError(t, err)

		// Deletes the database of node5 and restarts it with the just created snapshot.
		d.ResetNode("node5", response.FilePath)

		d.AwaitEpochFinalized()
		d.AwaitEpochFinalized()

		// check if the committee is the same among nodes
		currentEpoch := clt.CommittedAPI().TimeProvider().CurrentEpoch()
		d.AssertCommittee(currentEpoch, d.AccountsFromNodes(d.Nodes("V1", "V2", "V4")...))

		// check if the account and rewardsOutput are available
		node5Clt = d.Client("node5")
		_, err = node5Clt.Validator(ctx, v1DelegatorAccountData.Address)
		require.NoError(t, err)
		_, err = node5Clt.Validator(ctx, v2DelegatorAccountData.Address)
		require.NoError(t, err)

		_, err = node5Clt.OutputByID(ctx, v1DelegatorRewardsOutputID)
		require.NoError(t, err)

	}

	// claim rewards for V2 delegator, and see if the output is available on node5
	v2DelegatorRewardsOutputID := d.ClaimRewardsForDelegator(ctx, v2DelegatorWallet, v2DelegationOutputData)
	_, err = node5Clt.OutputByID(ctx, v2DelegatorRewardsOutputID)
	require.NoError(t, err)

	// create V3 delegator, the committee should change to V1, V3, V4
	v3DelegatorWallet, v3DelegatorAccountData, v3DelegationOutputData := createAccountAndDelegateTo(d.Node("V3"), "account-3")
	currentEpoch = clt.CommittedAPI().TimeProvider().CurrentEpoch()
	expectedEpoch = v3DelegationOutputData.Output.(*iotago.DelegationOutput).StartEpoch + 1
	for range expectedEpoch - currentEpoch {
		d.AwaitEpochFinalized()
	}

	d.AssertCommittee(expectedEpoch, d.AccountsFromNodes(d.Nodes("V1", "V3", "V4")...))

	d.AwaitEpochFinalized()

	// take the snapshot again and restart node5
	{
		fmt.Println("Create the second snapshot...")
		response, err := managementClient.CreateSnapshot(getContextWithTimeout(5 * time.Second))
		require.NoError(t, err)

		// Deletes the database of node5 and restarts it with the just created snapshot.
		d.ResetNode("node5", response.FilePath)
		currentEpoch = clt.CommittedAPI().TimeProvider().EpochFromSlot(v3DelegatorWallet.CurrentSlot())
		d.AssertCommittee(currentEpoch, d.AccountsFromNodes(d.Nodes("V1", "V3", "V4")...))

		node5Clt = d.Client("node5")
		_, err = node5Clt.Validator(ctx, v1DelegatorAccountData.Address)
		require.NoError(t, err)
		_, err = node5Clt.Validator(ctx, v2DelegatorAccountData.Address)
		require.NoError(t, err)
		_, err = node5Clt.Validator(ctx, v3DelegatorAccountData.Address)
		require.NoError(t, err)

		_, err = node5Clt.OutputByID(ctx, v1DelegatorRewardsOutputID)
		require.NoError(t, err)
		_, err = node5Clt.OutputByID(ctx, v2DelegatorRewardsOutputID)
		require.NoError(t, err)
	}
}
