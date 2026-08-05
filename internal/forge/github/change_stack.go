package github

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"go.abhg.dev/gs/internal/forge"
	gateway "go.abhg.dev/gs/internal/gateway/github"
)

// EnsureChangeStack creates or extends GitHub's native stack for changes.
func (r *Repository) EnsureChangeStack(ctx context.Context, changes []forge.ChangeID) error {
	if len(changes) == 0 {
		return nil
	}

	pullRequests := make([]int, len(changes))
	for i, change := range changes {
		pullRequests[i] = mustPR(change).Number
	}

	var stack *gateway.Stack
	for _, pullRequest := range pullRequests {
		candidate, err := r.gateway.FindStackForPullRequest(
			ctx, r.owner, r.repo, pullRequest,
		)
		if errors.Is(err, gateway.ErrNotFound) || errors.Is(err, gateway.ErrUnsupportedAPI) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find GitHub stack: %w", err)
		}
		if candidate == nil {
			continue
		}
		if stack != nil && stack.Number != candidate.Number {
			return fmt.Errorf(
				"pull requests belong to multiple GitHub stacks: %d and %d",
				stack.Number, candidate.Number,
			)
		}
		stack = candidate
	}
	if stack == nil {
		if len(pullRequests) < 2 {
			return nil
		}
		return r.createChangeStack(ctx, pullRequests)
	}
	if slices.Equal(stack.PullRequests, pullRequests) {
		return nil
	}
	if len(stack.PullRequests) < len(pullRequests) &&
		slices.Equal(stack.PullRequests, pullRequests[:len(stack.PullRequests)]) {
		_, err := r.gateway.AddToStack(
			ctx,
			r.owner,
			r.repo,
			stack.Number,
			pullRequests[len(stack.PullRequests):],
		)
		if errors.Is(err, gateway.ErrUnsupportedAPI) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("extend GitHub stack: %w", err)
		}
		return nil
	}

	if r.log != nil {
		r.log.Warn(
			"GitHub stack differs from submitted pull requests; leaving it unchanged",
			"stack", stack.Number,
			"remote", stack.PullRequests,
			"requested", pullRequests,
		)
	}
	return nil
}

func (r *Repository) createChangeStack(ctx context.Context, pullRequests []int) error {
	_, err := r.gateway.CreateStack(ctx, r.owner, r.repo, pullRequests)
	if errors.Is(err, gateway.ErrNotFound) || errors.Is(err, gateway.ErrUnsupportedAPI) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create GitHub stack: %w", err)
	}
	return nil
}

// ValidateChangeStack verifies native GitHub Stack membership and order without
// mutating the remote.
func (r *Repository) ValidateChangeStack(ctx context.Context, changes []forge.ChangeID) error {
	pullRequests := changePullRequests(changes)
	stack, supported, err := r.findCommonChangeStack(ctx, pullRequests)
	if err != nil || !supported || stack == nil {
		return err
	}
	return r.validateChangeStackOrder(ctx, stack, pullRequests)
}

// ValidateChangeStackSelection verifies that selected pull requests belong to
// one native GitHub Stack in the supplied order.
func (r *Repository) ValidateChangeStackSelection(
	ctx context.Context,
	changes []forge.ChangeID,
) error {
	pullRequests := changePullRequests(changes)
	stack, supported, err := r.findCommonChangeStack(ctx, pullRequests)
	if err != nil || !supported || stack == nil {
		return err
	}
	for start := 0; start+len(pullRequests) <= len(stack.PullRequests); start++ {
		if slices.Equal(
			stack.PullRequests[start:start+len(pullRequests)],
			pullRequests,
		) {
			return nil
		}
	}
	return fmt.Errorf(
		"GitHub stack %d order is %v, selected order is %v",
		stack.Number,
		stack.PullRequests,
		pullRequests,
	)
}

// MergeChangeStack requests one server-managed merge for a native GitHub
// Stack. GitHub merges the requested stack bottom-to-top and updates the
// remaining pull request bases and heads while it runs.
func (r *Repository) MergeChangeStack(
	ctx context.Context,
	changes []forge.ChangeID,
	opts forge.MergeChangeOptions,
) (bool, error) {
	stack, pullRequests, ok, err := r.findMergeChangeStack(ctx, changes)
	if err != nil || !ok {
		return false, err
	}

	top := pullRequests[len(pullRequests)-1]
	if err := r.mergePullRequestAsync(ctx, top, opts); err != nil {
		return false, fmt.Errorf("merge GitHub stack %d: %w", stack.Number, err)
	}
	r.log.Debug("Merged GitHub stack asynchronously", "stack", stack.Number)
	return true, nil
}

// CanMergeChangeStack reports whether changes exactly identify a native GitHub
// Stack, allowing an already-merged remote prefix.
func (r *Repository) CanMergeChangeStack(
	ctx context.Context,
	changes []forge.ChangeID,
) (bool, error) {
	_, _, ok, err := r.findMergeChangeStack(ctx, changes)
	return ok, err
}

func (r *Repository) findMergeChangeStack(
	ctx context.Context,
	changes []forge.ChangeID,
) (*gateway.Stack, []int, bool, error) {
	if len(changes) < 2 {
		return nil, nil, false, nil
	}
	if err := r.validateRemoteChangeIDs(ctx, changes); err != nil {
		return nil, nil, false, err
	}

	pullRequests := changePullRequests(changes)
	stack, supported, err := r.findCommonChangeStack(ctx, pullRequests)
	if err != nil || !supported || stack == nil {
		return nil, nil, false, err
	}
	if err := r.validateChangeStackOrder(ctx, stack, pullRequests); err != nil {
		return nil, nil, false, err
	}
	return stack, pullRequests, true, nil
}

func changePullRequests(changes []forge.ChangeID) []int {
	pullRequests := make([]int, len(changes))
	for i, change := range changes {
		pullRequests[i] = mustPR(change).Number
	}
	return pullRequests
}

func (r *Repository) validateRemoteChangeIDs(
	ctx context.Context,
	changes []forge.ChangeID,
) error {
	if err := r.ValidateChangeIDs(ctx, changes); err != nil {
		return err
	}
	for _, change := range changes {
		pr := mustPR(change)
		actual, err := r.gateway.PullRequestID(
			ctx,
			r.owner,
			r.repo,
			pr.Number,
		)
		if err != nil {
			return fmt.Errorf("verify pull request #%d identity: %w", pr.Number, err)
		}
		if actual != pr.GQLID {
			return fmt.Errorf(
				"pull request #%d GitHub node ID is %q, persisted ID is %q",
				pr.Number,
				actual,
				pr.GQLID,
			)
		}
	}
	return nil
}

func (r *Repository) findCommonChangeStack(
	ctx context.Context,
	pullRequests []int,
) (*gateway.Stack, bool, error) {
	var stack *gateway.Stack
	missing := false
	for _, pullRequest := range pullRequests {
		candidate, err := r.gateway.FindStackForPullRequest(
			ctx,
			r.owner,
			r.repo,
			pullRequest,
		)
		if errors.Is(err, gateway.ErrUnsupportedAPI) {
			return nil, false, nil
		}
		if errors.Is(err, gateway.ErrNotFound) {
			missing = true
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("find GitHub stack: %w", err)
		}
		if candidate == nil {
			missing = true
			continue
		}
		if stack != nil && stack.Number != candidate.Number {
			return nil, false, fmt.Errorf(
				"pull requests belong to multiple GitHub stacks: %d and %d",
				stack.Number,
				candidate.Number,
			)
		}
		stack = candidate
	}
	if stack == nil {
		return nil, true, nil
	}
	if missing {
		return nil, false, fmt.Errorf(
			"not all pull requests belong to GitHub stack %d",
			stack.Number,
		)
	}
	return stack, true, nil
}

func (r *Repository) validateChangeStackOrder(
	ctx context.Context,
	stack *gateway.Stack,
	pullRequests []int,
) error {
	if len(stack.PullRequests) < len(pullRequests) ||
		!slices.Equal(
			stack.PullRequests[len(stack.PullRequests)-len(pullRequests):],
			pullRequests,
		) {
		return fmt.Errorf(
			"GitHub stack %d order is %v, local order is %v",
			stack.Number,
			stack.PullRequests,
			pullRequests,
		)
	}

	omitted := stack.PullRequests[:len(stack.PullRequests)-len(pullRequests)]
	if len(omitted) == 0 {
		return nil
	}
	changes := make([]forge.ChangeID, len(omitted))
	for i, pullRequest := range omitted {
		changes[i] = &PR{Number: pullRequest}
	}
	statuses, err := r.ChangeStatuses(ctx, changes)
	if err != nil {
		return fmt.Errorf("check omitted GitHub stack prefix: %w", err)
	}
	if len(statuses) != len(omitted) {
		return fmt.Errorf(
			"check omitted GitHub stack prefix: got %d statuses for %d pull requests",
			len(statuses),
			len(omitted),
		)
	}
	for i, status := range statuses {
		if status.State != forge.ChangeMerged {
			return fmt.Errorf(
				"GitHub stack %d includes unmerged pull request %d before local order %v",
				stack.Number,
				omitted[i],
				pullRequests,
			)
		}
	}
	return nil
}
