package mock

import (
	"context"

	"github.com/iotaledger/hive.go/runtime/options"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
)

func (w *Wallet) CreateBasicBlock(ctx context.Context, blockName string, opts ...options.Option[BasicBlockParams]) (*blocks.Block, error) {
	return w.BlockIssuer.CreateBasicBlock(ctx, blockName, opts...)
}

func (w *Wallet) CreateAndSubmitBasicBlock(ctx context.Context, blockName string, opts ...options.Option[BasicBlockParams]) (*blocks.Block, error) {
	return w.BlockIssuer.CreateAndSubmitBasicBlock(ctx, blockName, opts...)
}

func (w *Wallet) CreateValidationBlock(ctx context.Context, blockName string, node *Node, opts ...options.Option[ValidationBlockParams]) (*blocks.Block, error) {
	return w.BlockIssuer.CreateValidationBlock(ctx, blockName, node, opts...)
}

func (w *Wallet) CreateAndSubmitValidationBlock(ctx context.Context, blockName string, node *Node, opts ...options.Option[ValidationBlockParams]) (*blocks.Block, error) {
	return w.BlockIssuer.CreateAndSubmitValidationBlock(ctx, blockName, node, opts...)
}
