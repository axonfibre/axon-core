//go:build dockertests

package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	"github.com/iotaledger/iota-core/tools/docker-network/tests/dockertestframework"
)

// Test_Payload_Nil tests that sending a nil payload does not result in a panic.
// This is a test to ensure issue #978 is fixed.
func Test_Payload_Nil_Test(t *testing.T) {
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

	// create account-1
	account := d.CreateAccountFromFaucet("account-1")

	// Issue a block with a nil payload.
	blk := lo.PanicOnErr(account.Wallet().CreateBasicBlock(ctx, "something", mock.WithPayload(nil)))
	d.SubmitBlock(ctx, blk.ProtocolBlock())

	// Wait for the epoch end to ensure the test does not exit early.
	d.AwaitEpochFinalized()
}
