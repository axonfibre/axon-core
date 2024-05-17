//go:build dockertests

package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/iota-core/tools/docker-network/tests/dockertestframework"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/api"
)

func calcNextEpoch(nodeStatus *api.InfoResNodeStatus, timeProvider *iotago.TimeProvider, minEpoch iotago.EpochIndex) iotago.EpochIndex {
	currentEpoch := timeProvider.EpochFromSlot(nodeStatus.LatestAcceptedBlockSlot)

	if currentEpoch+1 > minEpoch {
		return currentEpoch + 1
	}

	return minEpoch
}

// Test_SmallerCommittee tests if the committee rotated to a smaller committee than targetCommitteeSize
// if less than targetCommitteeSize validators issued candidacy payloads.
// 1. Run docker network, targetCommitteeSize=4, with 4 validators running.
// 2. Shut down inx-validator of V2.
// 3. Check that committee of size 3 is selected in next epoch.
// 4. Restart inx-validator of V2.
// 5. Check that committee of size 4 is selected in next epoch.
func Test_SmallerCommittee(t *testing.T) {
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

	status := d.NodeStatus("V1")

	clt := d.DefaultWallet().Client
	initialEpoch := clt.CommittedAPI().TimeProvider().EpochFromSlot(status.LatestAcceptedBlockSlot)

	// stop inx-validator plugin of validator 2
	err = d.StopContainer(d.Node("V2").ContainerName)
	require.NoError(t, err)

	nextEpoch := calcNextEpoch(d.NodeStatus("V1"), clt.CommittedAPI().TimeProvider(), initialEpoch+2)
	d.AssertCommittee(nextEpoch, d.AccountsFromNodes(d.Nodes("V1", "V3", "V4")...))

	// restart inx-validator plugin of validator 2
	err = d.RestartContainer(d.Node("V2").ContainerName)
	require.NoError(t, err)

	nextEpoch = calcNextEpoch(d.NodeStatus("V1"), clt.CommittedAPI().TimeProvider(), nextEpoch+1)
	d.AssertCommittee(nextEpoch, d.AccountsFromNodes(d.Nodes()...))
}

// Test_ReuseDueToNoFinalization tests if the committee members are the same (reused) due to no slot finalization at epochNearingThreshold and recovery after finalization comes back.
// 1. Run docker network, targetCommitteeSize=4, with 4 validators running.
// 2. Shutdown inx-validator of V2 and V3.
// 3. Check if finalization stops and committee is reused (remains 4 committee members) in next epoch due to no finalization.
// 4. Restart inx-validator of V2.
// 5. Check that committee of size 3 (V1, V2, V4) is selected in next epoch and finalization occurs again from that epoch.
func Test_ReuseDueToNoFinalization(t *testing.T) {
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

	// stop 2 inx-validator plugins, finalization should stop
	err = d.StopContainer(d.Node("V2").ContainerName, d.Node("V3").ContainerName)
	require.NoError(t, err)

	clt := d.DefaultWallet().Client
	status := d.NodeStatus("V1")

	// store initial finalized slot
	prevFinalizedSlot := status.LatestFinalizedSlot
	currentEpoch := clt.CommittedAPI().TimeProvider().EpochFromSlot(prevFinalizedSlot)

	// due to no finalization, committee should be reused, remain 4 validators
	// we check 2 epochs ahead
	nextEpoch := calcNextEpoch(d.NodeStatus("V1"), clt.CommittedAPI().TimeProvider(), currentEpoch+2)
	d.AssertCommittee(nextEpoch, d.AccountsFromNodes(d.Nodes()...))

	// check if finalization stops
	d.AssertFinalizedSlot(func(nodeName string, latestFinalizedSlot iotago.SlotIndex) error {
		if prevFinalizedSlot == latestFinalizedSlot {
			// finalization should have stopped
			return nil
		}

		return ierrors.Errorf("No finalization should have happened, Previous finalized slot: %d, Latest finalized slot: %d, Node: %s", prevFinalizedSlot, latestFinalizedSlot, nodeName)
	})

	// revive 1 validator, committee size should be 3, finalization should resume
	err = d.RestartContainer(d.Node("V2").ContainerName)
	require.NoError(t, err)

	d.WaitUntilNodesHealthy()

	// check if V2 missed to announce the candidacy during inx-validator restart.
	latestAcceptedBlockSlot := d.NodeStatus("V1").LatestAcceptedBlockSlot
	annoucementStartEpoch := clt.CommittedAPI().TimeProvider().EpochFromSlot(latestAcceptedBlockSlot)
	maxRegistrationSlot := dockertestframework.GetMaxRegistrationSlot(clt.CommittedAPI(), annoucementStartEpoch)
	// the candidacy announcement needs to be done before the nearing threshold of the epoch
	if latestAcceptedBlockSlot >= maxRegistrationSlot {
		// it's too late for validator to issue candidacy payloads anymore, so we wait until the next epoch
		annoucementStartEpoch++
	}

	// we check if the committee is updated in the next epoch, after candidacy announcement have been processed
	checkCommitteeEpoch := annoucementStartEpoch + 1

	d.AssertCommittee(checkCommitteeEpoch, d.AccountsFromNodes(d.Nodes("V1", "V2", "V4")...))

	// wait finalization to catch up and check if the finalization resumes
	time.Sleep(5 * time.Second)
	d.AssertFinalizedSlot(func(nodeName string, latestFinalizedSlot iotago.SlotIndex) error {
		if prevFinalizedSlot < latestFinalizedSlot {
			return nil
		}

		return ierrors.Errorf("Finalization should have happened, Previous finalized slot: %d, Latest finalized slot: %d, Node: %s", prevFinalizedSlot, latestFinalizedSlot, nodeName)
	})
}

// Test_NoCandidacyPayload tests if committee is reused due to no candidates announced but slot finalized at epochNearingThreshold.
// 1. Run docker network, targetCommitteeSize=4, with 4 validators running.
// 2. Stop issuing candidacy payload on all validators.
// 3. Check finalization advances and the committee is reused in next epoch due to no candidates.
// 4. Start issuing candidacy payload on 3 validators only.
// 5. Check finalization advances and the committee is changed to 3 committee members.
func Test_NoCandidacyPayload(t *testing.T) {
	d := dockertestframework.NewDockerTestFramework(t,
		dockertestframework.WithProtocolParametersOptions(dockertestframework.ShortSlotsAndEpochsProtocolParametersOptionsFunc()...),
	)
	defer d.Stop()

	d.AddValidatorNode("V1", "docker-network-inx-validator-1-1", "http://localhost:8050", "rms1pzg8cqhfxqhq7pt37y8cs4v5u4kcc48lquy2k73ehsdhf5ukhya3y5rx2w6", false)
	d.AddValidatorNode("V2", "docker-network-inx-validator-2-1", "http://localhost:8060", "rms1pqm4xk8e9ny5w5rxjkvtp249tfhlwvcshyr3pc0665jvp7g3hc875k538hl", false)
	d.AddValidatorNode("V3", "docker-network-inx-validator-3-1", "http://localhost:8070", "rms1pp4wuuz0y42caz48vv876qfpmffswsvg40zz8v79sy8cp0jfxm4kunflcgt", false)
	d.AddValidatorNode("V4", "docker-network-inx-validator-4-1", "http://localhost:8040", "rms1pr8cxs3dzu9xh4cduff4dd4cxdthpjkpwmz2244f75m0urslrsvtsshrrjw", false)
	d.AddNode("node5", "docker-network-node-5-1", "http://localhost:8080")

	err := d.Run()
	require.NoError(t, err)

	d.WaitUntilNetworkReady()

	clt := d.DefaultWallet().Client
	status := d.NodeStatus("V1")
	prevFinalizedSlot := status.LatestFinalizedSlot
	fmt.Println("First finalized slot: ", prevFinalizedSlot)
	currentEpoch := clt.CommittedAPI().TimeProvider().EpochFromSlot(status.LatestAcceptedBlockSlot)

	d.AssertCommittee(currentEpoch+1, d.AccountsFromNodes(d.Nodes()...))

	// Due to no candidacy payloads, committee should be reused, remain 4 validators
	d.AssertCommittee(currentEpoch+2, d.AccountsFromNodes(d.Nodes()...))

	// check if finalization continues
	d.AssertFinalizedSlot(func(nodeName string, latestFinalizedSlot iotago.SlotIndex) error {
		if prevFinalizedSlot < latestFinalizedSlot {
			return nil
		}

		return ierrors.Errorf("Finalization should have happened, Previous finalized slot: %d, Latest finalized slot: %d, Node: %s", prevFinalizedSlot, latestFinalizedSlot, nodeName)
	})

	// Start issuing candidacy payloads for 3 validators, and check if committee size is 3
	d.StartIssueCandidacyPayload(d.Nodes("V1", "V2", "V3")...)
	d.AssertCommittee(currentEpoch+4, d.AccountsFromNodes(d.Nodes("V1", "V2", "V3")...))
}

// Test_Staking tests if an newly created account becomes a staker with staking feature.
// 1. Run docker network, targetCommitteeSize=3, with 4 validators running.
// 2. Create an account with staking feature.
// 3. Check if the account became a staker.
func Test_Staking(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// create implicit account for the validator
	implicitAccount := d.CreateImplicitAccount(ctx, "account-1")

	blockIssuance := implicitAccount.Wallet().GetNewBlockIssuanceResponse()

	latestCommitmentSlot := blockIssuance.LatestCommitment.Slot
	stakingStartEpoch := d.DefaultWallet().StakingStartEpochFromSlot(latestCommitmentSlot)

	// create account with staking feature for the validator
	accountWithWallet := d.CreateAccountFromImplicitAccount(implicitAccount,
		blockIssuance,
		dockertestframework.WithStakingFeature(100, 1, stakingStartEpoch),
	)

	d.AssertValidatorExists(accountWithWallet.Account().Address)
}

// Test_Delegation tests if committee changed due to delegation.
// initial settings are exact the same: V1 = V2 = V3 = V4, so committee is selected regarding the accountID order which is V4 > V1 > V3 > V2
// 1. Run docker network, targetCommitteeSize=3, with 4 validators running. Committee members are: V1, V3, V4
// 2. Create an account for delegation.
// 3. Delegate requested faucet funds to V2, V2 should replace V3 as a committee member. (V2 > V4 > V1 > V3)
// 4. Delegate requested faucet funds to V3, V3 should replace V1 as a committee member. (V3 > V2 > V4 > V1)
func Test_Delegation(t *testing.T) {
	d := dockertestframework.NewDockerTestFramework(t,
		dockertestframework.WithProtocolParametersOptions(
			append(
				dockertestframework.ShortSlotsAndEpochsProtocolParametersOptionsFunc(),
				iotago.WithTargetCommitteeSize(3),
			)...,
		),
	)
	defer d.Stop()

	// V1 pubKey in hex: 0x293dc170d9a59474e6d81cfba7f7d924c09b25d7166bcfba606e53114d0a758b
	d.AddValidatorNode("V1", "docker-network-inx-validator-1-1", "http://localhost:8050", "rms1pzg8cqhfxqhq7pt37y8cs4v5u4kcc48lquy2k73ehsdhf5ukhya3y5rx2w6")
	// V2 pubKey in hex: 0x05c1de274451db8de8182d64c6ee0dca3ae0c9077e0b4330c976976171d79064
	d.AddValidatorNode("V2", "docker-network-inx-validator-2-1", "http://localhost:8060", "rms1pqm4xk8e9ny5w5rxjkvtp249tfhlwvcshyr3pc0665jvp7g3hc875k538hl")
	// V3 pubKey in hex: 0x1e4b21eb51dcddf65c20db1065e1f1514658b23a3ddbf48d30c0efc926a9a648
	d.AddValidatorNode("V3", "docker-network-inx-validator-3-1", "http://localhost:8070", "rms1pp4wuuz0y42caz48vv876qfpmffswsvg40zz8v79sy8cp0jfxm4kunflcgt")
	// V4 pubKey in hex: 0xc9ceac37d293155a578381aa313ee74edfa3ac73ee930d045564aae7771e8ffe
	d.AddValidatorNode("V4", "docker-network-inx-validator-4-1", "http://localhost:8040", "rms1pr8cxs3dzu9xh4cduff4dd4cxdthpjkpwmz2244f75m0urslrsvtsshrrjw")
	d.AddNode("node5", "docker-network-node-5-1", "http://localhost:8080")

	err := d.Run()
	require.NoError(t, err)

	d.WaitUntilNetworkReady()

	// create an account to perform delegation
	account := d.CreateAccountFromFaucet("account-1")

	// delegate all faucet funds to V2, V2 should replace V3
	//nolint:forcetypeassert
	delegationOutputData := d.DelegateToValidator(account.Wallet(), d.Node("V2").AccountAddress(t))
	d.AssertCommittee(delegationOutputData.Output.(*iotago.DelegationOutput).StartEpoch+1, d.AccountsFromNodes(d.Nodes("V1", "V2", "V4")...))

	// delegate all faucet funds to V3, V3 should replace V1
	delegationOutputData = d.DelegateToValidator(account.Wallet(), d.Node("V3").AccountAddress(t))
	d.AssertCommittee(delegationOutputData.Output.(*iotago.DelegationOutput).StartEpoch+1, d.AccountsFromNodes(d.Nodes("V2", "V3", "V4")...))
}
