package inx

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/serializer/v2/serix"
	inx "github.com/iotaledger/inx/go"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/mempool"
	iotago "github.com/iotaledger/iota.go/v4"
)

func (s *Server) ReadBlockIssuance(_ context.Context, req *inx.BlockIssuanceRequest) (*inx.BlockIssuanceResponse, error) {
	references := deps.Protocol.Engines.Main.Get().TipSelection.SelectTips(int(req.GetMaxStrongParentsCount()), int(req.GetMaxShallowLikeParentsCount()), int(req.GetMaxWeakParentsCount()))
	if len(references[iotago.StrongParentType]) == 0 {
		return nil, status.Errorf(codes.Unavailable, "no strong parents available")
	}

	// get the latest parent block issuing time
	var latestParentBlockIssuingTime time.Time

	checkParent := func(parentBlockID iotago.BlockID) error {
		parentBlock, exists := deps.Protocol.Engines.Main.Get().Block(parentBlockID)
		if !exists {
			// check if this is the genesis block
			if parentBlockID == deps.Protocol.CommittedAPI().ProtocolParameters().GenesisBlockID() {
				return nil
			}

			// or a root block
			rootBlocks, err := deps.Protocol.Engines.Main.Get().Storage.RootBlocks(parentBlockID.Slot())
			if err != nil {
				return status.Errorf(codes.Internal, "failed to get root blocks for slot %d: %s", parentBlockID.Slot(), err.Error())
			}

			isRootBlock, err := rootBlocks.Has(parentBlockID)
			if err != nil {
				return status.Errorf(codes.Internal, "failed to check if block %s is a root block: %s", parentBlockID, err.Error())
			}

			if isRootBlock {
				return nil
			}

			return status.Errorf(codes.NotFound, "no block found for block ID %s", parentBlockID)
		}

		if latestParentBlockIssuingTime.Before(parentBlock.ProtocolBlock().Header.IssuingTime) {
			latestParentBlockIssuingTime = parentBlock.ProtocolBlock().Header.IssuingTime
		}

		return nil
	}

	for _, parentType := range []iotago.ParentsType{iotago.StrongParentType, iotago.WeakParentType, iotago.ShallowLikeParentType} {
		for _, parentBlockID := range references[parentType] {
			if err := checkParent(parentBlockID); err != nil {
				return nil, ierrors.Wrap(err, "failed to retrieve parents")
			}
		}
	}

	latestCommitment := deps.Protocol.Engines.Main.Get().SyncManager.LatestCommitment()

	return &inx.BlockIssuanceResponse{
		StrongParents:                inx.NewBlockIds(references[iotago.StrongParentType]),
		WeakParents:                  inx.NewBlockIds(references[iotago.WeakParentType]),
		ShallowLikeParents:           inx.NewBlockIds(references[iotago.ShallowLikeParentType]),
		LatestParentBlockIssuingTime: inx.TimeToUint64(latestParentBlockIssuingTime),
		LatestFinalizedSlot:          uint32(deps.Protocol.Engines.Main.Get().SyncManager.LatestFinalizedSlot()),
		LatestCommitment:             inx.NewCommitmentWithBytes(latestCommitment.ID(), latestCommitment.Data()),
	}, nil
}

func (s *Server) ValidatePayload(_ context.Context, payload *inx.RawPayload) (*inx.PayloadValidationResponse, error) {
	if err := func() error {
		blockPayload, unwrapErr := payload.Unwrap(deps.Protocol.CommittedAPI(), serix.WithValidation())
		if unwrapErr != nil {
			return unwrapErr
		}

		switch typedPayload := blockPayload.(type) {
		case *iotago.SignedTransaction:
			memPool := deps.Protocol.Engines.Main.Get().Ledger.MemPool()

			inputReferences, inputsErr := memPool.VM().Inputs(typedPayload.Transaction)
			if inputsErr != nil {
				return inputsErr
			}

			loadedInputs := make([]mempool.State, 0)
			for _, inputReference := range inputReferences {
				metadata, metadataErr := memPool.StateMetadata(inputReference)
				if metadataErr != nil {
					return metadataErr
				}

				loadedInputs = append(loadedInputs, metadata.State())
			}

			executionContext, validationErr := memPool.VM().ValidateSignatures(typedPayload, loadedInputs)
			if validationErr != nil {
				return validationErr
			}

			return lo.Return2(memPool.VM().Execute(executionContext, typedPayload.Transaction))

		case *iotago.TaggedData:
			// TaggedData is always valid if serix decoding was successful
			return nil

		case *iotago.CandidacyAnnouncement:
			panic("TODO: implement me")
		default:
			// We're switching on the Go payload type here, so we can only run into the default case
			// if we added a new payload type and have not handled it above. In this case we want to panic.
			panic("all supported payload types should be handled above")
		}
	}(); err != nil {
		//nolint:nilerr // this is expected behavior
		return &inx.PayloadValidationResponse{IsValid: false, Error: err.Error()}, nil
	}

	return &inx.PayloadValidationResponse{IsValid: true}, nil
}
