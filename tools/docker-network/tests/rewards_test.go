//go:build dockertests

package tests

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	"github.com/iotaledger/iota-core/tools/docker-network/tests/dockertestframework"
	iotago "github.com/iotaledger/iota.go/v4"
)

// Test_ValidatorRewards tests the rewards for a validator.
// 1. Create 2 accounts with staking feature.
// 2. Issue candidacy payloads for the accounts and wait until the accounts is in the committee.
// 3. One of the account issues 5 validation blocks per slot, the other account issues 1 validation block per slot until claiming slot is reached.
// 4. Claim rewards and check if the mana increased as expected, the account that issued less validation blocks should have less mana.
func Test_ValidatorRewards(t *testing.T) {
	d := dockertestframework.NewDockerTestFramework(t,
		dockertestframework.WithProtocolParametersOptions(
			append(
				dockertestframework.ShortSlotsAndEpochsProtocolParametersOptions,
				iotago.WithStakingOptions(2, 10, 10),
				iotago.WithRewardsOptions(8, 11, 2, 384),
				iotago.WithTargetCommitteeSize(32),
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

	// cancel the context when the test is done
	t.Cleanup(cancel)

	clt := d.DefaultWallet().Client

	// create two implicit accounts for "good" and "lazy" validator
	validatorCount := 2
	implicitAccounts := d.CreateImplicitAccounts(ctx, validatorCount)

	blockIssuance, err := clt.BlockIssuance(ctx)
	require.NoError(t, err)

	latestCommitmentSlot := blockIssuance.LatestCommitment.Slot

	// we want to start staking as soon as possible, but we also add another epoch as a buffer in case we miss the staking start epoch
	// to do the candidacy announcement because of the time it takes to create the account.
	stakingStartEpoch := d.DefaultWallet().StakingStartEpochFromSlot(latestCommitmentSlot)

	// we want to claim the rewards as soon as possible
	stakingEndEpoch := stakingStartEpoch + clt.CommittedAPI().ProtocolParameters().StakingUnbondingPeriod() + 1

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
				dockertestframework.WithStakingFeature(100, 1, stakingStartEpoch, stakingEndEpoch),
			)
		}(i)
	}
	wg.Wait()

	goodValidator := validators[0]
	lazyValidator := validators[1]

	goodValidatorInitialMana := goodValidator.Account().Output.StoredMana()
	lazyValidatorInitialMana := lazyValidator.Account().Output.StoredMana()

	// issue candidacy payloads for the validators in the background
	for _, validator := range validators {
		issueCandidacyAnnouncementsInBackground(ctx,
			d,
			validator.Wallet(),
			// we start with the next epoch in case the stakingStartEpoch is already at EpochNearingThreshold
			stakingStartEpoch+1,
			// we don't need to issue candidacy payloads for the last epoch
			stakingEndEpoch-1)
	}

	// make sure the account is in the committee, so it can issue validation blocks
	goodValidatorAddrBech32 := goodValidator.Account().Address.Bech32(clt.CommittedAPI().ProtocolParameters().Bech32HRP())
	lazyValidatorAddrBech32 := lazyValidator.Account().Address.Bech32(clt.CommittedAPI().ProtocolParameters().Bech32HRP())
	d.AssertCommittee(stakingStartEpoch+2, append(d.AccountsFromNodes(d.Nodes("V1", "V3", "V2", "V4")...), goodValidatorAddrBech32, lazyValidatorAddrBech32))

	// create a new wait group for the next step
	wg = sync.WaitGroup{}

	// issue validation blocks to have performance
	currentSlot := clt.CommittedAPI().TimeProvider().CurrentSlot()
	validationBlocksEndSlot := clt.CommittedAPI().TimeProvider().EpochEnd(stakingEndEpoch)
	secondsToWait := time.Duration(validationBlocksEndSlot-currentSlot) * time.Duration(clt.CommittedAPI().ProtocolParameters().SlotDurationInSeconds()) * time.Second
	fmt.Println("Issuing validation blocks, wait for ", secondsToWait, "until expected slot: ", validationBlocksEndSlot)

	issueValidationBlocksInBackground(ctx, d, &wg, goodValidator.Wallet(), currentSlot, validationBlocksEndSlot, 5)
	issueValidationBlocksInBackground(ctx, d, &wg, lazyValidator.Wallet(), currentSlot, validationBlocksEndSlot, 1)

	// wait until all validation blocks are issued
	wg.Wait()

	// claim rewards that put to the account output
	d.AwaitCommitment(validationBlocksEndSlot)
	d.ClaimRewardsForValidator(ctx, goodValidator)
	d.ClaimRewardsForValidator(ctx, lazyValidator)

	// check if the mana increased as expected
	goodValidatorFinalMana := goodValidator.Account().Output.StoredMana()
	lazyValidatorFinalMana := lazyValidator.Account().Output.StoredMana()

	require.Greater(t, goodValidatorFinalMana, goodValidatorInitialMana)
	require.Greater(t, lazyValidatorFinalMana, lazyValidatorInitialMana)

	// account that issued more validation blocks should have more mana
	require.Greater(t, goodValidatorFinalMana, lazyValidatorFinalMana)
}

// Test_DelegatorRewards tests the rewards for a delegator.
// 1. Create an account and delegate funds to a validator.
// 2. Wait long enough so there's rewards can be claimed.
// 3. Claim rewards and check if the mana increased as expected.
func Test_DelegatorRewards(t *testing.T) {
	d := dockertestframework.NewDockerTestFramework(t,
		dockertestframework.WithProtocolParametersOptions(
			append(
				dockertestframework.ShortSlotsAndEpochsProtocolParametersOptions,
				iotago.WithStakingOptions(3, 10, 10),
				iotago.WithRewardsOptions(8, 11, 2, 384),
				iotago.WithTargetCommitteeSize(32),
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
	delegatorWallet, _ := d.CreateAccountFromFaucet()
	clt := delegatorWallet.Client

	// delegate funds to V2
	delegationOutputData := d.DelegateToValidator(delegatorWallet, d.Node("V2").AccountAddress(t))
	d.AwaitCommitment(delegationOutputData.ID.CreationSlot())

	// check if V2 received the delegator stake
	v2Resp, err := clt.Validator(ctx, d.Node("V2").AccountAddress(t))
	require.NoError(t, err)
	require.Greater(t, v2Resp.PoolStake, v2Resp.ValidatorStake)

	// wait until next epoch so the rewards can be claimed
	//nolint:forcetypeassert
	expectedSlot := clt.CommittedAPI().TimeProvider().EpochStart(delegationOutputData.Output.(*iotago.DelegationOutput).StartEpoch + 2)
	if currentSlot := clt.CommittedAPI().TimeProvider().CurrentSlot(); currentSlot < expectedSlot {
		slotToWait := expectedSlot - currentSlot
		secToWait := time.Duration(slotToWait) * time.Duration(clt.CommittedAPI().ProtocolParameters().SlotDurationInSeconds()) * time.Second
		fmt.Println("Wait for ", secToWait, "until expected slot: ", expectedSlot)
		time.Sleep(secToWait)
	}

	// claim rewards that put to an basic output
	rewardsOutputID := d.ClaimRewardsForDelegator(ctx, delegatorWallet, delegationOutputData)

	// check if the mana increased as expected
	outputFromAPI, err := clt.OutputByID(ctx, rewardsOutputID)
	require.NoError(t, err)

	rewardsOutput := delegatorWallet.Output(rewardsOutputID)
	require.Equal(t, rewardsOutput.Output.StoredMana(), outputFromAPI.StoredMana())
}

// Test_DelayedClaimingRewards tests the delayed claiming rewards for a delegator.
// 1. Create an account and delegate funds to a validator.
// 2. Delay claiming rewards for the delegation and check if the delegated stake is removed from the validator.
// 3. Claim rewards and check to destroy the delegation output.
func Test_DelayedClaimingRewards(t *testing.T) {
	d := dockertestframework.NewDockerTestFramework(t,
		dockertestframework.WithProtocolParametersOptions(
			append(
				dockertestframework.ShortSlotsAndEpochsProtocolParametersOptions,
				iotago.WithStakingOptions(3, 10, 10),
				iotago.WithRewardsOptions(8, 11, 2, 384),
				iotago.WithTargetCommitteeSize(32),
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
	delegatorWallet, _ := d.CreateAccountFromFaucet()
	clt := delegatorWallet.Client

	{
		// delegate funds to V2
		delegationOutputData := d.DelegateToValidator(delegatorWallet, d.Node("V2").AccountAddress(t))
		d.AwaitCommitment(delegationOutputData.ID.CreationSlot())

		// check if V2 received the delegator stake
		v2Resp, err := clt.Validator(ctx, d.Node("V2").AccountAddress(t))
		require.NoError(t, err)
		require.Greater(t, v2Resp.PoolStake, v2Resp.ValidatorStake)

		// delay claiming rewards
		currentSlot := delegatorWallet.CurrentSlot()
		apiForSlot := clt.APIForSlot(currentSlot)
		latestCommitmentSlot := delegatorWallet.GetNewBlockIssuanceResponse().LatestCommitment.Slot
		delegationEndEpoch := dockertestframework.GetDelegationEndEpoch(apiForSlot, currentSlot, latestCommitmentSlot)
		delegationOutputData = d.DelayedClaimingTransition(ctx, delegatorWallet, delegationOutputData)
		d.AwaitCommitment(delegationOutputData.ID.CreationSlot())

		// the delegated stake should be removed from the validator, so the pool stake should equal to the validator stake
		v2Resp, err = clt.Validator(ctx, d.Node("V2").AccountAddress(t))
		require.NoError(t, err)
		require.Equal(t, v2Resp.PoolStake, v2Resp.ValidatorStake)

		// wait until next epoch to destroy the delegation
		expectedSlot := clt.CommittedAPI().TimeProvider().EpochStart(delegationEndEpoch)
		if currentSlot := delegatorWallet.CurrentSlot(); currentSlot < expectedSlot {
			slotToWait := expectedSlot - currentSlot
			secToWait := time.Duration(slotToWait) * time.Duration(clt.CommittedAPI().ProtocolParameters().SlotDurationInSeconds()) * time.Second
			fmt.Println("Wait for ", secToWait, "until expected slot: ", expectedSlot)
			time.Sleep(secToWait)
		}
		fmt.Println("Claim rewards for delegator")
		d.ClaimRewardsForDelegator(ctx, delegatorWallet, delegationOutputData)
	}

	{
		// delegate funds to V2
		delegationOutputData := d.DelegateToValidator(delegatorWallet, d.Node("V2").AccountAddress(t))

		// delay claiming rewards in the same slot of delegation
		delegationOutputData = d.DelayedClaimingTransition(ctx, delegatorWallet, delegationOutputData)
		d.AwaitCommitment(delegationOutputData.ID.CreationSlot())

		// the delegated stake should be 0, thus poolStake should be equal to validatorStake
		v2Resp, err := clt.Validator(ctx, d.Node("V2").AccountAddress(t))
		require.NoError(t, err)
		require.Equal(t, v2Resp.PoolStake, v2Resp.ValidatorStake)

		// wait until next epoch to destroy the delegation
		d.ClaimRewardsForDelegator(ctx, delegatorWallet, delegationOutputData)
	}
}

// issue candidacy announcements for the account in the background, one per epoch
func issueCandidacyAnnouncementsInBackground(ctx context.Context, d *dockertestframework.DockerTestFramework, wallet *mock.Wallet, epochStart iotago.EpochIndex, epochEnd iotago.EpochIndex) {
	go func() {
		fmt.Println("Issuing candidacy announcements for account", wallet.BlockIssuer.AccountData.ID, "in the background...")
		defer fmt.Println("Issuing candidacy announcements for account", wallet.BlockIssuer.AccountData.ID, "in the background... done!")

		for epoch := epochStart; epoch <= epochEnd; epoch++ {
			if ctx.Err() != nil {
				// context is canceled
				return
			}

			// wait until the epoch start is reached
			d.AwaitSlot(d.DefaultWallet().Client.CommittedAPI().TimeProvider().EpochStart(epoch))
			if ctx.Err() != nil {
				// context is canceled
				return
			}

			fmt.Println("Issuing candidacy payload for account", wallet.BlockIssuer.AccountData.ID, "in epoch", epoch, "...")
			committedAPI := d.DefaultWallet().Client.CommittedAPI()

			// the candidacy announcement needs to be done before the nearing threshold
			epochEndSlot := committedAPI.TimeProvider().EpochEnd(epoch)
			maxRegistrationSlot := epochEndSlot - committedAPI.ProtocolParameters().EpochNearingThreshold() - 1

			candidacyBlockID := d.IssueCandidacyPayloadFromAccount(ctx, wallet)
			require.LessOrEqualf(d.Testing, candidacyBlockID.Slot(), maxRegistrationSlot, "candidacy announcement block slot is greater than max registration slot for the empoch (%d>%d)", candidacyBlockID.Slot(), maxRegistrationSlot)
		}
	}()
}

// issue validation blocks for the account in the background, blocksPerSlot per slot with a cooldown between the blocks
func issueValidationBlocksInBackground(ctx context.Context, d *dockertestframework.DockerTestFramework, wg *sync.WaitGroup, wallet *mock.Wallet, startSlot, endSlot iotago.SlotIndex, blocksPerSlot int) {
	wg.Add(1)

	go func() {
		defer wg.Done()

		fmt.Println("Issuing validation blocks for wallet", wallet.Name, "in the background...")
		defer fmt.Println("Issuing validation blocks for wallet", wallet.Name, "in the background... done!")

		validationBlockCooldown := time.Duration(d.DefaultWallet().Client.CommittedAPI().ProtocolParameters().SlotDurationInSeconds()) * time.Second / time.Duration(blocksPerSlot)

		for slot := startSlot; slot <= endSlot; slot++ {
			if ctx.Err() != nil {
				// context is canceled
				return
			}

			// wait until the slot is reached
			d.AwaitSlot(slot)
			if ctx.Err() != nil {
				// context is canceled
				return
			}

			ts := time.Now()
			for validationBlockNr := range blocksPerSlot {
				if ctx.Err() != nil {
					// context is canceled
					return
				}

				fmt.Println("Issuing validation block nr.", validationBlockNr, "for wallet", wallet.Name, "in slot", slot, "...")
				wallet.CreateAndSubmitValidationBlock(ctx, "", nil)

				if validationBlockNr < blocksPerSlot-1 {
					// wait until the next validation block can be issued
					<-time.After(time.Until(ts.Add(time.Duration(validationBlockNr+1) * validationBlockCooldown)))
				}
			}
		}
	}()
}
