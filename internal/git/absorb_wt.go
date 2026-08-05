package git

import (
	"context"
	"errors"
	"os"
)

// AbsorbRequest configures git absorb for the current worktree.
type AbsorbRequest struct {
	// Base limits candidate commits to the current stack branch.
	Base string

	DryRun            bool
	ForceAuthor       bool
	Force             bool
	WholeFile         bool
	OneFixupPerCommit bool
	Squash            bool
	Message           string
}

// Absorb applies staged changes to matching commits in the current branch.
func (w *Worktree) Absorb(ctx context.Context, req AbsorbRequest) error {
	if req.Base == "" {
		return errors.New("absorb base is required")
	}

	args := []string{"--base", req.Base}
	if req.DryRun {
		args = append(args, "--dry-run")
	} else {
		args = append(args, "--and-rebase")
	}
	if req.ForceAuthor {
		args = append(args, "--force-author")
	}
	if req.Force {
		args = append(args, "--force")
	}
	if req.WholeFile {
		args = append(args, "--whole-file")
	}
	if req.OneFixupPerCommit {
		args = append(args, "--one-fixup-per-commit")
	}
	if req.Squash {
		args = append(args, "--squash")
	}
	if req.Message != "" {
		args = append(args, "--message", req.Message)
	}
	cmd := w.gitCmd(ctx, "absorb", args...).WithStdout(os.Stdout)
	if !req.DryRun {
		cmd.AppendEnv("GIT_SEQUENCE_EDITOR=true")
	}
	return cmd.Run()
}
