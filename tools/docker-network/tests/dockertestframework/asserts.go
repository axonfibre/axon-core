//go:build dockertests

package dockertestframework

import (
	"context"
	"fmt"
	"sort"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	iotago "github.com/iotaledger/iota.go/v4"
)

func (d *DockerTestFramework) AssertIndexerAccount(account *mock.AccountData) {
	d.Eventually(func() error {
		ctx := context.TODO()
		indexerClt, err := d.defaultWallet.Client.Indexer(ctx)
		if err != nil {
			return err
		}

		outputID, output, _, err := indexerClt.Account(ctx, account.Address)
		if err != nil {
			return err
		}

		assert.EqualValues(d.fakeTesting, account.OutputID, *outputID)
		assert.EqualValues(d.fakeTesting, account.Output, output)

		return nil
	})
}

func (d *DockerTestFramework) AssertIndexerFoundry(foundryID iotago.FoundryID) {
	d.Eventually(func() error {
		ctx := context.TODO()
		indexerClt, err := d.defaultWallet.Client.Indexer(ctx)
		if err != nil {
			return err
		}

		_, _, _, err = indexerClt.Foundry(ctx, foundryID)
		if err != nil {
			return err
		}

		return nil
	})
}

func (d *DockerTestFramework) AssertValidatorExists(accountAddr *iotago.AccountAddress) {
	d.Eventually(func() error {
		for _, node := range d.Nodes() {
			_, err := d.Client(node.Name).Validator(context.TODO(), accountAddr)
			if err != nil {
				return err
			}
		}

		return nil
	})
}

func (d *DockerTestFramework) AssertCommittee(expectedEpoch iotago.EpochIndex, expectedCommitteeMember []string) {
	fmt.Println("Wait for committee selection..., expected epoch: ", expectedEpoch, ", expected committee size: ", len(expectedCommitteeMember))
	defer fmt.Println("Wait for committee selection... done!")

	sort.Strings(expectedCommitteeMember)

	d.AwaitLatestAcceptedBlockSlot(d.defaultWallet.Client.CommittedAPI().TimeProvider().EpochStart(expectedEpoch), true)

	d.Eventually(func() error {
		for _, node := range d.Nodes() {
			resp, err := d.Client(node.Name).Committee(context.TODO(), expectedEpoch)
			if err != nil {
				return err
			}

			if resp.Epoch != expectedEpoch {
				return ierrors.Errorf("expected epoch %d, but got %d", expectedEpoch, resp.Epoch)
			}

			members := make([]string, len(resp.Committee))
			for i, member := range resp.Committee {
				members[i] = member.AddressBech32
			}

			sort.Strings(members)
			if !lo.Equal(expectedCommitteeMember, members) {
				return ierrors.Errorf("committee members does not match as expected, expected: %v, actual: %v", expectedCommitteeMember, members)
			}
		}

		return nil
	})
}

func (d *DockerTestFramework) AssertFinalizedSlot(condition func(nodeName string, latestFinalizedSlot iotago.SlotIndex) error) {
	for _, node := range d.Nodes() {
		status := d.NodeStatus(node.Name)

		err := condition(node.Name, status.LatestFinalizedSlot)
		require.NoError(d.Testing, err)
	}
}
