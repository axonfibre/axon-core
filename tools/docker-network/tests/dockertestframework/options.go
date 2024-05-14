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
	iotago.WithTimeProviderOptions(5, time.Now().Unix(), 10, 3),
	iotago.WithLivenessOptions(10, 10, 2, 4, 5),
	iotago.WithCongestionControlOptions(1, 1, 1, 200_000_000, 125_000_000, 50_000_000, 1000, 100),
	iotago.WithRewardsOptions(8, 10, 2, 384),
	iotago.WithTargetCommitteeSize(4),
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
