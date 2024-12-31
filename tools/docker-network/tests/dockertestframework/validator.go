//go:build dockertests

package dockertestframework

import (
	"context"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	iotago "github.com/iotaledger/iota.go/v4"
)

func (d *DockerTestFramework) StartIssueCandidacyPayload(nodes ...*Node) {
	if len(nodes) == 0 {
		return
	}

	for _, node := range nodes {
		node.IssueCandidacyPayload = true
	}

	err := d.DockerComposeUp(true)
	require.NoError(d.Testing, err)
}

func (d *DockerTestFramework) StopIssueCandidacyPayload(nodes ...*Node) {
	if len(nodes) == 0 {
		return
	}

	for _, node := range nodes {
		node.IssueCandidacyPayload = false
	}

	err := d.DockerComposeUp(true)
	require.NoError(d.Testing, err)
}

func (d *DockerTestFramework) IssueCandidacyPayloadFromAccount(ctx context.Context, wallet *mock.Wallet) iotago.BlockID {
	block, err := wallet.CreateAndSubmitBasicBlock(ctx, "candidacy_payload", mock.WithPayload(&iotago.CandidacyAnnouncement{}))
	require.NoError(d.Testing, err)

	return block.ID()
}
