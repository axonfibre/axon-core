//go:build dockertests

package tests

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/iota-core/tools/docker-network/tests/dockertestframework"
)

// Test_AccountTransitions follows the account state transition flow described in:
// 1. Create account-1.
// 2. Create account-2.
// 3. account-1 requests faucet funds then allots 1000 mana to account-2.
// 4. account-2 requests faucet funds then creates native tokens.
func Test_AccountTransitions(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())

	// cancel the context when the test is done
	t.Cleanup(cancel)

	// create account-1
	accounts := d.CreateAccountsFromFaucet(ctx, 2, "account-1", "account-2")
	account1 := accounts[0]
	account2 := accounts[1]

	// allot 1000 mana from account-1 to account-2
	fmt.Println("Allotting mana from account-1 to account-2")
	d.RequestFaucetFundsAndAllotManaTo(account1.Wallet(), account2.Account(), 1000)

	// create native token
	fmt.Println("Creating native token")
	d.CreateNativeToken(account1.Wallet(), 5_000_000, 10_000_000_000)
}
