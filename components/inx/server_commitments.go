package inx

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/hive.go/kvstore"
	"github.com/iotaledger/hive.go/runtime/event"
	"github.com/iotaledger/hive.go/runtime/workerpool"
	inx "github.com/iotaledger/inx/go"
	"github.com/iotaledger/iota-core/pkg/model"
	iotago "github.com/iotaledger/iota.go/v4"
)

func inxCommitment(commitment *model.Commitment) *inx.Commitment {
	if commitment == nil {
		return nil
	}

	return &inx.Commitment{
		CommitmentId: inx.NewCommitmentId(commitment.ID()),
		Commitment: &inx.RawCommitment{
			Data: commitment.Data(),
		},
	}
}

func (s *Server) ListenToCommitments(req *inx.SlotRangeRequest, srv inx.INX_ListenToCommitmentsServer) error {
	createCommitmentPayloadForSlotAndSend := func(slot iotago.SlotIndex) error {
		commitment, err := deps.Protocol.Engines.Main.Get().Storage.Commitments().Load(slot)
		if err != nil {
			if ierrors.Is(err, kvstore.ErrKeyNotFound) {
				return status.Errorf(codes.NotFound, "commitment slot %d not found", slot)
			}

			return status.Errorf(codes.Internal, "failed to load commitment for slot %d: %s", slot, err.Error())
		}

		if err := srv.Send(inxCommitment(commitment)); err != nil {
			return ierrors.Wrap(err, "send error")
		}

		return nil
	}

	sendSlotsRange := func(startSlot iotago.SlotIndex, endSlot iotago.SlotIndex) error {
		for currentSlot := startSlot; currentSlot <= endSlot; currentSlot++ {
			if err := createCommitmentPayloadForSlotAndSend(currentSlot); err != nil {
				return err
			}
		}

		return nil
	}

	// if a startSlot is given, we send all available commitments including the start slot.
	// if an endSlot is given, we send all available commitments up to and including min(latestCommitmentSlot, endSlot).
	// if no startSlot is given, but an endSlot, we don't send previous commitments.
	sendPreviousSlots := func(startSlot iotago.SlotIndex, endSlot iotago.SlotIndex) (iotago.SlotIndex, error) {
		if startSlot == 0 {
			// no need to send previous commitments
			return 0, nil
		}

		latestCommitment := deps.Protocol.Engines.Main.Get().SyncManager.LatestCommitment()

		if startSlot > latestCommitment.Slot() {
			// no need to send previous commitments
			return 0, nil
		}

		// Stream all available commitments first
		prunedEpoch, hasPruned := deps.Protocol.Engines.Main.Get().SyncManager.LastPrunedEpoch()
		if hasPruned && startSlot <= deps.Protocol.CommittedAPI().TimeProvider().EpochEnd(prunedEpoch) {
			return 0, status.Errorf(codes.InvalidArgument, "given startSlot %d is older than the current pruningSlot %d", startSlot, deps.Protocol.CommittedAPI().TimeProvider().EpochEnd(prunedEpoch))
		}

		if endSlot == 0 || endSlot > latestCommitment.Slot() {
			endSlot = latestCommitment.Slot()
		}

		if err := sendSlotsRange(startSlot, endSlot); err != nil {
			return 0, err
		}

		return endSlot, nil
	}

	stream := &streamRange{
		start: iotago.SlotIndex(req.GetStartSlot()),
		end:   iotago.SlotIndex(req.GetEndSlot()),
	}

	var err error
	stream.lastSent, err = sendPreviousSlots(stream.start, stream.end)
	if err != nil {
		return err
	}

	if stream.isBounded() && stream.lastSent >= stream.end {
		// We are done sending, so close the stream
		return nil
	}

	catchUpFunc := func(start iotago.SlotIndex, end iotago.SlotIndex) error {
		err := sendSlotsRange(start, end)
		if err != nil {
			err := ierrors.Wrap(err, "sendSlotsRange error")
			Component.LogError(err.Error())

			return err
		}

		return nil
	}

	sendFunc := func(_ iotago.SlotIndex, payload *inx.Commitment) error {
		if err := srv.Send(payload); err != nil {
			err := ierrors.Wrap(err, "send error")
			Component.LogError(err.Error())

			return err
		}

		return nil
	}

	var innerErr error
	ctx, cancel := context.WithCancel(Component.Daemon().ContextStopped())

	wp := workerpool.New("ListenToCommitments", workerpool.WithWorkerCount(workerCount)).Start()

	unhook := deps.Protocol.Events.Engine.Notarization.LatestCommitmentUpdated.Hook(func(commitment *model.Commitment) {
		done, err := handleRangedSend1(commitment.Slot(), inxCommitment(commitment), stream, catchUpFunc, sendFunc)
		switch {
		case err != nil:
			innerErr = err
			cancel()

		case done:
			cancel()
		}
	}, event.WithWorkerPool(wp)).Unhook

	<-ctx.Done()
	unhook()

	// We need to wait until all tasks are done, otherwise we might call
	// "SendMsg" and "CloseSend" in parallel on the grpc stream, which is
	// not safe according to the grpc docs.
	wp.Shutdown()
	wp.ShutdownComplete.Wait()

	return innerErr
}

func (s *Server) ForceCommitUntil(_ context.Context, slot *inx.SlotRequest) (*inx.NoParams, error) {
	// If the chain manager is aware of a commitments on the main chain, then do not force commit.
	// The node should wait to warpsync those slots and use those commitments to avoid potentially creating a diverging commitment.
	unwrappedSlot := slot.Unwrap()
	if latestChainCommitment := deps.Protocol.Chains.Main.Get().LatestCommitment.Get(); latestChainCommitment.Slot() >= unwrappedSlot {
		return nil, ierrors.Errorf("chain manager is aware of a newer commitment (%s) than target slot %d", latestChainCommitment, unwrappedSlot)
	}

	err := deps.Protocol.Engines.Main.Get().Notarization.ForceCommitUntil(unwrappedSlot)
	if err != nil {
		return nil, ierrors.Wrapf(err, "error while performing force commit until %d", slot.GetSlot())
	}

	return &inx.NoParams{}, nil
}
func (s *Server) ReadCommitment(_ context.Context, req *inx.CommitmentRequest) (*inx.Commitment, error) {
	commitmentSlot := iotago.SlotIndex(req.GetCommitmentSlot())

	if req.GetCommitmentId() != nil {
		commitmentSlot = req.GetCommitmentId().Unwrap().Slot()
	}

	commitment, err := deps.Protocol.Engines.Main.Get().Storage.Commitments().Load(commitmentSlot)
	if err != nil {
		if ierrors.Is(err, kvstore.ErrKeyNotFound) {
			return nil, status.Errorf(codes.NotFound, "commitment slot %d not found", req.GetCommitmentSlot())
		}

		return nil, status.Errorf(codes.Internal, "failed to load commitment for slot %d: %s", commitmentSlot, err.Error())
	}

	if req.GetCommitmentId() != nil {
		// If it was requested by id, make sure the id matches the commitment.
		if commitment.ID() != req.GetCommitmentId().Unwrap() {
			return nil, status.Errorf(codes.NotFound, "commitment id %s not found, found %s instead", req.GetCommitmentId().Unwrap(), commitment.ID())
		}
	}

	return inxCommitment(commitment), nil
}
