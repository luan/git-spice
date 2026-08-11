package main

import (
	"context"

	"github.com/alecthomas/kong"
	"go.abhg.dev/gs/internal/git"
	"go.abhg.dev/gs/internal/handler/absorb"
	"go.abhg.dev/gs/internal/spice"
	"go.abhg.dev/gs/internal/spice/state"
	"go.abhg.dev/gs/internal/text"
)

type commitAbsorbCmd struct {
	absorb.Options
}

func (*commitAbsorbCmd) Help() string {
	return text.Dedent(`
		Apply staged changes to the commits they belong to in the current branch.
		The tracked Git-Spice branch base limits which commits can be absorbed,
		and branches above the current branch are restacked by default.

		This command uses git-absorb and intentionally does not expose
		--base, --no-limit, or --force-detach because they would escape
		the current tracked stack branch.
	`)
}

type AbsorbHandler interface {
	Absorb(context.Context, *absorb.Request) error
}

var _ AbsorbHandler = (*absorb.Handler)(nil)

func (cmd *commitAbsorbCmd) AfterApply(kctx *kong.Context) error {
	return kctx.BindToProvider(func(
		wt *git.Worktree,
		store *state.Store,
		svc *spice.Service,
		restackHandler RestackHandler,
	) (AbsorbHandler, error) {
		return &absorb.Handler{
			Worktree: wt,
			Store:    store,
			Service:  svc,
			Restack:  restackHandler,
		}, nil
	})
}

func (cmd *commitAbsorbCmd) Run(ctx context.Context, handler AbsorbHandler) error {
	return handler.Absorb(ctx, &absorb.Request{
		Options:         &cmd.Options,
		ContinueCommand: cmd.continueCommand(),
	})
}

func (cmd *commitAbsorbCmd) continueCommand() []string {
	command := []string{"commit", "absorb"}
	if cmd.Restack.None() {
		command = append(command, "--no-restack")
	}
	if cmd.DryRun {
		command = append(command, "--dry-run")
	}
	if cmd.ForceAuthor {
		command = append(command, "--force-author")
	}
	if cmd.Force {
		command = append(command, "--force")
	}
	if cmd.WholeFile {
		command = append(command, "--whole-file")
	}
	if cmd.OneFixupPerCommit {
		command = append(command, "--one-fixup-per-commit")
	}
	if cmd.Squash {
		command = append(command, "--squash")
	}
	if cmd.Message != "" {
		command = append(command, "--message", cmd.Message)
	}
	return command
}
