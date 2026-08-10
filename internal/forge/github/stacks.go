package github

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"go.abhg.dev/container/ring"
	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/github"
	"go.abhg.dev/gs/internal/graph"
)

var _ forge.WithStacks = (*Repository)(nil)

// UpdateStack reconciles the supplied change relationships with GitHub's
// native stack representation.
//
// GitHub accepts only linear stacks of open pull requests whose head branches
// belong to the receiving repository. UpdateStack projects each unrestricted
// change tree onto that representation, preserves compatible existing native
// stack membership, and selects one longest remaining path. When divergence
// leaves a branch and its upstack out of the native stack, UpdateStack warns
// once for the omitted branch tree instead of returning an error.
//
// If the initial batched inspection fails, UpdateStack returns without
// attempting an update. After inspection succeeds, an individually missing
// pull request or a write failure does not prevent independent eligible trees
// from being attempted; those errors are joined and returned. Divergence only
// produces warnings.
func (r *Repository) UpdateStack(
	ctx context.Context,
	changes []forge.StackChange,
) error {
	update, err := newGitHubStackUpdate(r, changes)
	if err != nil {
		return err
	}
	if err := update.loadPullRequests(ctx); err != nil {
		return err
	}

	errs := update.projectChangeTrees()
	errs = append(errs, update.reconcileChangeTrees(ctx)...)
	return errors.Join(errs...)
}

// githubStackUpdate owns one conversion from the forge's unrestricted change
// forest to GitHub's open, same-repository, linear native stacks.
//
// The conversion has three phases whose results remain on this value:
//
//  1. orderedChanges preserves the requested downstack-to-upstack order and
//     receives GitHub's pull request state in one batch.
//  2. projectChangeTrees removes representation-ineligible changes. Closed or
//     merged changes are transparent; missing or cross-repository changes cut
//     off their complete requested upstack.
//  3. reconcileChangeTrees preserves compatible native stack membership, then
//     selects one linear path from each projected tree and warns for omitted
//     divergent branch trees.
//
// Keeping these representations together makes the selection policy depend on
// one explicit snapshot instead of reconstructing request, projection, and
// remote membership state across helpers.
type githubStackUpdate struct {
	repository *Repository

	changesByNumber map[int]*stackUpdateChange
	orderedChanges  []*stackUpdateChange

	// aboves and bottoms describe the projected forest after closed and merged
	// pull requests have been removed.
	aboves  map[*stackUpdateChange][]*stackUpdateChange
	bottoms []*stackUpdateChange
}

// stackUpdateChange carries one requested relationship through GitHub lookup,
// projection, and linear-path selection.
type stackUpdateChange struct {
	// number is the repository-local pull request number.
	number int

	// requestedBase is the immediate base supplied in the forge request.
	requestedBase *stackUpdateChange

	// pullRequest is nil when GitHub did not find the requested change.
	pullRequest *github.StackUpdatePullRequest

	// projection records how this change participates in GitHub's
	// representation.
	projection stackProjection

	// nearestIncluded is this change when it is included. Transparent changes
	// inherit their requested base's value so their upstack reconnects to the
	// nearest included change below them.
	nearestIncluded *stackUpdateChange

	// projectedBase is the nearest included change below this change. It is set
	// only for included changes.
	projectedBase *stackUpdateChange
}

// stackProjection records how a requested change participates in GitHub's
// open, same-repository stack representation. The zero value means projection
// has not inspected the change yet.
type stackProjection uint8

const (
	// stackProjectionExcluded means GitHub cannot represent the change or its
	// requested upstack.
	stackProjectionExcluded stackProjection = iota + 1

	// stackProjectionTransparent means the pull request is closed or merged.
	// GitHub omits it and joins its upstack to the nearest included change below.
	stackProjectionTransparent

	// stackProjectionIncluded means the pull request is open and eligible for a
	// GitHub native stack.
	stackProjectionIncluded
)

func newGitHubStackUpdate(
	repository *Repository,
	changes []forge.StackChange,
) (*githubStackUpdate, error) {
	changesByNumber := make(map[int]*stackUpdateChange, len(changes))
	for _, change := range changes {
		number := mustPR(change.Change).Number
		changesByNumber[number] = &stackUpdateChange{number: number}
	}
	for _, change := range changes {
		number := mustPR(change.Change).Number
		if change.Base != nil {
			changesByNumber[number].requestedBase = changesByNumber[mustPR(change.Base).Number]
		}
	}

	orderedNumbers, err := graph.Toposort(
		slices.Sorted(maps.Keys(changesByNumber)),
		func(number int) (int, bool) {
			base := changesByNumber[number].requestedBase
			if base == nil {
				return 0, false
			}
			return base.number, true
		},
	)
	if err != nil {
		return nil, fmt.Errorf("order GitHub stack changes: %w", err)
	}

	orderedChanges := make([]*stackUpdateChange, len(orderedNumbers))
	for i, number := range orderedNumbers {
		orderedChanges[i] = changesByNumber[number]
	}
	return &githubStackUpdate{
		repository:      repository,
		changesByNumber: changesByNumber,
		orderedChanges:  orderedChanges,
	}, nil
}

// loadPullRequests attaches GitHub's compact stack-update projection to each
// requested change. The gateway preserves input order, so orderedChanges is the
// authoritative join between the request graph and the remote snapshot.
func (u *githubStackUpdate) loadPullRequests(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	numbers := make([]int, len(u.orderedChanges))
	for i, change := range u.orderedChanges {
		numbers[i] = change.number
	}
	pullRequests, err := u.repository.gateway.PullRequestsForStackUpdate(
		ctx,
		u.repository.owner,
		u.repository.repo,
		numbers,
	)
	if err != nil {
		return fmt.Errorf("inspect GitHub pull requests for stack update: %w", err)
	}
	for i, pullRequest := range pullRequests {
		u.orderedChanges[i].pullRequest = pullRequest
	}
	return nil
}

// projectChangeTrees converts requested forge relationships into the forest
// from which GitHub native stacks may be selected.
//
// A closed or merged pull request is transparent because its upstack remains
// valid and can reconnect to the nearest included change below it. A missing
// or cross-repository pull request instead excludes its complete requested
// upstack: GitHub cannot form a same-repository native stack through that
// boundary. Missing pull requests are genuine lookup failures as well as
// projection boundaries, so they are returned after projection completes.
func (u *githubStackUpdate) projectChangeTrees() []error {
	var errs []error
	// Project changes in base-first order. That makes each requested base's
	// exclusion and nearest included change final before its upstack is visited.
	for _, change := range u.orderedChanges {
		if change.requestedBase != nil &&
			change.requestedBase.projection == stackProjectionExcluded {
			change.projection = stackProjectionExcluded
			continue
		}

		// Closed and merged pull requests disappear from GitHub's native stack,
		// so carry the nearest included change through them. Eligible open pull
		// requests use that change as their projected base.
		var below *stackUpdateChange
		if change.requestedBase != nil {
			below = change.requestedBase.nearestIncluded
		}
		pullRequest := change.pullRequest
		if pullRequest == nil {
			change.projection = stackProjectionExcluded
			errs = append(errs, fmt.Errorf(
				"inspect GitHub pull request #%d: %w",
				change.number,
				forge.ErrNotFound,
			))
			u.warnOmittedUpstack(change.number, "the pull request was not found")
			continue
		}
		if pullRequest.State != github.PullRequestStateOpen {
			change.projection = stackProjectionTransparent
			change.nearestIncluded = below
			continue
		}
		headIsInRepository := strings.EqualFold(pullRequest.HeadRepositoryOwner, u.repository.owner) &&
			strings.EqualFold(pullRequest.HeadRepositoryName, u.repository.repo)
		if !headIsInRepository {
			change.projection = stackProjectionExcluded
			u.warnOmittedUpstack(
				change.number,
				"the head branch is not in the receiving repository",
			)
			continue
		}

		change.projection = stackProjectionIncluded
		change.nearestIncluded = change
		change.projectedBase = below
	}

	// Materialize the projected forest only after every change has been
	// classified. Excluded changes never become traversal roots or edges.
	u.aboves = make(map[*stackUpdateChange][]*stackUpdateChange, len(u.changesByNumber))
	for _, change := range u.orderedChanges {
		if change.projection != stackProjectionIncluded {
			continue
		}
		if change.projectedBase == nil {
			u.bottoms = append(u.bottoms, change)
			continue
		}
		u.aboves[change.projectedBase] = append(u.aboves[change.projectedBase], change)
	}

	// PR-number ordering makes independent-tree processing, equal-length path
	// selection, and divergent-upstack warnings deterministic.
	slices.SortFunc(u.bottoms, func(a, b *stackUpdateChange) int {
		return cmp.Compare(a.number, b.number)
	})
	for _, changes := range u.aboves {
		slices.SortFunc(changes, func(a, b *stackUpdateChange) int {
			return cmp.Compare(a.number, b.number)
		})
	}
	return errs
}

// reconcileChangeTrees independently selects and writes each projected tree.
// Selection limitations only warn and skip their tree; genuine write failures
// are collected so later trees still receive their best-effort update.
func (u *githubStackUpdate) reconcileChangeTrees(ctx context.Context) []error {
	var errs []error
	// Each bottom owns one complete projected change tree.
	// Selection inspects every reachable change
	// so existing native membership can constrain the path
	// before longest-path selection.
	// The selected path is the complete desired linear stack,
	// including any existing prefix.
	// applyLinearPath translates that state into a create, no-op,
	// or append of only the missing suffix.
	for _, bottom := range u.bottoms {
		selected, remoteStack, ok := u.selectLinearPath(bottom)
		if !ok {
			continue
		}

		// For each selected branch in the stack, if it has siblings,
		// those siblings are omitted from the native stack.
		for idx, change := range selected {
			var selectedAbove *stackUpdateChange
			if idx+1 < len(selected) {
				selectedAbove = selected[idx+1]
			}
			for _, above := range u.aboves[change] {
				if above != selectedAbove {
					u.warnOmittedUpstack(
						above.number,
						"the change tree diverges from the selected linear path",
					)
				}
			}
		}

		if len(selected) < 2 {
			continue
		}
		if err := u.applyLinearPath(ctx, selected, remoteStack); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// selectLinearPath applies GitHub's non-divergence constraint before choosing
// among the projected tree's remaining paths.
//
// If any member already belongs to a native stack, that stack's complete open
// membership must be a compatible prefix of the selected path. GitHub cannot
// replace that membership with a sibling path without unstacking pull
// requests, so incompatible or multiple native stacks leave the tree unchanged
// and produce one tree-level warning. Only the upstack above a compatible
// prefix participates in longest-path selection; equal lengths prefer the
// lower pull request number.
//
// On success, selectLinearPath returns the selected base-up path, the remote
// stack to extend (or nil when GitHub must create one), and true. When the
// existing membership cannot be preserved, it warns, returns false, and the
// caller leaves the complete projected tree unchanged.
func (u *githubStackUpdate) selectLinearPath(
	bottom *stackUpdateChange,
) ([]*stackUpdateChange, *github.PullRequestStack, bool) {
	// Discover whether this projected tree intersects one existing native stack.
	// More than one remote stack cannot be represented by one GitHub update, and
	// choosing either would implicitly abandon the other.
	var remoteStack *github.PullRequestStack
	var remaining ring.Q[*stackUpdateChange]
	remaining.Push(bottom)
	for !remaining.Empty() {
		change := remaining.Pop()
		for _, above := range u.aboves[change] {
			remaining.Push(above)
		}

		stack := change.pullRequest.Stack
		if stack == nil {
			continue
		}
		if remoteStack == nil {
			remoteStack = stack
			continue
		}
		if remoteStack.Number != stack.Number {
			u.repository.log.Warnf(
				"#%d: Leaving pull request and its upstack in existing GitHub native stacks: pull requests belong to different native stacks",
				bottom.number,
			)
			return nil, nil, false
		}
	}

	extensionBase := bottom
	if remoteStack != nil {
		// Prove that the remote stack's open members are exactly one base-up path
		// through this projected tree. The last member becomes the fixed prefix
		// above which a new path may be selected.
		var below *stackUpdateChange
		compatible := len(remoteStack.OpenPullRequests) > 0
		for i, number := range remoteStack.OpenPullRequests {
			change := u.changesByNumber[number]
			if change == nil || change.projection != stackProjectionIncluded ||
				(i == 0 && change != bottom) ||
				(i > 0 && change.projectedBase != below) {
				compatible = false
				break
			}
			below = change
		}
		if !compatible {
			openPullRequests := make([]string, len(remoteStack.OpenPullRequests))
			for i, number := range remoteStack.OpenPullRequests {
				openPullRequests[i] = fmt.Sprintf("#%d", number)
			}
			u.repository.log.Warnf(
				"#%d: Leaving pull request and its upstack in existing GitHub native stack #%d: open pull requests %s have incompatible membership",
				bottom.number,
				remoteStack.Number,
				strings.Join(openPullRequests, ", "),
			)
			return nil, nil, false
		}
		extensionBase = below
	}

	// Select only above the fixed remote prefix. FurthestChildren establishes
	// maximum path length; PR number supplies GitHub's deterministic tie-break.
	tops := graph.FurthestChildren(
		extensionBase,
		func(change *stackUpdateChange) []*stackUpdateChange {
			return u.aboves[change]
		},
	)
	top := slices.MinFunc(tops, func(a, b *stackUpdateChange) int {
		return cmp.Compare(a.number, b.number)
	})
	var selected []*stackUpdateChange
	for change := top; change != nil; change = change.projectedBase {
		selected = append(selected, change)
	}
	slices.Reverse(selected)
	return selected, remoteStack, true
}

// applyLinearPath creates a native stack for an unstacked path or extends the
// compatible remote prefix established by selectLinearPath. An already-current
// path requires no write.
func (u *githubStackUpdate) applyLinearPath(
	ctx context.Context,
	changes []*stackUpdateChange,
	remoteStack *github.PullRequestStack,
) error {
	numbers := make([]int, len(changes))
	for i, change := range changes {
		numbers[i] = change.number
	}

	if remoteStack == nil {
		err := u.repository.gateway.CreatePullRequestStack(ctx, &github.CreatePullRequestStackInput{
			Owner:        u.repository.owner,
			Repo:         u.repository.repo,
			PullRequests: slices.Clone(numbers),
		})
		return githubStackError("create GitHub native stack", err)
	}
	if slices.Equal(remoteStack.OpenPullRequests, numbers) {
		return nil
	}
	// Path selection established that the current open members are a strict
	// prefix of numbers, so GitHub only needs the new upstack changes.
	err := u.repository.gateway.AddPullRequestsToStack(ctx, &github.AddPullRequestsToStackInput{
		Owner:        u.repository.owner,
		Repo:         u.repository.repo,
		StackNumber:  remoteStack.Number,
		PullRequests: slices.Clone(numbers[len(remoteStack.OpenPullRequests):]),
	})
	return githubStackError(fmt.Sprintf("extend GitHub native stack #%d", remoteStack.Number), err)
}

func (u *githubStackUpdate) warnOmittedUpstack(number int, reason string) {
	u.repository.log.Warnf(
		"#%d: Leaving pull request and its upstack out of the GitHub native stack: %s",
		number,
		reason,
	)
}

func githubStackError(action string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, github.ErrNotFound) {
		// GitHub returns 404 both when the native-stack endpoint is unavailable
		// and when a referenced resource disappeared between lookup and update.
		// Preserve both classifications so callers can detect unsupported APIs
		// without losing the underlying resource failure.
		return fmt.Errorf("%s: %w", action, errors.Join(forge.ErrUnsupported, err))
	}
	return fmt.Errorf("%s: %w", action, err)
}
