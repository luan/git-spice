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
