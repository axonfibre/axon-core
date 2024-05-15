//go:build dockertests

package dockertestframework

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/stretchr/testify/require"

	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/api"
)

func (d *DockerTestFramework) WaitUntilFaucetHealthy() {
	fmt.Println("Wait until the faucet is healthy...")
	defer fmt.Println("Wait until the faucet is healthy... done!")

	d.Eventually(func() error {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, d.optsFaucetURL+"/health", nil)
		if err != nil {
			return err
		}

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			return ierrors.Errorf("faucet is not healthy, status code: %d", res.StatusCode)
		}

		return nil
	}, true)
}

func (d *DockerTestFramework) SendFaucetRequest(ctx context.Context, wallet *mock.Wallet, receiveAddr iotago.Address) {
	cltAPI := wallet.Client.CommittedAPI()
	addrBech := receiveAddr.Bech32(cltAPI.ProtocolParameters().Bech32HRP())

	type EnqueueRequest struct {
		Address string `json:"address"`
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.optsFaucetURL+"/api/enqueue", func() io.Reader {
		jsonData, _ := json.Marshal(&EnqueueRequest{
			Address: addrBech,
		})

		return bytes.NewReader(jsonData)
	}())
	require.NoError(d.Testing, err)

	req.Header.Set("Content-Type", api.MIMEApplicationJSON)

	res, err := http.DefaultClient.Do(req)
	require.NoError(d.Testing, err)
	defer res.Body.Close()

	require.Equal(d.Testing, http.StatusAccepted, res.StatusCode)
}

// RequestFaucetFunds requests faucet funds for the given address type, and returns the outputID of the received funds.
func (d *DockerTestFramework) RequestFaucetFunds(ctx context.Context, wallet *mock.Wallet, addressType iotago.AddressType) *mock.OutputData {
	var address iotago.Address
	if addressType == iotago.AddressImplicitAccountCreation {
		address = wallet.ImplicitAccountCreationAddress(wallet.BlockIssuer.AccountData.AddressIndex)
	} else {
		address = wallet.Address()
	}

	d.SendFaucetRequest(ctx, wallet, address)

	outputID, output, err := d.AwaitAddressUnspentOutputAccepted(ctx, wallet, address)
	require.NoError(d.Testing, err)

	outputData := &mock.OutputData{
		ID:           outputID,
		Address:      address,
		AddressIndex: wallet.BlockIssuer.AccountData.AddressIndex,
		Output:       output,
	}
	wallet.AddOutput("faucet funds", outputData)

	fmt.Printf("Faucet funds received, txID: %s, amount: %d, mana: %d\n", outputID.TransactionID().ToHex(), output.BaseTokenAmount(), output.StoredMana())

	return outputData
}

// RequestFaucetFundsAndAllotManaTo requests faucet funds then uses it to allots mana from one account to another.
func (d *DockerTestFramework) RequestFaucetFundsAndAllotManaTo(fromWallet *mock.Wallet, to *mock.AccountData, manaToAllot iotago.Mana) {
	// requesting faucet funds for allotment
	ctx := context.TODO()
	fundsOutputID := d.RequestFaucetFunds(ctx, fromWallet, iotago.AddressEd25519)
	clt := fromWallet.Client

	signedTx := fromWallet.AllotManaFromBasicOutput(
		"allotment_tx",
		fundsOutputID,
		manaToAllot,
		to.ID,
	)
	preAllotmentCommitmentID := fromWallet.GetNewBlockIssuanceResponse().LatestCommitment.MustID()
	block, err := fromWallet.CreateAndSubmitBasicBlock(ctx, "allotment", mock.WithPayload(signedTx))
	require.NoError(d.Testing, err)
	fmt.Println("Allot mana transaction sent, blkID:", block.ID().ToHex(), ", txID:", signedTx.Transaction.MustID().ToHex(), ", slot:", block.ID().Slot())

	d.AwaitTransactionPayloadAccepted(ctx, signedTx.Transaction.MustID())

	// allotment is updated when the transaction is committed
	d.AwaitCommittedSlot(block.ID().Slot())

	// check if the mana is allotted
	toCongestionResp, err := clt.Congestion(ctx, to.Address, 0, preAllotmentCommitmentID)
	require.NoError(d.Testing, err)
	oldBIC := toCongestionResp.BlockIssuanceCredits

	toCongestionResp, err = clt.Congestion(ctx, to.Address, 0)
	require.NoError(d.Testing, err)
	newBIC := toCongestionResp.BlockIssuanceCredits

	decayedOldBIC, err := clt.LatestAPI().ManaDecayProvider().DecayManaBySlots(iotago.Mana(oldBIC), preAllotmentCommitmentID.Slot(), block.ID().Slot())
	expectedBIC := iotago.BlockIssuanceCredits(decayedOldBIC + manaToAllot)
	require.Equal(d.Testing, expectedBIC, newBIC)
}
