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

	t.Run("ValidateExactOrder", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		stack := &gateway.Stack{Number: 7, PullRequests: []int{41, 42}}
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(stack, nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 42).
			Return(stack, nil)

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		err := repo.ValidateChangeStack(t.Context(), []forge.ChangeID{&PR{Number: 41}, &PR{Number: 42}})
		require.NoError(t, err)
	})

	t.Run("ValidateRejectsPartialMembership", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		stack := &gateway.Stack{Number: 7, PullRequests: []int{41, 42}}
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(stack, nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 42).
			Return(nil, nil)

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		err := repo.ValidateChangeStack(
			t.Context(),
			[]forge.ChangeID{&PR{Number: 41}, &PR{Number: 42}},
		)
		require.Error(t, err)
		assert.ErrorContains(t, err, "not all pull requests belong to GitHub stack 7")
	})

	t.Run("ValidateRejectsDifferentOrder", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		stack := &gateway.Stack{Number: 7, PullRequests: []int{42, 41}}
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(stack, nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 42).
			Return(stack, nil)

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		err := repo.ValidateChangeStack(t.Context(), []forge.ChangeID{&PR{Number: 41}, &PR{Number: 42}})
		require.Error(t, err)
		assert.ErrorContains(t, err, "GitHub stack 7 order is [42 41], local order is [41 42]")
	})

	t.Run("ValidateAllowsMergedPrefix", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		stack := &gateway.Stack{Number: 7, PullRequests: []int{40, 41, 42}}
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(stack, nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 42).
			Return(stack, nil)
		client.EXPECT().
			PullRequestID(gomock.Any(), "octo", "hello", 40).
			Return(gateway.ID("pr40"), nil)
		client.EXPECT().
			ChangeStatuses(gomock.Any(), []gateway.ID{"pr40"}).
			Return([]*gateway.ChangeStatus{{
				State: gateway.PullRequestStateMerged,
			}}, nil)

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		err := repo.ValidateChangeStack(
			t.Context(),
			[]forge.ChangeID{&PR{Number: 41}, &PR{Number: 42}},
		)
		require.NoError(t, err)
	})

	t.Run("ValidateRejectsUnmergedPrefix", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		stack := &gateway.Stack{Number: 7, PullRequests: []int{40, 41, 42}}
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(stack, nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 42).
			Return(stack, nil)
		client.EXPECT().
			PullRequestID(gomock.Any(), "octo", "hello", 40).
			Return(gateway.ID("pr40"), nil)
		client.EXPECT().
			ChangeStatuses(gomock.Any(), []gateway.ID{"pr40"}).
			Return([]*gateway.ChangeStatus{{
				State: gateway.PullRequestStateOpen,
			}}, nil)

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		err := repo.ValidateChangeStack(
			t.Context(),
			[]forge.ChangeID{&PR{Number: 41}, &PR{Number: 42}},
		)
		require.Error(t, err)
		assert.ErrorContains(t, err, "includes unmerged pull request 40")
	})
	t.Run("ValidateSelectionAllowsPartialStack", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		stack := &gateway.Stack{Number: 7, PullRequests: []int{40, 41, 42}}
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(stack, nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 42).
			Return(stack, nil)

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		err := repo.ValidateChangeStackSelection(
			t.Context(),
			[]forge.ChangeID{&PR{Number: 41}, &PR{Number: 42}},
		)
		require.NoError(t, err)
	})

	t.Run("MergeNativeStack", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		stack := &gateway.Stack{Number: 7, PullRequests: []int{41, 42}}
		client.EXPECT().
			PullRequestID(gomock.Any(), "octo", "hello", 41).
			Return(gateway.ID("pr-41"), nil)
		client.EXPECT().
			PullRequestID(gomock.Any(), "octo", "hello", 42).
			Return(gateway.ID("pr-42"), nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(stack, nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 42).
			Return(stack, nil)
		client.EXPECT().
			MergePullRequestAsync(
				gomock.Any(),
				"octo",
				"hello",
				42,
				&gateway.MergePullRequestAsyncInput{
					SHA:         "top-head",
					MergeMethod: "squash",
					MergeAction: "default",
				},
			).
			Return(&gateway.MergePullRequestAsyncResult{Status: "merged"}, nil)

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		handled, err := repo.MergeChangeStack(
			t.Context(),
			[]forge.ChangeID{
				&PR{Number: 41, GQLID: "pr-41"},
				&PR{Number: 42, GQLID: "pr-42"},
			},
			forge.MergeChangeOptions{
				HeadHash: "top-head",
				Method:   forge.MergeMethodSquash,
			},
		)
		assert.True(t, handled)
		require.NoError(t, err)
	})

	t.Run("MergeRejectsDifferentOrder", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		client.EXPECT().
			PullRequestID(gomock.Any(), "octo", "hello", 41).
			Return(gateway.ID("pr-41"), nil)
		client.EXPECT().
			PullRequestID(gomock.Any(), "octo", "hello", 42).
			Return(gateway.ID("pr-42"), nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(&gateway.Stack{
				Number:       7,
				PullRequests: []int{42, 41},
			}, nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 42).
			Return(&gateway.Stack{
				Number:       7,
				PullRequests: []int{42, 41},
			}, nil)

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		handled, err := repo.MergeChangeStack(
			t.Context(),
			[]forge.ChangeID{
				&PR{Number: 41, GQLID: "pr-41"},
				&PR{Number: 42, GQLID: "pr-42"},
			},
			forge.MergeChangeOptions{},
		)
		assert.False(t, handled)
		require.Error(t, err)
		assert.ErrorContains(t, err, "GitHub stack 7 order is [42 41]")
	})

	t.Run("MergeRejectsMismatchedNodeID", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		client.EXPECT().
			PullRequestID(gomock.Any(), "octo", "hello", 41).
			Return(gateway.ID("actual-pr-41"), nil)

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		handled, err := repo.MergeChangeStack(
			t.Context(),
			[]forge.ChangeID{
				&PR{Number: 41, GQLID: "stale-pr-41"},
				&PR{Number: 42, GQLID: "pr-42"},
			},
			forge.MergeChangeOptions{},
		)
		assert.False(t, handled)
		require.Error(t, err)
		assert.ErrorContains(t, err, `GitHub node ID is "actual-pr-41", persisted ID is "stale-pr-41"`)
	})

	t.Run("MergeRejectsPartialMembership", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		client := NewMockGithubGateway(ctrl)
		client.EXPECT().
			PullRequestID(gomock.Any(), "octo", "hello", 41).
			Return(gateway.ID("pr-41"), nil)
		client.EXPECT().
			PullRequestID(gomock.Any(), "octo", "hello", 42).
			Return(gateway.ID("pr-42"), nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 41).
			Return(nil, nil)
		client.EXPECT().
			FindStackForPullRequest(gomock.Any(), "octo", "hello", 42).
			Return(&gateway.Stack{
				Number:       7,
				PullRequests: []int{41, 42},
			}, nil)

		repo := &Repository{owner: "octo", repo: "hello", gateway: client}
		handled, err := repo.MergeChangeStack(
			t.Context(),
			[]forge.ChangeID{
				&PR{Number: 41, GQLID: "pr-41"},
				&PR{Number: 42, GQLID: "pr-42"},
			},
			forge.MergeChangeOptions{},
		)
		assert.False(t, handled)
		require.Error(t, err)
		assert.ErrorContains(t, err, "not all pull requests belong to GitHub stack 7")
	})
}
