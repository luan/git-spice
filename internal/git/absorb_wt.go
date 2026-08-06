package git

import (
	"context"
	"errors"
)

// AbsorbRequest configures git absorb for the current worktree.
type AbsorbRequest struct {
	// Base limits candidate commits to the current stack branch.
	Base string
}

// Absorb applies staged changes to the commits in the current branch and
// rebases the branch with the generated fixups.
func (w *Worktree) Absorb(ctx context.Context, req AbsorbRequest) error {
	if req.Base == "" {
		return errors.New("absorb base is required")
	}
	return w.gitCmd(ctx, "absorb", "--base", req.Base, "--and-rebase").Run()
}
