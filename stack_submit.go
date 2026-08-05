package main

import (
	"context"
	"fmt"

	"go.abhg.dev/gs/internal/git"
	"go.abhg.dev/gs/internal/handler/submit"
	"go.abhg.dev/gs/internal/spice"
	"go.abhg.dev/gs/internal/spice/state"
	"go.abhg.dev/gs/internal/text"
)

type stackSubmitCmd struct {
	submitOptions
	submit.BatchOptions
}

func (*stackSubmitCmd) Help() string {
	return text.Dedent(`
		Change Requests are created or updated
		for all branches in the current stack.
	`) + "\n" + _submitHelp
}

func (cmd *stackSubmitCmd) Run(
	ctx context.Context,
	wt *git.Worktree,
	store *state.Store,
	svc *spice.Service,
	submitHandler SubmitHandler,
) error {
	currentBranch, err := wt.CurrentBranch(ctx)
	if err != nil {
		return fmt.Errorf("get current branch: %w", err)
	}

	graph, err := svc.BranchGraph(ctx, nil)
	if err != nil {
		return fmt.Errorf("build branch graph: %w", err)
	}

	toSubmit, err := selectStackBranches(graph, currentBranch, store.Trunk())
	if err != nil {
		return err
	}

	// TODO: separate preparation of the stack from submission

	return submitHandler.SubmitBatch(ctx, &submit.BatchRequest{
		Branches:      toSubmit,
		StackBranches: toSubmit,
		Options:       &cmd.Options,
		BatchOptions:  &cmd.BatchOptions,
		BranchGraph:   graph,
	})
}

func selectStackBranches(
	graph *spice.BranchGraph,
	branch, trunk string,
) ([]string, error) {
	stack, err := graph.StackLinear(branch)
	if err != nil {
		return nil, fmt.Errorf("cannot submit nonlinear stack: %w", err)
	}

	toSubmit := make([]string, 0, len(stack))
	for _, branch := range stack {
		if branch != trunk {
			toSubmit = append(toSubmit, branch)
		}
	}
	return toSubmit, nil
}
