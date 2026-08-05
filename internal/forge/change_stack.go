package forge

import "context"

// EnsureChangeStack asks a forge to represent changes as one ordered stack.
// Repositories without native stack support ignore the request.
func EnsureChangeStack(ctx context.Context, repo Repository, changes []ChangeID) error {
	stacker, ok := repo.(interface {
		EnsureChangeStack(context.Context, []ChangeID) error
	})
	if !ok {
		return nil
	}
	return stacker.EnsureChangeStack(ctx, changes)
}
