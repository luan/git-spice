package submit

import (
	"context"
	"errors"

	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/spice"
)

// updateStacks updates the forge-native representation of the tracked stack
// trees affected by a successful submit. Because the changes are already
// published, unavailable capabilities are ignored and other failures are
// reported as warnings rather than returned to the submit workflow.
func (h *Handler) updateStacks(ctx context.Context, submitted []string) {
	repo, err := h.upstreamRepository(ctx)
	if err != nil {
		h.Log.Warn("Could not update stacks", "error", err)
		return
	}

	stackRepo, ok := repo.(forge.WithStacks)
	if !ok {
		return
	}

	// Submission can create change metadata after the command builds its first
	// branch graph.
	// Reload it so newly published changes participate in the update.
	graph, err := h.Service.BranchGraph(ctx, nil)
	if err != nil {
		h.Log.Warn("Could not update stacks", "error", err)
		return
	}

	changes := nativeStackChanges(graph, repo.Forge().ID(), submitted)
	if len(changes) == 0 {
		return
	}

	if err := stackRepo.UpdateStack(ctx, changes); err != nil &&
		!errors.Is(err, forge.ErrUnsupported) {
		h.Log.Warn("Could not update stacks", "error", err)
	}
}

// nativeStackChanges projects every published change in a tree containing a
// submitted branch into the forge's native-stack representation.
//
// A submission may affect any branch in the same tree: adding or updating one
// change can complete a relationship elsewhere in its divergent upstack. The
// projection therefore starts at each submitted branch's bottom and retains
// all published changes for the target forge. If a branch's base is absent
// from that projection, the forge contract treats the branch as a tree root.
func nativeStackChanges(
	graph *spice.BranchGraph,
	forgeID string,
	submitted []string,
) []forge.StackChange {
	affectedBranches := make(map[string]struct{})
	for _, branch := range submitted {
		for member := range graph.Upstack(graph.Bottom(branch)) {
			affectedBranches[member] = struct{}{}
		}
	}

	changeByBranch := make(map[string]forge.ChangeID, len(affectedBranches))
	for branch := range graph.All() {
		if _, ok := affectedBranches[branch.Name]; !ok || branch.Change == nil {
			continue
		}
		if branch.Change.ForgeID() == forgeID {
			changeByBranch[branch.Name] = branch.Change.ChangeID()
		}
	}

	changes := make([]forge.StackChange, 0, len(changeByBranch))
	for branch := range graph.All() {
		change, ok := changeByBranch[branch.Name]
		if !ok {
			continue
		}
		changes = append(changes, forge.StackChange{
			Change: change,
			Base:   changeByBranch[branch.Base],
		})
	}
	return changes
}
