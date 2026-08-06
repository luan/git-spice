// Package absorb implements stack-aware git absorb operations.
package absorb

import (
	"context"
	"errors"
	"fmt"

	"go.abhg.dev/gs/internal/git"
	"go.abhg.dev/gs/internal/handler/restack"
	"go.abhg.dev/gs/internal/spice"
)

// GitWorktree is the worktree surface needed by Handler.
type GitWorktree interface {
	CurrentBranch(context.Context) (string, error)
	Absorb(context.Context, git.AbsorbRequest) error
	RebaseState(context.Context) (*git.RebaseState, error)
}

// Store is the repository state surface needed by Handler.
type Store interface {
	Trunk() string
}

// Service is the Git-Spice service surface needed by Handler.
type Service interface {
	BranchGraph(context.Context, *spice.BranchGraphOptions) (*spice.BranchGraph, error)
	RebaseRescue(context.Context, spice.RebaseRescueRequest) error
}

// RestackHandler restacks branches above the absorbed branch.
type RestackHandler interface {
	RestackUpstack(context.Context, *restack.UpstackRequest) error
}

// Handler implements stack-aware absorb operations.
type Handler struct {
	Worktree GitWorktree    // required
	Store    Store          // required
	Service  Service        // required
	Restack  RestackHandler // required when restacking
}

// Request configures an absorb operation.
type Request struct {
	Restack         spice.AutoRestackMode
	ContinueCommand []string
}

// Absorb applies staged changes to commits in the current tracked branch.
func (h *Handler) Absorb(ctx context.Context, req *Request) error {
	branch, err := h.Worktree.CurrentBranch(ctx)
	if err != nil {
		if errors.Is(err, git.ErrDetachedHead) {
			return errors.New("HEAD is detached; cannot absorb changes")
		}
		return fmt.Errorf("get current branch: %w", err)
	}
	if branch == h.Store.Trunk() {
		return fmt.Errorf("cannot absorb changes on trunk branch %q", branch)
	}

	graph, err := h.Service.BranchGraph(ctx, nil)
	if err != nil {
		return fmt.Errorf("load branch graph: %w", err)
	}
	tracked, ok := graph.Lookup(branch)
	if !ok {
		return fmt.Errorf("branch not tracked: %s", branch)
	}
	if tracked.Base == "" {
		return fmt.Errorf("branch %s has no tracked base", branch)
	}

	if err := h.Worktree.Absorb(ctx, git.AbsorbRequest{Base: tracked.Base}); err != nil {
		if _, stateErr := h.Worktree.RebaseState(ctx); stateErr == nil {
			command := req.ContinueCommand
			if len(command) == 0 {
				command = []string{"commit", "absorb"}
			}
			return h.Service.RebaseRescue(ctx, spice.RebaseRescueRequest{
				Err:     err,
				Branch:  branch,
				Command: command,
				Message: fmt.Sprintf("absorb changes into %s", branch),
			})
		}
		return fmt.Errorf("absorb changes: %w", err)
	}

	if req.Restack.None() {
		return nil
	}
	return h.Restack.RestackUpstack(ctx, &restack.UpstackRequest{
		Branch: branch,
		Options: &restack.UpstackOptions{
			SkipStart: true,
		},
	})
}

var _ GitWorktree = (*git.Worktree)(nil)
var _ Service = (*spice.Service)(nil)
var _ RestackHandler = (*restack.Handler)(nil)
