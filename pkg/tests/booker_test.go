package tests

import (
	"testing"
	"time"

	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/hive.go/runtime/options"
	"github.com/iotaledger/iota-core/pkg/core/acceptance"
	"github.com/iotaledger/iota-core/pkg/protocol"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/blocks"
	"github.com/iotaledger/iota-core/pkg/testsuite"
	"github.com/iotaledger/iota-core/pkg/testsuite/mock"
	iotago "github.com/iotaledger/iota.go/v4"
	"github.com/iotaledger/iota.go/v4/builder"
)

func Test_IssuingTransactionsOutOfOrder(t *testing.T) {
	ts := testsuite.NewTestSuite(t)
	defer ts.Shutdown()

	node1 := ts.AddValidatorNode("node1")
	wallet := ts.AddDefaultWallet(node1)
	ts.Run(true, map[string][]options.Option[protocol.Protocol]{})

	tx1 := wallet.CreateBasicOutputsEquallyFromInput("tx1", 1, "Genesis:0")
	tx2 := wallet.CreateBasicOutputsEquallyFromInput("tx2", 1, "tx1:0")

	// issue block1 that contains an unsolid transaction
	{
		ts.IssueBasicBlockWithOptions("block1", wallet, tx2)

		ts.AssertTransactionsExist(wallet.Transactions("tx2"), true, node1)
		ts.AssertTransactionsExist(wallet.Transactions("tx1"), false, node1)
		ts.AssertTransactionsInCacheBooked(wallet.Transactions("tx2"), false, node1)
		// make sure that the block is not booked
	}

	// issue block2 that makes block1 solid (but not booked yet)
	{
		ts.IssueBasicBlockWithOptions("block2", wallet, tx1)

		ts.AssertTransactionsExist(wallet.Transactions("tx1", "tx2"), true, node1)
		ts.AssertTransactionsInCacheBooked(wallet.Transactions("tx1", "tx2"), true, node1)
		ts.AssertBlocksInCacheBooked(ts.Blocks("block2"), true, node1)
		ts.AssertBlocksInCacheBooked(ts.Blocks("block1"), false, node1)
		ts.AssertBlocksInCacheConflicts(map[*blocks.Block][]string{
			ts.Block("block2"): {"tx1"},
		}, node1)
		ts.AssertTransactionInCacheConflicts(map[*iotago.Transaction][]string{
			wallet.Transaction("tx2"): {"tx2"},
			wallet.Transaction("tx1"): {"tx1"},
		}, node1)
	}

	// confirm 2nd block so block1 gets booked
	{
		ts.IssueValidationBlockWithHeaderOptions("block3", node1, mock.WithStrongParents(ts.BlockID("block2")))
		ts.IssueValidationBlockWithHeaderOptions("block4", node1, mock.WithStrongParents(ts.BlockID("block3")))

		ts.AssertBlocksInCacheBooked(ts.Blocks("block1", "block2", "block3"), true, node1)
		ts.AssertBlocksInCacheConflicts(map[*blocks.Block][]string{
			ts.Block("block1"): {"tx2"},
			ts.Block("block2"): {"tx1"},
			ts.Block("block3"): {"tx1"},
		}, node1)
	}
}

func Test_WeightPropagation(t *testing.T) {
	ts := testsuite.NewTestSuite(t)
	defer ts.Shutdown()

	node1 := ts.AddValidatorNode("node1")
	node2 := ts.AddValidatorNode("node2")

	wallet := ts.AddDefaultWallet(node1)

	ts.Run(true, map[string][]options.Option[protocol.Protocol]{})

	ts.AssertSybilProtectionCommittee(0, []iotago.AccountID{
		node1.Validator.AccountData.ID,
		node2.Validator.AccountData.ID,
	}, ts.Nodes()...)

	// Create and issue double spends
	{
		tx1 := wallet.CreateBasicOutputsEquallyFromInput("tx1", 1, "Genesis:0")
		tx2 := wallet.CreateBasicOutputsEquallyFromInput("tx2", 1, "Genesis:0")

		ts.IssueBasicBlockWithOptions("block1", wallet, tx1, mock.WithStrongParents(ts.BlockID("Genesis")))
		ts.IssueBasicBlockWithOptions("block2", wallet, tx2, mock.WithStrongParents(ts.BlockID("Genesis")))

		ts.AssertTransactionsExist(wallet.Transactions("tx1", "tx2"), true, node1, node2)
		ts.AssertTransactionsInCacheBooked(wallet.Transactions("tx1", "tx2"), true, node1, node2)
		ts.AssertTransactionsInCachePending(wallet.Transactions("tx1", "tx2"), true, node1, node2)
		ts.AssertBlocksInCacheConflicts(map[*blocks.Block][]string{
			ts.Block("block1"): {"tx1"},
			ts.Block("block2"): {"tx2"},
		}, node1, node2)

		ts.AssertTransactionInCacheConflicts(map[*iotago.Transaction][]string{
			wallet.Transaction("tx2"): {"tx2"},
			wallet.Transaction("tx1"): {"tx1"},
		}, node1, node2)
	}

	// Issue some more blocks and assert that conflicts are propagated to blocks.
	{

		ts.IssueBasicBlockWithOptions("block3-basic", ts.Wallet("node1"), &iotago.TaggedData{}, mock.WithStrongParents(ts.BlockID("block1")))
		ts.IssueBasicBlockWithOptions("block4-basic", ts.Wallet("node2"), &iotago.TaggedData{}, mock.WithStrongParents(ts.BlockID("block2")))

		ts.AssertBlocksInCacheConflicts(map[*blocks.Block][]string{
			ts.Block("block3-basic"): {"tx1"},
			ts.Block("block4-basic"): {"tx2"},
		}, node1, node2)
		ts.AssertSpendersInCacheAcceptanceState([]string{"tx1", "tx2"}, acceptance.Pending, ts.Nodes()...)
		ts.AssertTransactionsInCachePending(wallet.Transactions("tx1", "tx2"), true, node1, node2)
	}

	// Issue valid blocks that should resolve the conflict, but basic blocks don't carry any weight..
	{
		ts.IssueBasicBlockWithOptions("block5-basic", ts.Wallet("node1"), &iotago.TaggedData{}, mock.WithStrongParents(ts.BlockIDs("block4-basic")...), mock.WithShallowLikeParents(ts.BlockID("block2")))
		ts.IssueBasicBlockWithOptions("block6-basic", ts.Wallet("node2"), &iotago.TaggedData{}, mock.WithStrongParents(ts.BlockIDs("block5-basic")...))

		ts.AssertBlocksInCacheConflicts(map[*blocks.Block][]string{
			ts.Block("block6-basic"): {"tx2"},
		}, ts.Nodes()...)

		// Make sure that neither approval (conflict weight),
		// nor witness (block weight) was not propagated using basic blocks and caused acceptance.
		ts.AssertSpendersInCacheAcceptanceState([]string{"tx1", "tx2"}, acceptance.Pending, ts.Nodes()...)
		ts.AssertTransactionsInCacheAccepted(wallet.Transactions("tx2"), false, node1, node2)
		ts.AssertTransactionsInCacheRejected(wallet.Transactions("tx1"), false, node1, node2)
		ts.AssertTransactionsInCachePending(wallet.Transactions("tx1", "tx2"), true, node1, node2)
		ts.AssertBlocksInCacheAccepted(ts.Blocks("block3-basic", "block4-basic", "block5-basic", "block6-basic"), false, ts.Nodes()...)
		ts.AssertBlocksInCachePreAccepted(ts.Blocks("block3-basic", "block4-basic", "block5-basic", "block6-basic"), false, ts.Nodes()...)
	}

	// Issue validation blocks that are subjectively invalid, but accept the basic blocks.
	// Make sure that the pre-accepted basic blocks do not apply approval weight - the conflicts should remain unresolved.
	// If basic blocks carry approval or witness weight, then the test will fail.
	{
		ts.IssueValidationBlockWithHeaderOptions("block8", node1, mock.WithStrongParents(ts.BlockIDs("block3-basic", "block6-basic")...))
		ts.IssueValidationBlockWithHeaderOptions("block9", node2, mock.WithStrongParents(ts.BlockID("block8")))
		ts.IssueValidationBlockWithHeaderOptions("block10", node1, mock.WithStrongParents(ts.BlockID("block9")))

		ts.AssertBlocksInCacheConflicts(map[*blocks.Block][]string{
			ts.Block("block8"):  {"tx1", "tx2"},
			ts.Block("block9"):  {"tx1", "tx2"},
			ts.Block("block10"): {"tx1", "tx2"},
		}, node1, node2)

		ts.AssertBlocksInCachePreAccepted(ts.Blocks("block3-basic", "block4-basic", "block5-basic", "block6-basic"), true, node1, node2)
		ts.AssertBlocksInCacheAccepted(ts.Blocks("block3-basic", "block4-basic", "block5-basic", "block6-basic"), true, node1, node2)
		ts.AssertTransactionsInCachePending(wallet.Transactions("tx1", "tx2"), true, node1, node2)
	}
}

func Test_DoubleSpend(t *testing.T) {
	ts := testsuite.NewTestSuite(t)
	defer ts.Shutdown()

	node1 := ts.AddValidatorNode("node1")
	node2 := ts.AddValidatorNode("node2")
	wallet := ts.AddDefaultWallet(node1)

	ts.Run(true, map[string][]options.Option[protocol.Protocol]{})

	ts.AssertSybilProtectionCommittee(0, []iotago.AccountID{
		node1.Validator.AccountData.ID,
		node2.Validator.AccountData.ID,
	}, ts.Nodes()...)

	// Create and issue double spends
	{
		tx1 := wallet.CreateBasicOutputsEquallyFromInput("tx1", 1, "Genesis:0")
		tx2 := wallet.CreateBasicOutputsEquallyFromInput("tx2", 1, "Genesis:0")

		ts.IssueBasicBlockWithOptions("block1", wallet, tx1, mock.WithStrongParents(ts.BlockID("Genesis")))
		ts.IssueBasicBlockWithOptions("block2", wallet, tx2, mock.WithStrongParents(ts.BlockID("Genesis")))

		ts.AssertTransactionsExist(wallet.Transactions("tx1", "tx2"), true, node1, node2)
		ts.AssertTransactionsInCacheBooked(wallet.Transactions("tx1", "tx2"), true, node1, node2)
		ts.AssertTransactionsInCachePending(wallet.Transactions("tx1", "tx2"), true, node1, node2)
		ts.AssertBlocksInCacheConflicts(map[*blocks.Block][]string{
			ts.Block("block1"): {"tx1"},
			ts.Block("block2"): {"tx2"},
		}, node1, node2)

		ts.AssertTransactionInCacheConflicts(map[*iotago.Transaction][]string{
			wallet.Transaction("tx2"): {"tx2"},
			wallet.Transaction("tx1"): {"tx1"},
		}, node1, node2)
	}

	// Issue some more blocks and assert that conflicts are propagated to blocks.
	{
		ts.IssueValidationBlockWithHeaderOptions("block3", node1, mock.WithStrongParents(ts.BlockID("block1")))
		ts.IssueValidationBlockWithHeaderOptions("block4", node1, mock.WithStrongParents(ts.BlockID("block2")))

		ts.AssertBlocksInCacheConflicts(map[*blocks.Block][]string{
			ts.Block("block3"): {"tx1"},
			ts.Block("block4"): {"tx2"},
		}, node1, node2)
		ts.AssertTransactionsInCachePending(wallet.Transactions("tx1", "tx2"), true, node1, node2)
	}

	// Issue an invalid block and assert that its vote is not cast.
	{
		ts.IssueValidationBlockWithHeaderOptions("block5", node2, mock.WithStrongParents(ts.BlockIDs("block3", "block4")...))

		ts.AssertTransactionsInCachePending(wallet.Transactions("tx1", "tx2"), true, node1, node2)
	}

	// Issue valid blocks that resolve the conflict.
	{
		ts.IssueValidationBlockWithHeaderOptions("block6", node2, mock.WithStrongParents(ts.BlockIDs("block3", "block4")...), mock.WithShallowLikeParents(ts.BlockID("block2")))
		ts.IssueValidationBlockWithHeaderOptions("block7", node1, mock.WithStrongParents(ts.BlockIDs("block6")...))
		ts.IssueValidationBlockWithHeaderOptions("block8", node2, mock.WithStrongParents(ts.BlockIDs("block7")...))
		ts.IssueValidationBlockWithHeaderOptions("block9", node1, mock.WithStrongParents(ts.BlockIDs("block8")...))

		ts.AssertBlocksInCacheConflicts(map[*blocks.Block][]string{
			ts.Block("block6"): {"tx2"},
		}, node1, node2)
		ts.AssertTransactionsInCacheAccepted(wallet.Transactions("tx2"), true, node1, node2)
		ts.AssertTransactionsInCacheRejected(wallet.Transactions("tx1"), true, node1, node2)

	}
}

func Test_SpendPendingCommittedRace(t *testing.T) {
	ts := testsuite.NewTestSuite(t,
		testsuite.WithProtocolParametersOptions(
			iotago.WithTimeProviderOptions(
				0,
				testsuite.GenesisTimeWithOffsetBySlots(20, testsuite.DefaultSlotDurationInSeconds),
				testsuite.DefaultSlotDurationInSeconds,
				testsuite.DefaultSlotsPerEpochExponent,
			),
			iotago.WithLivenessOptions(
				15,
				15,
				2,
				5,
				testsuite.DefaultEpochNearingThreshold,
			),
		),
	)
	defer ts.Shutdown()

	node1 := ts.AddValidatorNode("node1")
	node2 := ts.AddValidatorNode("node2")
	wallet := ts.AddDefaultWallet(node1)

	ts.Run(true, map[string][]options.Option[protocol.Protocol]{})

	ts.AssertSybilProtectionCommittee(0, []iotago.AccountID{
		node1.Validator.AccountData.ID,
		node2.Validator.AccountData.ID,
	}, ts.Nodes()...)

	genesisCommitment := lo.PanicOnErr(node1.Protocol.Engines.Main.Get().Storage.Commitments().Load(0)).Commitment()

	// Create and issue double spends
	{
		tx1 := wallet.CreateBasicOutputsEquallyFromInput("tx1", 1, "Genesis:0")
		tx2 := wallet.CreateBasicOutputsEquallyFromInput("tx2", 1, "Genesis:0")

		wallet.SetDefaultClient(node2.Client)
		ts.SetCurrentSlot(1)
		ts.IssueBasicBlockWithOptions("block1.1", wallet, tx1, mock.WithStrongParents(ts.BlockID("Genesis")))
		ts.IssueBasicBlockWithOptions("block1.2", wallet, tx2, mock.WithStrongParents(ts.BlockID("Genesis")))

		ts.AssertTransactionsExist(wallet.Transactions("tx1", "tx2"), true, node1, node2)
		ts.AssertTransactionsInCacheBooked(wallet.Transactions("tx1", "tx2"), true, node1, node2)
		ts.AssertTransactionsInCachePending(wallet.Transactions("tx1", "tx2"), true, node1, node2)
		ts.AssertBlocksInCacheConflicts(map[*blocks.Block][]string{
			ts.Block("block1.1"): {"tx1"},
			ts.Block("block1.2"): {"tx2"},
		}, node1, node2)

		ts.AssertTransactionInCacheConflicts(map[*iotago.Transaction][]string{
			wallet.Transaction("tx2"): {"tx2"},
			wallet.Transaction("tx1"): {"tx1"},
		}, node1, node2)
	}

	// Issue some more blocks and assert that conflicts are propagated to blocks.
	{
		ts.SetCurrentSlot(2)
		ts.IssueValidationBlockWithHeaderOptions("block2.1", node2, mock.WithSlotCommitment(genesisCommitment), mock.WithStrongParents(ts.BlockID("block1.1")))
		ts.IssueValidationBlockWithHeaderOptions("block2.2", node2, mock.WithSlotCommitment(genesisCommitment), mock.WithStrongParents(ts.BlockID("block1.2")))

		ts.AssertBlocksInCacheConflicts(map[*blocks.Block][]string{
			ts.Block("block2.1"): {"tx1"},
			ts.Block("block2.2"): {"tx2"},
		}, node1, node2)
		ts.AssertTransactionsInCachePending(wallet.Transactions("tx1", "tx2"), true, node1, node2)
	}

	// Advance both nodes at the edge of slot 1 committability
	{
		ts.IssueBlocksAtSlots("", []iotago.SlotIndex{2, 3, 4}, 1, "Genesis", ts.Nodes("node1", "node2"), false, false)

		ts.AssertNodeState(ts.Nodes(),
			testsuite.WithProtocolParameters(ts.API.ProtocolParameters()),
			testsuite.WithLatestCommitmentSlotIndex(0),
			testsuite.WithEqualStoredCommitmentAtIndex(0),
			testsuite.WithEvictedSlot(0),
		)

		ts.SetCurrentSlot(5)
		ts.IssueValidationBlockWithHeaderOptions("", node1, mock.WithSlotCommitment(genesisCommitment), mock.WithStrongParents(ts.BlockIDsWithPrefix("4.0")...))

		ts.IssueBlocksAtSlots("", []iotago.SlotIndex{5}, 1, "4.0", ts.Nodes("node1"), false, false)

		ts.AssertBlocksExist(ts.BlocksWithPrefix("5.0"), true, ts.ClientsForNodes()...)
	}

	partitions := map[string][]*mock.Node{
		"node1": {node1},
		"node2": {node2},
	}

	// Split the nodes into partitions and commit slot 1 only on node2
	{
		ts.SplitIntoPartitions(partitions)

		// Only node2 will commit after issuing this one
		ts.IssueBlocksAtSlots("", []iotago.SlotIndex{5}, 1, "5.0", ts.Nodes("node2"), false, false)

		ts.AssertNodeState(ts.Nodes("node1"),
			testsuite.WithProtocolParameters(ts.API.ProtocolParameters()),
			testsuite.WithLatestCommitmentSlotIndex(0),
			testsuite.WithEqualStoredCommitmentAtIndex(0),
			testsuite.WithEvictedSlot(0),
		)

		ts.AssertNodeState(ts.Nodes("node2"),
			testsuite.WithProtocolParameters(ts.API.ProtocolParameters()),
			testsuite.WithLatestCommitmentSlotIndex(1),
			testsuite.WithEqualStoredCommitmentAtIndex(1),
			testsuite.WithEvictedSlot(1),
		)
	}

	commitment1 := lo.PanicOnErr(node2.Protocol.Engines.Main.Get().Storage.Commitments().Load(1)).Commitment()

	// Issue a block booked on a pending conflict on node2
	{
		ts.IssueValidationBlockWithHeaderOptions("n2-pending-genesis", node2, mock.WithSlotCommitment(genesisCommitment), mock.WithStrongParents(ts.BlockIDs("block2.1")...))
		ts.IssueValidationBlockWithHeaderOptions("n2-pending-commit1", node2, mock.WithSlotCommitment(commitment1), mock.WithStrongParents(ts.BlockIDs("block2.1")...))

		ts.AssertTransactionsExist(wallet.Transactions("tx1"), true, node2)
		ts.AssertTransactionsInCachePending(wallet.Transactions("tx1"), true, node2)

		ts.AssertBlocksInCacheBooked(ts.Blocks("n2-pending-genesis", "n2-pending-commit1"), true, node2)
		ts.AssertBlocksInCacheInvalid(ts.Blocks("n2-pending-genesis", "n2-pending-commit1"), false, node2)

		ts.AssertBlocksInCacheConflicts(map[*blocks.Block][]string{
			ts.Block("block2.1"):           {"tx1"},
			ts.Block("n2-pending-genesis"): {"tx1"},
			ts.Block("n2-pending-commit1"): {}, // no conflits inherited as the block merges orphaned conflicts.
		}, node2)
	}

	ts.MergePartitionsToMain(lo.Keys(partitions)...)

	// Sync up the nodes to he same point and check consistency between them.
	{
		// Let node1 catch up with commitment 1
		ts.IssueBlocksAtSlots("5.1", []iotago.SlotIndex{5}, 1, "5.0", ts.Nodes("node2"), false, false)

		ts.AssertNodeState(ts.Nodes("node1", "node2"),
			testsuite.WithProtocolParameters(ts.API.ProtocolParameters()),
			testsuite.WithLatestCommitmentSlotIndex(1),
			testsuite.WithEqualStoredCommitmentAtIndex(1),
			testsuite.WithEvictedSlot(1),
		)

		// Exchange each-other blocks, ignoring invalidity
		wallet.SetDefaultClient(node1.Client)
		ts.IssueExistingBlock("n2-pending-genesis", wallet)
		ts.IssueExistingBlock("n2-pending-commit1", wallet)

		// The nodes agree on the results of the invalid blocks
		ts.AssertBlocksInCacheBooked(ts.Blocks("n2-pending-genesis", "n2-pending-commit1"), true, node1, node2)
		ts.AssertBlocksInCacheInvalid(ts.Blocks("n2-pending-genesis", "n2-pending-commit1"), false, node1, node2)

		ts.AssertBlocksInCacheConflicts(map[*blocks.Block][]string{
			ts.Block("block2.1"):           {"tx1"},
			ts.Block("n2-pending-genesis"): {"tx1"},
			ts.Block("n2-pending-commit1"): {}, // no conflits inherited as the block merges orphaned conflicts.
		}, node1, node2)

		ts.AssertTransactionsInCachePending(wallet.Transactions("tx1", "tx2"), true, node1, node2)
	}

	// Commit further and test eviction of transactions
	{
		ts.AssertTransactionsExist(wallet.Transactions("tx1", "tx2"), true, node1, node2)
		ts.AssertTransactionsInCachePending(wallet.Transactions("tx1", "tx2"), true, node1, node2)

		ts.IssueBlocksAtSlots("", []iotago.SlotIndex{6, 7, 8, 9, 10}, 5, "5.1", ts.Nodes("node1", "node2"), false, false)

		ts.AssertNodeState(ts.Nodes("node1", "node2"),
			testsuite.WithProtocolParameters(ts.API.ProtocolParameters()),
			testsuite.WithLatestCommitmentSlotIndex(8),
			testsuite.WithEqualStoredCommitmentAtIndex(8),
			testsuite.WithEvictedSlot(8),
		)

		ts.AssertTransactionsExist(wallet.Transactions("tx1", "tx2"), false, node1, node2)
	}
}

func Test_RootBlockShallowLike(t *testing.T) {
	ts := testsuite.NewTestSuite(t,
		testsuite.WithProtocolParametersOptions(
			iotago.WithTimeProviderOptions(
				0,
				testsuite.GenesisTimeWithOffsetBySlots(1000, testsuite.DefaultSlotDurationInSeconds),
				testsuite.DefaultSlotDurationInSeconds,
				3,
			),
			iotago.WithLivenessOptions(
				10,
				10,
				2,
				4,
				5,
			),
		),

		testsuite.WithWaitFor(5*time.Second),
	)
	defer ts.Shutdown()

	node1 := ts.AddValidatorNode("node1")
	wallet := ts.AddDefaultWallet(node1)
	ts.Run(true, map[string][]options.Option[protocol.Protocol]{})

	tx1 := wallet.CreateBasicOutputsEquallyFromInput("tx1", 1, "Genesis:0")

	ts.IssueBasicBlockWithOptions("block1", wallet, tx1, mock.WithIssuingTime(ts.API.TimeProvider().SlotStartTime(1)))
	ts.IssueBasicBlockWithOptions("block2", wallet, &iotago.TaggedData{}, mock.WithIssuingTime(ts.API.TimeProvider().SlotStartTime(1)))

	ts.AssertTransactionsExist(wallet.Transactions("tx1"), true, node1)

	ts.AssertBlocksInCacheConflicts(map[*blocks.Block][]string{
		ts.Block("block1"): {"tx1"},
	}, node1)

	ts.AssertTransactionInCacheConflicts(map[*iotago.Transaction][]string{
		wallet.Transaction("tx1"): {"tx1"},
	}, node1)

	ts.IssueBlocksAtSlots("", []iotago.SlotIndex{2, 3, 4}, 2, "block", ts.Nodes(), true, false)

	ts.AssertActiveRootBlocks(append(ts.Blocks("Genesis", "block1", "block2"), ts.BlocksWithPrefix("2.")...), ts.Nodes()...)

	ts.IssueBasicBlockWithOptions("block-shallow-like-valid", wallet, &iotago.TaggedData{}, mock.WithStrongParents(ts.BlockID("4.1-node1")), mock.WithShallowLikeParents(ts.BlockID("block1")), mock.WithIssuingTime(ts.API.TimeProvider().SlotStartTime(5)))
	ts.AssertBlocksInCacheBooked(ts.Blocks("block-shallow-like-valid"), true, node1)
	ts.AssertBlocksInCacheInvalid(ts.Blocks("block-shallow-like-valid"), false, node1)

	ts.IssueBasicBlockWithOptions("block-shallow-like-invalid", wallet, &iotago.TaggedData{}, mock.WithStrongParents(ts.BlockID("4.1-node1")), mock.WithShallowLikeParents(ts.BlockID("block2")), mock.WithIssuingTime(ts.API.TimeProvider().SlotStartTime(5)))
	ts.AssertBlocksInCacheBooked(ts.Blocks("block-shallow-like-invalid"), false, node1)
	ts.AssertBlocksInCacheInvalid(ts.Blocks("block-shallow-like-invalid"), true, node1)
}

func Test_BlockWithInvalidTransactionGetsBooked(t *testing.T) {
	ts := testsuite.NewTestSuite(t,
		testsuite.WithProtocolParametersOptions(
			iotago.WithTimeProviderOptions(
				0,
				testsuite.GenesisTimeWithOffsetBySlots(1000, testsuite.DefaultSlotDurationInSeconds),
				testsuite.DefaultSlotDurationInSeconds,
				13,
			),
		),
	)

	node1 := ts.AddValidatorNode("node1")
	ts.AddNode("node2")
	ts.AddDefaultWallet(node1)

	ts.Run(true)
	defer ts.Shutdown()

	// CREATE NFT FROM BASIC UTXO
	var block1Slot iotago.SlotIndex = ts.API.ProtocolParameters().GenesisSlot() + 1
	ts.SetCurrentSlot(block1Slot)

	tx1 := ts.DefaultWallet().CreateNFTFromInput("TX1", ts.DefaultWallet().OutputData("Genesis:0"),
		func(nftBuilder *builder.NFTOutputBuilder) {
			// Set an issuer ID that is not unlocked in the TX which will cause the TX to be invalid.
			nftBuilder.ImmutableIssuer(&iotago.Ed25519Address{})
		},
	)
	block1 := lo.PanicOnErr(ts.IssueBasicBlockWithOptions("block1", ts.DefaultWallet(), tx1))

	vblock1 := lo.PanicOnErr(ts.IssueValidationBlockWithHeaderOptions("vblock1", node1, mock.WithWeakParents(block1.ID()), mock.WithStrongParents(ts.Block("Genesis").ID())))
	vblock2 := lo.PanicOnErr(ts.IssueValidationBlockWithHeaderOptions("vblock2", node1, mock.WithStrongParents(vblock1.ID())))
	vblock3 := lo.PanicOnErr(ts.IssueValidationBlockWithHeaderOptions("vblock3", node1, mock.WithStrongParents(vblock2.ID())))

	ts.AssertBlocksInCacheAccepted(ts.Blocks("block1"), true, ts.Nodes()...)
	ts.AssertBlocksInCacheConfirmed(ts.Blocks("block1"), true, ts.Nodes()...)

	ts.AssertTransactionsExist([]*iotago.Transaction{tx1.Transaction}, true, ts.Nodes()...)
	ts.AssertTransactionFailure(tx1.MustID(), iotago.ErrIssuerFeatureNotUnlocked, ts.Nodes()...)
	ts.AssertTransactionsInCacheAccepted([]*iotago.Transaction{tx1.Transaction}, false, ts.Nodes()...)

	ts.CommitUntilSlot(block1Slot, vblock3.ID())

	ts.AssertStorageCommitmentBlockAccepted(block1Slot, block1.ID(), true, ts.Nodes()...)
	ts.AssertStorageCommitmentTransactionAccepted(block1Slot, tx1.Transaction.MustID(), false, ts.Nodes()...)
}
