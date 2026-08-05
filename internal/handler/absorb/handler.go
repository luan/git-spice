// Package absorb implements stack-aware git absorb operations.
package absorb

import (
	"cmp"
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

// Options configures a stack-aware absorb operation.
type Options struct {
	Restack spice.AutoRestackMode `negatable:"" default:"upstack" config:"commitAbsorb.restack" enum:"none,upstack" help:"Whether to restack upstack branches."`

	DryRun            bool   `help:"Show fixups without changing commits."`
	ForceAuthor       bool   `help:"Allow fixups to commits authored by someone else."`
	Force             bool   `short:"f" help:"Skip git-absorb safety checks."`
	WholeFile         bool   `short:"w" help:"Match changes against complete files."`
	OneFixupPerCommit bool   `short:"F" help:"Create at most one fixup per commit."`
	Squash            bool   `short:"s" help:"Create squash commits instead of fixups."`
	Message           string `short:"m" help:"Commit message body for generated fixups."`
}

// Request configures an absorb operation.
type Request struct {
	Options         *Options // optional
	ContinueCommand []string
}

// Absorb applies staged changes to commits in the current tracked branch.
func (h *Handler) Absorb(ctx context.Context, req *Request) error {
	opts := cmp.Or(req.Options, &Options{})
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

	absorbReq := git.AbsorbRequest{
		Base:              tracked.Base,
		DryRun:            opts.DryRun,
		ForceAuthor:       opts.ForceAuthor,
		Force:             opts.Force,
		WholeFile:         opts.WholeFile,
		OneFixupPerCommit: opts.OneFixupPerCommit,
		Squash:            opts.Squash,
		Message:           opts.Message,
	}
	if err := h.Worktree.Absorb(ctx, absorbReq); err != nil {
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

	if opts.DryRun || opts.Restack.None() {
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
