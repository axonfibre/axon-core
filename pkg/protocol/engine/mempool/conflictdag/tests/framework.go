package tests

import (
	"testing"

	"github.com/iotaledger/hive.go/ds/advancedset"
	"github.com/iotaledger/hive.go/lo"
	"github.com/iotaledger/iota-core/pkg/core/acceptance"
	"github.com/iotaledger/iota-core/pkg/core/vote"
	"github.com/iotaledger/iota-core/pkg/protocol/engine/mempool/conflictdag"
)

// Framework is a test framework for the ConflictDAG that allows to easily create and manipulate the DAG and its
// validators using human-readable aliases instead of actual IDs.
type Framework[ConflictID, ResourceID conflictdag.IDType, VotePower conflictdag.VotePowerType[VotePower]] struct {
	// Instance is the ConflictDAG instance that is used in the tests.
	Instance conflictdag.ConflictDAG[ConflictID, ResourceID, VotePower]

	// Accounts is the AccountsTestFramework that is used in the tests.
	Accounts *AccountsTestFramework

	// Assert provides a set of assertions that can be used to verify the state of the ConflictDAG.
	Assert *Assertions[ConflictID, ResourceID, VotePower]

	// ConflictID is a function that is used to translate a string alias into a (deterministic) ConflictID.
	ConflictID func(string) ConflictID

	// ResourceID is a function that is used to translate a string alias into a (deterministic) ResourceID.
	ResourceID func(string) ResourceID

	// test is the *testing.T instance that is used in the tests.
	test *testing.T
}

// NewFramework creates a new instance of the Framework.
func NewFramework[CID, RID conflictdag.IDType, V conflictdag.VotePowerType[V]](
	t *testing.T,
	conflictDAG conflictdag.ConflictDAG[CID, RID, V],
	validators *AccountsTestFramework,
	conflictID func(string) CID,
	resourceID func(string) RID,
) *Framework[CID, RID, V] {
	f := &Framework[CID, RID, V]{
		Instance:   conflictDAG,
		Accounts:   validators,
		ConflictID: conflictID,
		ResourceID: resourceID,
		test:       t,
	}
	f.Assert = &Assertions[CID, RID, V]{f}

	return f
}

// CreateOrUpdateConflict creates a new conflict or adds it to the given ConflictSets.
func (f *Framework[ConflictID, ResourceID, VotePower]) CreateOrUpdateConflict(alias string, resourceAliases []string, initialAcceptanceState ...acceptance.State) error {
	return f.Instance.CreateOrUpdateConflict(f.ConflictID(alias), f.ConflictSetIDs(resourceAliases...), lo.First(initialAcceptanceState))
}

// UpdateConflictParents updates the parents of the conflict with the given alias.
func (f *Framework[ConflictID, ResourceID, VotePower]) UpdateConflictParents(conflictAlias string, addedParentIDs, removedParentIDs []string) error {
	return f.Instance.UpdateConflictParents(f.ConflictID(conflictAlias), f.ConflictIDs(addedParentIDs...), f.ConflictIDs(removedParentIDs...))
}

// LikedInstead returns the set of conflicts that are liked instead of the given conflicts.
func (f *Framework[ConflictID, ResourceID, VotePower]) LikedInstead(conflictAliases ...string) *advancedset.AdvancedSet[ConflictID] {
	var result *advancedset.AdvancedSet[ConflictID]
	_ = f.Instance.ReadConsistent(func(conflictDAG conflictdag.ReadLockedConflictDAG[ConflictID, ResourceID, VotePower]) error {
		result = conflictDAG.LikedInstead(f.ConflictIDs(conflictAliases...))

		return nil
	})

	return result
}

// CastVotes casts the given votes for the given conflicts.
func (f *Framework[ConflictID, ResourceID, VotePower]) CastVotes(nodeAlias string, votePower int, conflictAliases ...string) error {
	return f.Instance.CastVotes(vote.NewVote[VotePower](f.Accounts.ID(nodeAlias), f.votePower(votePower)), f.ConflictIDs(conflictAliases...))
}

// ConflictIDs translates the given aliases into an AdvancedSet of ConflictIDs.
func (f *Framework[ConflictID, ResourceID, VotePower]) ConflictIDs(aliases ...string) *advancedset.AdvancedSet[ConflictID] {
	conflictIDs := advancedset.New[ConflictID]()
	for _, alias := range aliases {
		conflictIDs.Add(f.ConflictID(alias))
	}

	return conflictIDs
}

// ConflictSetIDs translates the given aliases into an AdvancedSet of ResourceIDs.
func (f *Framework[ConflictID, ResourceID, VotePower]) ConflictSetIDs(aliases ...string) *advancedset.AdvancedSet[ResourceID] {
	conflictSetIDs := advancedset.New[ResourceID]()
	for _, alias := range aliases {
		conflictSetIDs.Add(f.ResourceID(alias))
	}

	return conflictSetIDs
}

// votePower returns the nth VotePower.
func (f *Framework[ConflictID, ResourceID, VotePower]) votePower(n int) VotePower {
	var votePower VotePower
	for i := 0; i < n; i++ {
		votePower = votePower.Increase()
	}

	return votePower
}
