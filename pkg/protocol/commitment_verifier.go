package protocol

import (
	"github.com/iotaledger/hive.go/ads"
	"github.com/iotaledger/hive.go/ds"
	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/kvstore/mapdb"
	"github.com/iotaledger/iota-core/pkg/model"
	"github.com/iotaledger/iota-core/pkg/protocol/engine"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/merklehasher"
)

type CommitmentVerifier struct {
	engine *engine.Engine
}

func NewCommitmentVerifier(mainEngine *engine.Engine) *CommitmentVerifier {
	return &CommitmentVerifier{
		engine: mainEngine,
	}
}

func (c *CommitmentVerifier) verifyCommitment(prevCommitment, commitment *model.Commitment, attestations []*iotago.Attestation, merkleProof *merklehasher.Proof[iotago.Identifier]) (iotago.BlockIDs, error) {
	// 1. Verify that the provided attestations are indeed the ones that were included in the commitment.
	tree := ads.NewMap[iotago.AccountID, *iotago.Attestation](mapdb.NewMapDB(),
		iotago.Identifier.Bytes,
		iotago.IdentifierFromBytes,
		func(attestation *iotago.Attestation) ([]byte, error) {
			return c.engine.APIForVersion(attestation.ProtocolVersion).Encode(attestation)
		},
		func(bytes []byte) (*iotago.Attestation, int, error) {
			a := new(iotago.Attestation)
			n, err := c.engine.APIForVersion(bytes[0]).Decode(bytes, a)

			return a, n, err
		},
	)

	for _, att := range attestations {
		tree.Set(att.IssuerID, att)
	}
	if !iotago.VerifyProof(merkleProof, iotago.Identifier(tree.Root()), commitment.RootsID()) {
		return nil, ierrors.Errorf("invalid merkle proof for attestations for commitment %s", commitment.ID())
	}

	// 2. Verify attestations.
	blockIDs, seatCount, err := c.verifyAttestations(attestations)
	if err != nil {
		return nil, ierrors.Wrapf(err, "error validating attestations for commitment %s", commitment.ID())
	}

	// 3. Verify cumulative weight of commitment matches with calculated weight from attestations.
	if prevCommitment.CumulativeWeight()+seatCount != commitment.CumulativeWeight() {
		return nil, ierrors.Errorf("invalid cumulative weight for commitment %s", commitment.ID())
	}

	return blockIDs, nil
}

func (c *CommitmentVerifier) verifyAttestations(attestations []*iotago.Attestation) (iotago.BlockIDs, uint64, error) {
	visitedIdentities := ds.NewSet[iotago.AccountID]()
	var blockIDs iotago.BlockIDs
	var seatCount uint64

	for _, att := range attestations {
		// TODO: 1. Make sure the public key used to sign is valid for the given issuerID.
		//  First, this can be based on the latest commonly known ledger state.
		//  Later, this needs to include a proof of added/removed public keys.

		api := c.engine.APIForVersion(att.ProtocolVersion)

		// 2. Verify the signature of the attestation.
		if valid, err := att.VerifySignature(api); !valid {
			if err != nil {
				return nil, 0, ierrors.Wrap(err, "error validating attestation signature")
			}

			return nil, 0, ierrors.New("invalid attestation signature")
		}

		// 3. A valid set of attestations can't contain multiple attestations from the same issuerID.
		if visitedIdentities.Has(att.IssuerID) {
			return nil, 0, ierrors.Errorf("issuerID %s contained in multiple attestations", att.IssuerID)
		}

		// TODO: this might differ if we have a Accounts with changing weights depending on the SlotIndex/epoch
		attestationBlockID, err := att.BlockID(api)
		if err != nil {
			return nil, 0, ierrors.Wrap(err, "error calculating blockID from attestation")
		}
		if _, seatExists := c.engine.SybilProtection.SeatManager().Committee(attestationBlockID.Index()).GetSeat(att.IssuerID); seatExists {
			seatCount++
		}

		visitedIdentities.Add(att.IssuerID)

		blockID, err := att.BlockID(api)
		if err != nil {
			return nil, 0, ierrors.Wrap(err, "error calculating blockID from attestation")
		}

		blockIDs = append(blockIDs, blockID)
	}

	return blockIDs, seatCount, nil
}
