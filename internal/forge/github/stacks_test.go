package github

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/github"
	"go.abhg.dev/gs/internal/silog"
	"go.abhg.dev/gs/internal/silog/silogtest"
	"go.uber.org/mock/gomock"
)

func TestRepository_UpdateStackCreate(t *testing.T) {
	gateway := NewMockGithubGateway(gomock.NewController(t))
	expectPullRequests(t, gateway, map[int]*github.StackUpdatePullRequest{
		1: newStackPullRequest(nil),
		2: newStackPullRequest(nil),
	})
	gateway.EXPECT().CreatePullRequestStack(gomock.Any(), &github.CreatePullRequestStackInput{
		Owner:        "acme",
		Repo:         "repo",
		PullRequests: []int{1, 2},
	}).Return(nil)

	err := newStackRepository(t, gateway).UpdateStack(t.Context(), []forge.StackChange{
		{Change: &PR{Number: 2}, Base: &PR{Number: 1}},
		{Change: &PR{Number: 1}},
	})
	require.NoError(t, err)
}

func TestRepository_UpdateStackCurrent(t *testing.T) {
	gateway := NewMockGithubGateway(gomock.NewController(t))
	stack := &github.PullRequestStack{Number: 42, OpenPullRequests: []int{1, 2}}
	expectPullRequests(t, gateway, map[int]*github.StackUpdatePullRequest{
		1: newStackPullRequest(stack),
		2: newStackPullRequest(stack),
	})

	err := newStackRepository(t, gateway).UpdateStack(t.Context(), []forge.StackChange{
		{Change: &PR{Number: 1}},
		{Change: &PR{Number: 2}, Base: &PR{Number: 1}},
	})
	require.NoError(t, err)
}

func TestRepository_UpdateStackExtend(t *testing.T) {
	gateway := NewMockGithubGateway(gomock.NewController(t))
	stack := &github.PullRequestStack{Number: 42, OpenPullRequests: []int{1, 2}}
	expectPullRequests(t, gateway, map[int]*github.StackUpdatePullRequest{
		1: newStackPullRequest(stack),
		2: newStackPullRequest(stack),
		3: newStackPullRequest(nil),
	})
	gateway.EXPECT().AddPullRequestsToStack(gomock.Any(), &github.AddPullRequestsToStackInput{
		Owner:        "acme",
		Repo:         "repo",
		StackNumber:  42,
		PullRequests: []int{3},
	}).Return(nil)

	err := newStackRepository(t, gateway).UpdateStack(t.Context(), []forge.StackChange{
		{Change: &PR{Number: 1}},
		{Change: &PR{Number: 2}, Base: &PR{Number: 1}},
		{Change: &PR{Number: 3}, Base: &PR{Number: 2}},
	})
	require.NoError(t, err)
}

func TestRepository_UpdateStackDivergent(t *testing.T) {
	gateway := NewMockGithubGateway(gomock.NewController(t))
	expectPullRequests(t, gateway, map[int]*github.StackUpdatePullRequest{
		1: newStackPullRequest(nil),
		2: newStackPullRequest(nil),
		3: newStackPullRequest(nil),
		4: newStackPullRequest(nil),
		5: newStackPullRequest(nil),
	})
	gateway.EXPECT().CreatePullRequestStack(gomock.Any(), &github.CreatePullRequestStackInput{
		Owner:        "acme",
		Repo:         "repo",
		PullRequests: []int{1, 3, 4},
	}).Return(nil)

	var logs strings.Builder
	repo := newStackRepository(t, gateway)
	repo.log = silog.New(&logs, &silog.Options{Level: silog.LevelDebug})
	err := repo.UpdateStack(t.Context(), []forge.StackChange{
		{Change: &PR{Number: 3}, Base: &PR{Number: 1}},
		{Change: &PR{Number: 4}, Base: &PR{Number: 3}},
		{Change: &PR{Number: 1}},
		{Change: &PR{Number: 2}, Base: &PR{Number: 1}},
		{Change: &PR{Number: 5}, Base: &PR{Number: 2}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(logs.String(),
		"#2: Leaving pull request and its upstack out of the GitHub native stack: the change tree diverges from the selected linear path"))
	assert.NotContains(t, logs.String(), "#5:")
}

func TestRepository_UpdateStackPrefersExistingStackOverLongerPath(t *testing.T) {
	gateway := NewMockGithubGateway(gomock.NewController(t))
	stack := &github.PullRequestStack{
		Number:           42,
		OpenPullRequests: []int{1, 2, 4},
	}
	expectPullRequests(t, gateway, map[int]*github.StackUpdatePullRequest{
		1: newStackPullRequest(stack),
		2: newStackPullRequest(stack),
		3: newStackPullRequest(nil),
		4: newStackPullRequest(stack),
		5: newStackPullRequest(nil),
	})

	var logs strings.Builder
	repo := newStackRepository(t, gateway)
	repo.log = silog.New(&logs, &silog.Options{Level: silog.LevelDebug})
	err := repo.UpdateStack(t.Context(), []forge.StackChange{
		{Change: &PR{Number: 1}},
		{Change: &PR{Number: 2}, Base: &PR{Number: 1}},
		{Change: &PR{Number: 3}, Base: &PR{Number: 2}},
		{Change: &PR{Number: 4}, Base: &PR{Number: 2}},
		{Change: &PR{Number: 5}, Base: &PR{Number: 3}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(logs.String(),
		"#3: Leaving pull request and its upstack out of the GitHub native stack: the change tree diverges from the selected linear path"))
	assert.NotContains(t, logs.String(), "#4:")
	assert.NotContains(t, logs.String(), "incompatible membership")
}

func TestRepository_UpdateStackIncompatible(t *testing.T) {
	gateway := NewMockGithubGateway(gomock.NewController(t))
	stack := &github.PullRequestStack{Number: 42, OpenPullRequests: []int{1, 3}}
	expectPullRequests(t, gateway, map[int]*github.StackUpdatePullRequest{
		1: newStackPullRequest(stack),
		2: newStackPullRequest(stack),
	})

	var logs strings.Builder
	repo := newStackRepository(t, gateway)
	repo.log = silog.New(&logs, &silog.Options{Level: silog.LevelDebug})
	err := repo.UpdateStack(t.Context(), []forge.StackChange{
		{Change: &PR{Number: 1}},
		{Change: &PR{Number: 2}, Base: &PR{Number: 1}},
	})
	require.NoError(t, err)
	assert.Contains(t, logs.String(),
		"#1: Leaving pull request and its upstack in existing GitHub native stack #42: open pull requests #1, #3 have incompatible membership")
}

func TestRepository_UpdateStackMultipleExistingStacks(t *testing.T) {
	gateway := NewMockGithubGateway(gomock.NewController(t))
	expectPullRequests(t, gateway, map[int]*github.StackUpdatePullRequest{
		1: newStackPullRequest(&github.PullRequestStack{
			Number:           42,
			OpenPullRequests: []int{1},
		}),
		2: newStackPullRequest(&github.PullRequestStack{
			Number:           43,
			OpenPullRequests: []int{2},
		}),
	})

	var logs strings.Builder
	repo := newStackRepository(t, gateway)
	repo.log = silog.New(&logs, &silog.Options{Level: silog.LevelDebug})
	err := repo.UpdateStack(t.Context(), []forge.StackChange{
		{Change: &PR{Number: 1}},
		{Change: &PR{Number: 2}, Base: &PR{Number: 1}},
	})
	require.NoError(t, err)
	assert.Contains(t, logs.String(),
		"#1: Leaving pull request and its upstack in existing GitHub native stacks: pull requests belong to different native stacks")
}

func TestRepository_UpdateStackUnsupported(t *testing.T) {
	gateway := NewMockGithubGateway(gomock.NewController(t))
	expectPullRequests(t, gateway, map[int]*github.StackUpdatePullRequest{
		1: newStackPullRequest(nil),
		2: newStackPullRequest(nil),
	})
	gateway.EXPECT().CreatePullRequestStack(gomock.Any(), gomock.Any()).
		Return(github.ErrNotFound)

	err := newStackRepository(t, gateway).UpdateStack(t.Context(), []forge.StackChange{
		{Change: &PR{Number: 1}},
		{Change: &PR{Number: 2}, Base: &PR{Number: 1}},
	})
	require.ErrorIs(t, err, forge.ErrUnsupported)
	assert.ErrorIs(t, err, github.ErrNotFound)
}

func TestRepository_UpdateStackDisabled(t *testing.T) {
	gateway := NewMockGithubGateway(gomock.NewController(t))
	repo, err := newRepository(
		t.Context(),
		new(Forge),
		"acme",
		"repo",
		silogtest.New(t),
		gateway,
		"repo-id",
	)
	require.NoError(t, err)

	err = repo.UpdateStack(t.Context(), []forge.StackChange{
		{Change: &PR{Number: 1}},
	})
	require.ErrorIs(t, err, forge.ErrUnsupported)
}

func TestRepository_UpdateStackMissingChange(t *testing.T) {
	gateway := NewMockGithubGateway(gomock.NewController(t))
	expectPullRequests(t, gateway, map[int]*github.StackUpdatePullRequest{1: nil})

	err := newStackRepository(t, gateway).UpdateStack(t.Context(), []forge.StackChange{
		{Change: &PR{Number: 1}},
	})
	require.ErrorIs(t, err, forge.ErrNotFound)
}

func TestRepository_UpdateStackLookupFailure(t *testing.T) {
	gateway := NewMockGithubGateway(gomock.NewController(t))
	gateway.EXPECT().PullRequestsForStackUpdate(
		gomock.Any(),
		"acme",
		"repo",
		[]int{1},
	).Return(nil, github.ErrForbidden)

	err := newStackRepository(t, gateway).UpdateStack(t.Context(), []forge.StackChange{
		{Change: &PR{Number: 1}},
	})
	require.ErrorIs(t, err, github.ErrForbidden)
}

func TestRepository_UpdateStackCanceled(t *testing.T) {
	gateway := NewMockGithubGateway(gomock.NewController(t))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := newStackRepository(t, gateway).UpdateStack(ctx, []forge.StackChange{
		{Change: &PR{Number: 1}},
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestRepository_UpdateStackMultipleRoots(t *testing.T) {
	gateway := NewMockGithubGateway(gomock.NewController(t))
	expectPullRequests(t, gateway, map[int]*github.StackUpdatePullRequest{
		1:  newStackPullRequest(nil),
		2:  newStackPullRequest(nil),
		10: newStackPullRequest(nil),
		11: newStackPullRequest(nil),
	})
	gateway.EXPECT().CreatePullRequestStack(gomock.Any(), &github.CreatePullRequestStackInput{
		Owner:        "acme",
		Repo:         "repo",
		PullRequests: []int{1, 2},
	}).Return(nil)
	gateway.EXPECT().CreatePullRequestStack(gomock.Any(), &github.CreatePullRequestStackInput{
		Owner:        "acme",
		Repo:         "repo",
		PullRequests: []int{10, 11},
	}).Return(nil)

	err := newStackRepository(t, gateway).UpdateStack(t.Context(), []forge.StackChange{
		{Change: &PR{Number: 11}, Base: &PR{Number: 10}},
		{Change: &PR{Number: 2}, Base: &PR{Number: 1}},
		{Change: &PR{Number: 10}},
		{Change: &PR{Number: 1}},
	})
	require.NoError(t, err)
}

func TestRepository_UpdateStackContinuesAfterFailure(t *testing.T) {
	gateway := NewMockGithubGateway(gomock.NewController(t))
	expectPullRequests(t, gateway, map[int]*github.StackUpdatePullRequest{
		1:  newStackPullRequest(nil),
		2:  newStackPullRequest(nil),
		10: newStackPullRequest(nil),
		11: newStackPullRequest(nil),
	})
	gateway.EXPECT().CreatePullRequestStack(gomock.Any(), &github.CreatePullRequestStackInput{
		Owner:        "acme",
		Repo:         "repo",
		PullRequests: []int{1, 2},
	}).Return(github.ErrForbidden)
	gateway.EXPECT().CreatePullRequestStack(gomock.Any(), &github.CreatePullRequestStackInput{
		Owner:        "acme",
		Repo:         "repo",
		PullRequests: []int{10, 11},
	}).Return(nil)

	err := newStackRepository(t, gateway).UpdateStack(t.Context(), []forge.StackChange{
		{Change: &PR{Number: 1}},
		{Change: &PR{Number: 2}, Base: &PR{Number: 1}},
		{Change: &PR{Number: 10}},
		{Change: &PR{Number: 11}, Base: &PR{Number: 10}},
	})
	require.ErrorIs(t, err, github.ErrForbidden)
}

func TestRepository_UpdateStackReconnectsAboveMergedChange(t *testing.T) {
	gateway := NewMockGithubGateway(gomock.NewController(t))
	merged := newStackPullRequest(nil)
	merged.State = github.PullRequestStateMerged
	expectPullRequests(t, gateway, map[int]*github.StackUpdatePullRequest{
		1: merged,
		2: newStackPullRequest(nil),
		3: newStackPullRequest(nil),
	})
	gateway.EXPECT().CreatePullRequestStack(gomock.Any(), &github.CreatePullRequestStackInput{
		Owner:        "acme",
		Repo:         "repo",
		PullRequests: []int{2, 3},
	}).Return(nil)

	err := newStackRepository(t, gateway).UpdateStack(t.Context(), []forge.StackChange{
		{Change: &PR{Number: 1}},
		{Change: &PR{Number: 2}, Base: &PR{Number: 1}},
		{Change: &PR{Number: 3}, Base: &PR{Number: 2}},
	})
	require.NoError(t, err)
}

func newStackRepository(t *testing.T, gateway githubGateway) *Repository {
	return &Repository{
		owner:         "acme",
		repo:          "repo",
		gateway:       gateway,
		log:           silogtest.New(t),
		stacksEnabled: true,
	}
}

func newStackPullRequest(stack *github.PullRequestStack) *github.StackUpdatePullRequest {
	return &github.StackUpdatePullRequest{
		State:               github.PullRequestStateOpen,
		Stack:               stack,
		HeadRepositoryOwner: "acme",
		HeadRepositoryName:  "repo",
	}
}

func expectPullRequests(
	t *testing.T,
	gateway *MockGithubGateway,
	pullRequests map[int]*github.StackUpdatePullRequest,
) {
	t.Helper()
	gateway.EXPECT().PullRequestsForStackUpdate(
		gomock.Any(),
		"acme",
		"repo",
		gomock.Any(),
	).DoAndReturn(func(
		_ context.Context,
		_, _ string,
		numbers []int,
	) ([]*github.StackUpdatePullRequest, error) {
		require.Len(t, numbers, len(pullRequests))
		result := make([]*github.StackUpdatePullRequest, len(numbers))
		for i, number := range numbers {
			var ok bool
			result[i], ok = pullRequests[number]
			require.True(t, ok, "unexpected pull request #%d", number)
		}
		return result, nil
	})
}
