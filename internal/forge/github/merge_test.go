package github

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/gateway/github"
	"go.abhg.dev/gs/internal/silog/silogtest"
	"go.uber.org/mock/gomock"
)

func TestRepository_MergeChange_pollsAsyncEndpointWhenRequired(t *testing.T) {
	ctrl := gomock.NewController(t)
	gateway := NewMockGithubGateway(ctrl)
	expectedHead := "abc123"
	expectedMethod := github.MergeMethodSquash
	gateway.EXPECT().
		PullRequestID(gomock.Any(), "owner", "repo", 42).
		Return(github.ID("prID"), nil)
	gateway.EXPECT().
		MergePullRequest(gomock.Any(), &github.MergePullRequestInput{
			PullRequestID:   "prID",
			ExpectedHeadOID: &expectedHead,
			MergeMethod:     &expectedMethod,
		}).
		Return(github.ErrUnprocessable)
	gateway.EXPECT().
		FindStackForPullRequest(gomock.Any(), "owner", "repo", 42).
		Return(&github.Stack{Number: 7}, nil)
	gateway.EXPECT().
		MergePullRequestAsync(
			gomock.Any(),
			"owner",
			"repo",
			42,
			&github.MergePullRequestAsyncInput{
				SHA:         "abc123",
				MergeMethod: "squash",
				MergeAction: "default",
			},
		).
		Return(&github.MergePullRequestAsyncResult{
			Status: "pending",
			Details: github.MergePullRequestAsyncResultDetails{
				UUID: "merge-uuid",
			},
		}, nil)
	gateway.EXPECT().
		PullRequestAsyncMerge(gomock.Any(), "owner", "repo", 42, "merge-uuid").
		Return(&github.MergePullRequestAsyncResult{Status: "merged"}, nil)

	repo := &Repository{
		owner:   "owner",
		repo:    "repo",
		log:     silogtest.New(t),
		gateway: gateway,
	}
	err := repo.MergeChange(t.Context(), &PR{Number: 42, GQLID: "prID"}, forge.MergeChangeOptions{
		HeadHash: "abc123",
		Method:   forge.MergeMethodSquash,
		Timeout:  time.Second,
	})
	require.NoError(t, err)
}

func TestRepository_MergeChange_usesGraphQLForOrdinaryPullRequest(t *testing.T) {
	ctrl := gomock.NewController(t)
	gateway := NewMockGithubGateway(ctrl)
	gateway.EXPECT().
		MergePullRequest(gomock.Any(), &github.MergePullRequestInput{
			PullRequestID: "prID",
		}).
		Return(nil)

	repo := &Repository{
		owner:   "owner",
		repo:    "repo",
		log:     silogtest.New(t),
		gateway: gateway,
	}
	require.NoError(t, repo.MergeChange(
		t.Context(),
		&PR{Number: 42, GQLID: "prID"},
		forge.MergeChangeOptions{},
	))
}

func TestRepository_MergeChange_reportsAsyncFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	gateway := NewMockGithubGateway(ctrl)
	gateway.EXPECT().
		PullRequestID(gomock.Any(), "owner", "repo", 42).
		Return(github.ID("prID"), nil)
	gateway.EXPECT().
		MergePullRequest(gomock.Any(), gomock.Any()).
		Return(github.ErrUnprocessable)
	gateway.EXPECT().
		FindStackForPullRequest(gomock.Any(), "owner", "repo", 42).
		Return(&github.Stack{Number: 7}, nil)
	gateway.EXPECT().
		MergePullRequestAsync(gomock.Any(), "owner", "repo", 42, gomock.Any()).
		Return(&github.MergePullRequestAsyncResult{
			Status: "failed",
			Details: github.MergePullRequestAsyncResultDetails{
				Message: "merge requirements not met",
			},
		}, nil)

	repo := &Repository{
		owner:   "owner",
		repo:    "repo",
		log:     silogtest.New(t),
		gateway: gateway,
	}
	err := repo.MergeChange(t.Context(), &PR{Number: 42, GQLID: "prID"}, forge.MergeChangeOptions{})
	require.Error(t, err)
	assert.ErrorContains(t, err, "merge requirements not met")
}

func TestRepository_mergePullRequestAsync_enqueued(t *testing.T) {
	tests := []struct {
		name    string
		initial *github.MergePullRequestAsyncResult
		polled  bool
	}{
		{
			name:    "Initial",
			initial: &github.MergePullRequestAsyncResult{Status: "enqueued"},
		},
		{
			name: "Polled",
			initial: &github.MergePullRequestAsyncResult{
				Status: "pending",
				Details: github.MergePullRequestAsyncResultDetails{
					UUID: "merge-uuid",
				},
			},
			polled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			gateway := NewMockGithubGateway(ctrl)
			gateway.EXPECT().
				MergePullRequestAsync(
					gomock.Any(),
					"owner",
					"repo",
					42,
					gomock.Any(),
				).
				Return(tt.initial, nil)
			if tt.polled {
				gateway.EXPECT().
					PullRequestAsyncMerge(
						gomock.Any(),
						"owner",
						"repo",
						42,
						"merge-uuid",
					).
					Return(&github.MergePullRequestAsyncResult{
						Status: "enqueued",
					}, nil)
			}

			repo := &Repository{
				owner:   "owner",
				repo:    "repo",
				log:     silogtest.New(t),
				gateway: gateway,
			}
			err := repo.mergePullRequestAsync(
				t.Context(),
				42,
				forge.MergeChangeOptions{Timeout: time.Second},
			)
			require.NoError(t, err)
		})
	}
}

func TestRepository_MergeChange_doesNotUseAsyncWithoutNativeStack(t *testing.T) {
	ctrl := gomock.NewController(t)
	gateway := NewMockGithubGateway(ctrl)
	gateway.EXPECT().
		MergePullRequest(gomock.Any(), gomock.Any()).
		Return(github.ErrUnprocessable)
	gateway.EXPECT().
		FindStackForPullRequest(gomock.Any(), "owner", "repo", 42).
		Return(nil, nil)

	repo := &Repository{
		owner:   "owner",
		repo:    "repo",
		log:     silogtest.New(t),
		gateway: gateway,
	}
	err := repo.MergeChange(
		t.Context(),
		&PR{Number: 42, GQLID: "prID"},
		forge.MergeChangeOptions{},
	)
	require.ErrorIs(t, err, github.ErrUnprocessable)
}
