package main

import (
	"context"
	"errors"

	"go.abhg.dev/gs/internal/spice"
	"go.abhg.dev/gs/internal/text"
)

type stackReorderCmd struct {
	Branches []string `arg:"" help:"Branches from bottom to top" predictor:"trackedBranches"`
}

func (*stackReorderCmd) Help() string {
	return text.Dedent(`
		Reorder a linear stack without opening an editor.
		Provide every branch from closest to trunk to furthest from trunk.

		For example:

		    gs stack reorder foundation api docs tests
	`)
}

func (cmd *stackReorderCmd) Run(ctx context.Context, svc *spice.Service) error {
	if len(cmd.Branches) < 2 {
		return errors.New("stack reorder requires at least two branches")
	}
	_, err := svc.StackReorder(ctx, &spice.StackReorderRequest{
		Stack: cmd.Branches,
	})
	return err
}
