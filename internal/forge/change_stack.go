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

// ValidateChangeStack verifies that changes match a forge's existing ordered
// stack without mutating it. Repositories without native stack support ignore
// the request.
func ValidateChangeStack(ctx context.Context, repo Repository, changes []ChangeID) error {
	validator, ok := repo.(interface {
		ValidateChangeStack(context.Context, []ChangeID) error
	})
	if !ok {
		return nil
	}
	return validator.ValidateChangeStack(ctx, changes)
}

// ValidateChangeStackSelection verifies that selected changes belong to one
// native stack in the supplied order. Repositories without native stack
// support ignore the request.
func ValidateChangeStackSelection(
	ctx context.Context,
	repo Repository,
	changes []ChangeID,
) error {
	validator, ok := repo.(interface {
		ValidateChangeStackSelection(context.Context, []ChangeID) error
	})
	if !ok {
		return nil
	}
	return validator.ValidateChangeStackSelection(ctx, changes)
}

// CanMergeChangeStack reports whether the forge can merge the supplied
// ordered changes as one server-managed stack operation.
func CanMergeChangeStack(
	ctx context.Context,
	repo Repository,
	changes []ChangeID,
) (bool, error) {
	merger, ok := repo.(interface {
		CanMergeChangeStack(context.Context, []ChangeID) (bool, error)
	})
	if !ok {
		return false, nil
	}
	return merger.CanMergeChangeStack(ctx, changes)
}

// MergeChangeStack asks a forge to merge one existing ordered stack as a
// server-managed operation. It reports whether the forge handled the request.
func MergeChangeStack(
	ctx context.Context,
	repo Repository,
	changes []ChangeID,
	opts MergeChangeOptions,
) (bool, error) {
	merger, ok := repo.(interface {
		MergeChangeStack(context.Context, []ChangeID, MergeChangeOptions) (bool, error)
	})
	if !ok {
		return false, nil
	}
	return merger.MergeChangeStack(ctx, changes, opts)
}
