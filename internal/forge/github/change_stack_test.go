package github

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/forge"
	gateway "go.abhg.dev/gs/internal/gateway/github"
	"go.uber.org/mock/gomock"
)

func TestRepository_EnsureChangeStack(t *testing.T) {
	t.Run("Create", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(nil, nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 42).
			Return(nil, nil)
		client.EXPECT().
			CreateStack(gomock.Any(), "octo", "hello", []int{41, 42}).
			Return(&gateway.Stack{Number: 7}, nil)

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		err := repo.EnsureChangeStack(t.Context(), []forge.ChangeID{&PR{Number: 41}, &PR{Number: 42}})
		require.NoError(t, err)
	})
	t.Run("FindsLaterStack", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(nil, nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 42).
			Return(&gateway.Stack{Number: 7, PullRequests: []int{42, 43}}, nil)

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		err := repo.EnsureChangeStack(t.Context(), []forge.ChangeID{&PR{Number: 41}, &PR{Number: 42}})
		require.NoError(t, err)
	})
	t.Run("RejectsMultipleExistingStacks", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(&gateway.Stack{Number: 7, PullRequests: []int{41, 42}}, nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 42).
			Return(&gateway.Stack{Number: 7, PullRequests: []int{41, 42}}, nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 43).
			Return(&gateway.Stack{Number: 8, PullRequests: []int{43, 44}}, nil)

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		err := repo.EnsureChangeStack(t.Context(), []forge.ChangeID{
			&PR{Number: 41}, &PR{Number: 42}, &PR{Number: 43},
		})
		require.Error(t, err)
		assert.ErrorContains(t, err, "multiple GitHub stacks")
	})

	t.Run("Extend", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(&gateway.Stack{Number: 7, PullRequests: []int{41, 42}}, nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 42).
			Return(&gateway.Stack{Number: 7, PullRequests: []int{41, 42}}, nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 43).
			Return(&gateway.Stack{Number: 7, PullRequests: []int{41, 42}}, nil)
		client.EXPECT().
			AddToStack(gomock.Any(), "octo", "hello", 7, []int{43}).
			Return(&gateway.Stack{Number: 7}, nil)

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		err := repo.EnsureChangeStack(t.Context(), []forge.ChangeID{&PR{Number: 41}, &PR{Number: 42}, &PR{Number: 43}})
		require.NoError(t, err)
	})

	t.Run("Unavailable", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(nil, &gateway.RESTError{StatusCode: http.StatusNotFound})

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		err := repo.EnsureChangeStack(t.Context(), []forge.ChangeID{&PR{Number: 41}, &PR{Number: 42}})
		require.NoError(t, err)
	})
	t.Run("UnsupportedAPI", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(nil, &gateway.RESTError{StatusCode: http.StatusGone})

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		err := repo.EnsureChangeStack(t.Context(), []forge.ChangeID{&PR{Number: 41}, &PR{Number: 42}})
		require.NoError(t, err)
	})

	t.Run("LeavesDivergentStack", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(&gateway.Stack{Number: 7, PullRequests: []int{41, 99}}, nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 42).
			Return(&gateway.Stack{Number: 7, PullRequests: []int{41, 99}}, nil)

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		err := repo.EnsureChangeStack(t.Context(), []forge.ChangeID{&PR{Number: 41}, &PR{Number: 42}})
		require.NoError(t, err)
	})

	t.Run("LeavesRemoteStackAfterShrink", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(&gateway.Stack{Number: 7, PullRequests: []int{41, 42}}, nil)

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		err := repo.EnsureChangeStack(t.Context(), []forge.ChangeID{&PR{Number: 41}})
		require.NoError(t, err)
	})

}
