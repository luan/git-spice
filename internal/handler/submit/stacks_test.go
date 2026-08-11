package submit

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/forge/forgetest"
	"go.abhg.dev/gs/internal/silog"
	"go.abhg.dev/gs/internal/spice"
	"go.abhg.dev/gs/internal/spice/spicetest"
	"go.abhg.dev/gs/internal/spice/state"
	"go.uber.org/mock/gomock"
)

func TestNativeStackChanges(t *testing.T) {
	graph := spicetest.NewBranchGraph(t, spicetest.BranchGraphConfig{
		Trunk: "main",
		Branches: []spice.LoadBranchItem{
			{Name: "other", Base: "main", Change: submitFakeChange("pr-9")},
			{Name: "top", Base: "middle", Change: submitFakeChange("pr-3")},
			{Name: "bottom", Base: "main", Change: submitFakeChange("pr-1")},
			{Name: "divergent", Base: "middle", Change: submitFakeChange("pr-4")},
			{Name: "middle", Base: "bottom", Change: submitFakeChange("pr-2")},
		},
	})

	got := nativeStackChanges(graph, "test", []string{"top"})
	assert.ElementsMatch(t, []forge.StackChange{
		{Change: submitFakeChangeID("pr-1")},
		{Change: submitFakeChangeID("pr-2"), Base: submitFakeChangeID("pr-1")},
		{Change: submitFakeChangeID("pr-3"), Base: submitFakeChangeID("pr-2")},
		{Change: submitFakeChangeID("pr-4"), Base: submitFakeChangeID("pr-2")},
	}, got)
}

func TestHandler_updateStackRepresentations_unsupported(t *testing.T) {
	ctrl := gomock.NewController(t)
	remoteForge := forgetest.NewMockForge(ctrl)
	remoteRepo := forgetest.NewMockRepository(ctrl)

	service := NewMockService(ctrl)
	handler := new(Handler)
	handler.Log = silog.Nop()
	handler.Service = service
	handler.FindRemote = func(context.Context) (state.Remote, error) {
		return state.Remote{Upstream: "origin"}, nil
	}
	handler.ResolveRepository = func(
		context.Context,
		string,
	) (forge.Forge, forge.RepositoryID, error) {
		return remoteForge, stubRepositoryID("acme/repo"), nil
	}
	handler.OpenRepository = func(
		context.Context,
		forge.Forge,
		forge.RepositoryID,
	) (forge.Repository, error) {
		return remoteRepo, nil
	}

	require.NoError(t, handler.updateStackRepresentations(
		t.Context(),
		&Options{NavComment: NavCommentNever},
		[]string{"feature"},
	))
}

func TestHandler_updateStackRepresentations_nativeStackErrors(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantLog bool
	}{
		{name: "Unsupported", err: forge.ErrUnsupported},
		{name: "Failure", err: errors.New("boom"), wantLog: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			remoteForge := forgetest.NewMockForge(ctrl)
			remoteForge.EXPECT().ID().Return("test").AnyTimes()

			remoteRepo := forgetest.NewMockRepository(ctrl)
			remoteRepo.EXPECT().Forge().Return(remoteForge).AnyTimes()
			stackRepo := &submitStackRepository{
				Repository: remoteRepo,
				updateErr:  tt.err,
			}

			graph := spicetest.NewBranchGraph(t, spicetest.BranchGraphConfig{
				Trunk: "main",
				Branches: []spice.LoadBranchItem{
					{Name: "feature", Base: "main", Change: submitFakeChange("pr-1")},
				},
			})
			service := NewMockService(ctrl)
			service.EXPECT().BranchGraph(gomock.Any(), nil).Return(graph, nil)

			var logs bytes.Buffer
			handler := new(Handler)
			handler.Log = silog.New(&logs, nil)
			handler.Service = service
			handler.FindRemote = func(context.Context) (state.Remote, error) {
				return state.Remote{Upstream: "origin"}, nil
			}
			handler.ResolveRepository = func(
				context.Context,
				string,
			) (forge.Forge, forge.RepositoryID, error) {
				return remoteForge, stubRepositoryID("acme/repo"), nil
			}
			handler.OpenRepository = func(
				context.Context,
				forge.Forge,
				forge.RepositoryID,
			) (forge.Repository, error) {
				return stackRepo, nil
			}

			require.NoError(t, handler.updateStackRepresentations(
				t.Context(),
				&Options{NavComment: NavCommentNever},
				[]string{"feature"},
			))
			require.Len(t, stackRepo.updates, 1)

			if tt.wantLog {
				assert.Contains(t, logs.String(), "Could not update stacks")
				assert.Contains(t, logs.String(), "boom")
			} else {
				assert.Empty(t, logs.String())
			}
		})
	}
}

type submitStackRepository struct {
	forge.Repository

	updates   [][]forge.StackChange
	updateErr error
}

func (r *submitStackRepository) UpdateStack(
	_ context.Context,
	changes []forge.StackChange,
) error {
	r.updates = append(r.updates, changes)
	return r.updateErr
}
