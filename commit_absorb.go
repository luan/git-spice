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
	Restack spice.AutoRestackMode `negatable:"" default:"upstack" config:"commitAbsorb.restack" enum:"none,upstack" help:"Whether to restack upstack branches."`
}

func (*commitAbsorbCmd) Help() string {
	return text.Dedent(`
		Apply staged changes to the commits they belong to in the current branch.
		The current Git-Spice branch base limits which commits can be absorbed.
		Branches above the current branch are restacked by default.
		Use --no-restack to leave them untouched.

		This command requires the git-absorb executable.
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
		Restack:         cmd.Restack,
		ContinueCommand: cmd.continueCommand(),
	})
}

func (cmd *commitAbsorbCmd) continueCommand() []string {
	command := []string{"commit", "absorb"}
	if cmd.Restack.None() {
		command = append(command, "--no-restack")
	}
	return command
}
