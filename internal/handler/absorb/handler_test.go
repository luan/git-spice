package absorb

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/git"
	"go.abhg.dev/gs/internal/handler/restack"
	"go.abhg.dev/gs/internal/spice"
	"go.abhg.dev/gs/internal/spice/spicetest"
)

func TestHandler_Absorb(t *testing.T) {
	t.Run("restacksUpstackFromTrackedBase", func(t *testing.T) {
		wt := &fakeWorktree{branch: "feature"}
		svc := &fakeService{graph: testGraph(t)}
		restack := new(fakeRestack)

		err := (&Handler{
			Worktree: wt,
			Store:    fakeStore{trunk: "main"},
			Service:  svc,
			Restack:  restack,
		}).Absorb(t.Context(), &Request{
			Options: &Options{
				Restack:   spice.AutoRestackUpstack,
				WholeFile: true,
			},
		})

		require.NoError(t, err)
		assert.Equal(t, git.AbsorbRequest{Base: "main", WholeFile: true}, wt.absorbRequest)
		assert.Equal(t, "feature", restack.branch)
		assert.True(t, restack.skipStart)
	})

	t.Run("noRestack", func(t *testing.T) {
		wt := &fakeWorktree{branch: "feature"}
		restack := new(fakeRestack)

		err := (&Handler{
			Worktree: wt,
			Store:    fakeStore{trunk: "main"},
			Service:  &fakeService{graph: testGraph(t)},
			Restack:  restack,
		}).Absorb(t.Context(), &Request{
			Options: &Options{Restack: spice.AutoRestackNone},
		})

		require.NoError(t, err)
		assert.Empty(t, restack.branch)
	})

	t.Run("dryRunDoesNotRestack", func(t *testing.T) {
		wt := &fakeWorktree{branch: "feature"}
		restack := new(fakeRestack)

		err := (&Handler{
			Worktree: wt,
			Store:    fakeStore{trunk: "main"},
			Service:  &fakeService{graph: testGraph(t)},
			Restack:  restack,
		}).Absorb(t.Context(), &Request{
			Options: &Options{
				Restack: spice.AutoRestackUpstack,
				DryRun:  true,
			},
		})

		require.NoError(t, err)
		assert.Equal(t, git.AbsorbRequest{Base: "main", DryRun: true}, wt.absorbRequest)
		assert.Empty(t, restack.branch)
	})

	t.Run("rejectsTrunk", func(t *testing.T) {
		err := (&Handler{
			Worktree: &fakeWorktree{branch: "main"},
			Store:    fakeStore{trunk: "main"},
			Service:  &fakeService{graph: testGraph(t)},
			Restack:  new(fakeRestack),
		}).Absorb(t.Context(), &Request{
			Options: &Options{Restack: spice.AutoRestackNone},
		})

		assert.ErrorContains(t, err, "cannot absorb changes on trunk")
	})

	t.Run("rescuesConflict", func(t *testing.T) {
		absorbErr := errors.New("conflict")
		wt := &fakeWorktree{
			branch:      "feature",
			absorbError: absorbErr,
			rebaseState: &git.RebaseState{Branch: "feature"},
		}
		svc := &fakeService{graph: testGraph(t)}

		err := (&Handler{
			Worktree: wt,
			Store:    fakeStore{trunk: "main"},
			Service:  svc,
			Restack:  new(fakeRestack),
		}).Absorb(t.Context(), &Request{
			Options: &Options{
				Restack: spice.AutoRestackNone,
			},
			ContinueCommand: []string{"commit", "absorb", "--no-restack"},
		})

		require.ErrorIs(t, err, svc.rescueError)
		assert.Equal(t, []string{"commit", "absorb", "--no-restack"}, svc.rescueRequest.Command)
	})
}

func testGraph(t *testing.T) *spice.BranchGraph {
	t.Helper()
	return spicetest.NewBranchGraph(t, spicetest.BranchGraphConfig{
		Trunk: "main",
		Branches: []spice.LoadBranchItem{
			{Name: "feature", Base: "main"},
			{Name: "top", Base: "feature"},
		},
	})
}

type fakeStore struct{ trunk string }

func (s fakeStore) Trunk() string { return s.trunk }

type fakeWorktree struct {
	branch        string
	currentErr    error
	absorbError   error
	rebaseState   *git.RebaseState
	absorbRequest git.AbsorbRequest
}

func (w *fakeWorktree) CurrentBranch(context.Context) (string, error) {
	return w.branch, w.currentErr
}

func (w *fakeWorktree) Absorb(_ context.Context, req git.AbsorbRequest) error {
	w.absorbRequest = req
	return w.absorbError
}

func (w *fakeWorktree) RebaseState(context.Context) (*git.RebaseState, error) {
	if w.rebaseState == nil {
		return nil, git.ErrNoRebase
	}
	return w.rebaseState, nil
}

type fakeService struct {
	graph         *spice.BranchGraph
	rescueRequest spice.RebaseRescueRequest
	rescueError   error
}

func (s *fakeService) BranchGraph(context.Context, *spice.BranchGraphOptions) (*spice.BranchGraph, error) {
	return s.graph, nil
}

func (s *fakeService) RebaseRescue(_ context.Context, req spice.RebaseRescueRequest) error {
	s.rescueRequest = req
	if s.rescueError == nil {
		s.rescueError = errors.New("rescued")
	}
	return s.rescueError
}

type fakeRestack struct {
	branch    string
	skipStart bool
}

func (r *fakeRestack) RestackUpstack(_ context.Context, req *restack.UpstackRequest) error {
	r.branch = req.Branch
	r.skipStart = req.Options != nil && req.Options.SkipStart
	return nil
}
