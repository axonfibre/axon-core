package management

import (
	"github.com/labstack/echo/v4"

	"github.com/iotaledger/hive.go/ierrors"
	"github.com/iotaledger/iota.go/v4/api"
)

func createSnapshots(_ echo.Context) (*api.CreateSnapshotResponse, error) {
	if deps.Protocol.Engines.Main.Get().IsSnapshotting() || deps.Protocol.Engines.Main.Get().Storage.IsPruning() {
		return nil, ierrors.WithMessage(echo.ErrServiceUnavailable, "node is already creating a snapshot or pruning is running")
	}

	targetSlot, filePath, err := deps.Protocol.Engines.Main.Get().ExportSnapshot(deps.SnapshotFilePath, true, true)
	if err != nil {
		return nil, ierrors.WithMessagef(echo.ErrInternalServerError, "creating snapshot failed: %w", err)
	}

	return &api.CreateSnapshotResponse{
		Slot:     targetSlot,
		FilePath: filePath,
	}, nil
}
