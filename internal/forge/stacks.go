package forge

import "context"

// WithStacks is an optional repository capability for updating native stacks.
type WithStacks interface {
	Repository

	// UpdateStack updates provider state to represent the supplied change
	// relationships.
	// Each entry identifies a change and, when present, its immediate base from
	// the same request.
	// An entry with no base starts a stack tree;
	// a base that is absent from the request is treated the same way.
	//
	// Implementations may update independent trees before returning an error.
	// A non-nil error therefore does not guarantee that every tree was left
	// unchanged.
	//
	// [ErrUnsupported] means the native-stack operation is unavailable.
	// Provider-specific limits on individual trees do not make the capability
	// unsupported.
	UpdateStack(context.Context, []StackChange) error
}

// StackChange identifies one member and its immediate base in an update
// request.
type StackChange struct {
	// Change identifies the stack member.
	Change ChangeID // required

	// Base identifies the immediate base change.
	// It may be nil for a tree root.
	// A base that is not included in the request is treated as a tree root.
	Base ChangeID
}
