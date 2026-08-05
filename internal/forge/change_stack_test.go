package forge_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/forge/forgetest"
	"go.uber.org/mock/gomock"
)

type stackRepository struct {
	forge.Repository
	got []forge.ChangeID
}

func (r *stackRepository) EnsureChangeStack(_ context.Context, changes []forge.ChangeID) error {
	r.got = changes
	return assert.AnError
}

func TestEnsureChangeStack(t *testing.T) {
	ctrl := gomock.NewController(t)
	changes := []forge.ChangeID{forgetest.NewMockChangeID(ctrl)}
	repo := &stackRepository{Repository: forgetest.NewMockRepository(ctrl)}

	err := forge.EnsureChangeStack(t.Context(), repo, changes)

	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, changes, repo.got)
}

func TestEnsureChangeStack_unsupported(t *testing.T) {
	ctrl := gomock.NewController(t)
	err := forge.EnsureChangeStack(t.Context(), forgetest.NewMockRepository(ctrl), nil)
	require.NoError(t, err)
}
