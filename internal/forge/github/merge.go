package github

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/github"
)

const defaultAsyncMergeTimeout = 2 * time.Minute

// MergeChange merges an open pull request into its base branch.
func (r *Repository) MergeChange(
	ctx context.Context, fid forge.ChangeID,
	opts forge.MergeChangeOptions,
) error {
	if err := r.ValidateChangeIDs(ctx, []forge.ChangeID{fid}); err != nil {
		return err
	}
	id := mustPR(fid)
	gqlID := id.GQLID
	mergeErr := r.mergePullRequest(ctx, gqlID, opts)
	if mergeErr == nil {
		return nil
	}
	if !errors.Is(mergeErr, github.ErrUnprocessable) {
		return mergeErr
	}
	stack, err := r.gateway.FindStackForPullRequest(
		ctx,
		r.owner,
		r.repo,
		id.Number,
	)
	if errors.Is(err, github.ErrNotFound) ||
		errors.Is(err, github.ErrUnsupportedAPI) {
		return mergeErr
	}
	if err != nil {
		return fmt.Errorf("verify native GitHub stack membership: %w", err)
	}
	if stack == nil {
		return mergeErr
	}
	if err := r.validateRemoteChangeIDs(ctx, []forge.ChangeID{fid}); err != nil {
		return err
	}

	if err := r.mergePullRequestAsync(ctx, id.Number, opts); err != nil {
		return fmt.Errorf("merge pull request asynchronously: %w", err)
	}
	r.log.Debug("Merged pull request asynchronously", "pr", id.Number)
	return nil
}

func (r *Repository) mergePullRequest(
	ctx context.Context,
	gqlID github.ID,
	opts forge.MergeChangeOptions,
) error {
	input := github.MergePullRequestInput{
		PullRequestID: gqlID,
	}
	if opts.HeadHash != "" {
		input.ExpectedHeadOID = new(opts.HeadHash.String())
	}
	switch opts.Method {
	case forge.MergeMethodDefault:
	case forge.MergeMethodMerge:
		input.MergeMethod = new(github.MergeMethodMerge)
	case forge.MergeMethodSquash:
		input.MergeMethod = new(github.MergeMethodSquash)
	case forge.MergeMethodRebase:
		input.MergeMethod = new(github.MergeMethodRebase)
	default:
		r.log.Warn(
			"Unsupported merge method; using forge default",
			"method", opts.Method,
		)
	}
	if err := r.gateway.MergePullRequest(ctx, &input); err != nil {
		return fmt.Errorf("merge pull request: %w", err)
	}
	return nil
}

func (r *Repository) asyncMergeInput(
	opts forge.MergeChangeOptions,
) github.MergePullRequestAsyncInput {
	input := github.MergePullRequestAsyncInput{
		MergeAction: "default",
	}
	if opts.HeadHash != "" {
		input.SHA = opts.HeadHash.String()
	}
	switch opts.Method {
	case forge.MergeMethodDefault:
	case forge.MergeMethodMerge:
		input.MergeMethod = "merge"
	case forge.MergeMethodSquash:
		input.MergeMethod = "squash"
	case forge.MergeMethodRebase:
		input.MergeMethod = "rebase"
	default:
		r.log.Warn(
			"Unsupported merge method; using forge default",
			"method", opts.Method,
		)
	}
	return input
}

func (r *Repository) mergePullRequestAsync(
	ctx context.Context,
	pullNumber int,
	opts forge.MergeChangeOptions,
) error {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultAsyncMergeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	input := r.asyncMergeInput(opts)
	result, err := r.gateway.MergePullRequestAsync(
		ctx,
		r.owner,
		r.repo,
		pullNumber,
		&input,
	)
	if err != nil {
		return err
	}

	for {
		switch result.Status {
		case "merged", "enqueued":
			return nil
		case "failed":
			return fmt.Errorf("GitHub asynchronous merge failed: %s", result.Details.Message)
		case "pending":
			if result.Details.UUID == "" {
				return fmt.Errorf("GitHub asynchronous merge did not return an operation UUID")
			}
		case "":
			return fmt.Errorf("GitHub asynchronous merge returned no status")
		default:
			return fmt.Errorf("GitHub asynchronous merge returned unknown status %q", result.Status)
		}

		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for GitHub asynchronous merge: %w", ctx.Err())
		case <-timer.C:
		}
		result, err = r.gateway.PullRequestAsyncMerge(
			ctx,
			r.owner,
			r.repo,
			pullNumber,
			result.Details.UUID,
		)
		if err != nil {
			return fmt.Errorf("get GitHub asynchronous merge result: %w", err)
		}
	}
}
