//go:build dockertests

package dockertestframework

import (
	"fmt"
	"time"

	"github.com/iotaledger/hive.go/runtime/options"
	"github.com/iotaledger/iota-core/pkg/testsuite/snapshotcreator"
	"github.com/iotaledger/iota-core/tools/genesis-snapshot/presets"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/builder"
)

var DefaultProtocolParametersOptions = []options.Option[iotago.V3ProtocolParameters]{
	iotago.WithNetworkOptions(fmt.Sprintf("docker-tests-%d", time.Now().Unix()), iotago.PrefixTestnet),
}

// ShortSlotsAndEpochsProtocolParametersOptions sets the protocol parameters to have 5s slots and 40s epochs.
var ShortSlotsAndEpochsProtocolParametersOptions = []options.Option[iotago.V3ProtocolParameters]{
	iotago.WithStorageOptions(100, 1, 10, 100, 100, 100),
	iotago.WithWorkScoreOptions(500, 110_000, 7_500, 40_000, 90_000, 50_000, 40_000, 70_000, 5_000, 15_000),
	//iotago.WithTimeProviderOptions(0, time.Now().Unix(), 10, 13),
	iotago.WithTimeProviderOptions(5, time.Now().Unix(), 10, 3),
	//iotago.WithLivenessOptions(15, 30, 10, 20, 60),
	iotago.WithLivenessOptions(10, 10, 2, 4, 5),
	iotago.WithSupplyOptions(1813620509061365, 63, 1, 17, 32, 21, 70),
	//iotago.WithCongestionControlOptions(1, 1, 1, 400_000_000, 250_000_000, 50_000_000, 1000, 100),
	iotago.WithCongestionControlOptions(1, 1, 1, 200_000_000, 125_000_000, 50_000_000, 1000, 100),
	iotago.WithStakingOptions(10, 10, 10),
	iotago.WithVersionSignalingOptions(7, 5, 7),
	//iotago.WithRewardsOptions(8, 11, 2, 384),
	iotago.WithRewardsOptions(8, 10, 2, 384),
	//iotago.WithTargetCommitteeSize(32),
	iotago.WithTargetCommitteeSize(4),
	iotago.WithChainSwitchingThreshold(3),
}

// DefaultAccountOptions are the default snapshot options for the docker network.
func DefaultAccountOptions(protocolParams *iotago.V3ProtocolParameters) []options.Option[snapshotcreator.Options] {
	return []options.Option[snapshotcreator.Options]{
		snapshotcreator.WithAccounts(presets.AccountsDockerFunc(protocolParams)...),
		snapshotcreator.WithBasicOutputs(presets.BasicOutputsDocker...),
	}
}

func WithFaucetURL(url string) options.Option[DockerTestFramework] {
	return func(d *DockerTestFramework) {
		d.optsFaucetURL = url
	}
}

func WithProtocolParametersOptions(protocolParameterOptions ...options.Option[iotago.V3ProtocolParameters]) options.Option[DockerTestFramework] {
	return func(d *DockerTestFramework) {
		d.optsProtocolParameterOptions = protocolParameterOptions
	}
}

func WithSnapshotOptions(snapshotOptions ...options.Option[snapshotcreator.Options]) options.Option[DockerTestFramework] {
	return func(d *DockerTestFramework) {
		d.optsSnapshotOptions = snapshotOptions
	}
}

func WithWaitForSync(waitForSync time.Duration) options.Option[DockerTestFramework] {
	return func(d *DockerTestFramework) {
		d.optsWaitForSync = waitForSync
	}
}

func WithWaitFor(waitFor time.Duration) options.Option[DockerTestFramework] {
	return func(d *DockerTestFramework) {
		d.optsWaitFor = waitFor
	}
}

func WithTick(tick time.Duration) options.Option[DockerTestFramework] {
	return func(d *DockerTestFramework) {
		d.optsTick = tick
	}
}

func WithStakingFeature(amount iotago.BaseToken, fixedCost iotago.Mana, startEpoch iotago.EpochIndex, optEndEpoch ...iotago.EpochIndex) options.Option[builder.AccountOutputBuilder] {
	return func(accountBuilder *builder.AccountOutputBuilder) {
		accountBuilder.Staking(amount, fixedCost, startEpoch, optEndEpoch...)
	}
}
