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
	engine           *engine.Engine
	forkingPoint     *model.Commitment
	cumulativeWeight uint64
}

func NewCommitmentVerifier(mainEngine *engine.Engine, forkingPoint *model.Commitment) *CommitmentVerifier {
	return &CommitmentVerifier{
		engine:           mainEngine,
		forkingPoint:     forkingPoint,
		cumulativeWeight: forkingPoint.CumulativeWeight(),
	}
}

func (c *CommitmentVerifier) verifyCommitment(commitment *model.Commitment, attestations []*iotago.Attestation, merkleProof *merklehasher.Proof[iotago.Identifier]) (iotago.BlockIDs, uint64, error) {
	// 1. Verify that the provided attestations are indeed the ones that were included in the commitment.
	tree := ads.NewMap[iotago.AccountID, *iotago.Attestation](mapdb.NewMapDB(),
		iotago.Identifier.Bytes,
		iotago.IdentifierFromBytes,
		func(attestation *iotago.Attestation) ([]byte, error) {
			apiForVersion, err := c.engine.APIForVersion(attestation.ProtocolVersion)
			if err != nil {
				return nil, ierrors.Wrapf(err, "failed to get API for version %d", attestation.ProtocolVersion)
			}

			return apiForVersion.Encode(attestation)
		},
		func(bytes []byte) (*iotago.Attestation, int, error) {
			version, _, err := iotago.VersionFromBytes(bytes)
			if err != nil {
				return nil, 0, ierrors.Wrap(err, "failed to determine version")
			}

			a := new(iotago.Attestation)
			apiForVersion, err := c.engine.APIForVersion(version)
			if err != nil {
				return nil, 0, ierrors.Wrapf(err, "failed to get API for version %d", version)
			}
			n, err := apiForVersion.Decode(bytes, a)

			return a, n, err
		},
	)

	for _, att := range attestations {
		tree.Set(att.IssuerID, att)
	}
	if !iotago.VerifyProof(merkleProof, iotago.Identifier(tree.Root()), commitment.RootsID()) {
		return nil, 0, ierrors.Errorf("invalid merkle proof for attestations for commitment %s", commitment.ID())
	}

	// 2. Verify attestations.
	blockIDs, seatCount, err := c.verifyAttestations(attestations)
	if err != nil {
		return nil, 0, ierrors.Wrapf(err, "error validating attestations for commitment %s", commitment.ID())
	}

	// 3. Verify that cumulative weight of commitment matches (as an upper bound) with calculated weight from attestations.
	// This is necessary due to the following edge case that can happen in the window of forking point and the current state of the other chain:
	//	1. A public key is added to an account.
	//     We do not count a seat for the issuer for this slot and the computed CW will be lower than the CW in
	//	   the commitment. This is fine, since this is a rare occasion and a heavier chain will become heavier anyway, eventually.
	//	   It will simply take a bit longer to accumulate enough CW so that the chain-switch rule kicks in.
	//	TODO: what happens in an extreme case where all change their keys?
	//
	// 2. A public key is removed from an account.
	//    We count the seat for the issuer for this slot even though we shouldn't have. According to the protocol, a valid
	//    chain with such a block can never exist because the block itself (here provided as an attestation) would be invalid.
	//    However, we do not know about this yet since we do not have the latest ledger state.
	//    In a rare and elaborate scenario, an attacker might have acquired such removed (private) keys and could
	//    fabricate attestations and thus a theoretically heavier chain (solely when looking on the chain backed by attestations)
	//    than it actually is. Nodes might consider to switch to this chain, even though it is invalid which will be discovered
	//    before the candidate chain/engine is activated.
	c.cumulativeWeight += seatCount
	if c.cumulativeWeight <= commitment.CumulativeWeight() {
		return nil, 0, ierrors.Errorf("invalid cumulative weight for commitment %s", commitment.ID())
	}

	// TODO: when activating the candidate engine we need to make sure that the its indeed the same chain as the one we verified here.

	return blockIDs, c.cumulativeWeight, nil
}

func (c *CommitmentVerifier) verifyAttestations(attestations []*iotago.Attestation) (iotago.BlockIDs, uint64, error) {
	visitedIdentities := ds.NewSet[iotago.AccountID]()
	var blockIDs iotago.BlockIDs
	var seatCount uint64

	for _, att := range attestations {
		// 1. Make sure the public key used to sign is valid for the given issuerID.
		//    We ignore attestations that don't have a valid public key for the given issuerID.
		//    1. The attestation might be fake.
		//    2. The issuer might have added a new public key in the meantime, but we don't know about it yet
		//       since we only have the ledger state at the forking point.

		// TODO: here we need to use a different function to get an old ledgerstate (at the forking point) instead
		//  of the current one based on the progress of the current chain.
		account, exists, err := c.engine.Ledger.Account(att.IssuerID, c.forkingPoint.Index())
		// We always need to have the account for a validator.
		if err != nil {
			return nil, 0, ierrors.Wrapf(err, "error getting account for issuerID %s", att.IssuerID)
		}
		if !exists {
			return nil, 0, ierrors.Errorf("account for issuerID %s does not exist", att.IssuerID)
		}

		edSig, isEdSig := att.Signature.(*iotago.Ed25519Signature)
		if !isEdSig {
			return nil, 0, ierrors.Errorf("only ed2519 signatures supported, got %s", att.Signature.Type())
		}

		// We found the account but we don't know the public key used to sign this block/attestation. Ignore.
		if !account.PubKeys.Has(edSig.PublicKey) {
			continue
		}

		api, err := c.engine.APIForVersion(att.ProtocolVersion)
		if err != nil {
			return nil, 0, ierrors.Wrap(err, "error determining API for attestation")
		}

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
