package github_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/fixturetest"
	"go.abhg.dev/gs/internal/forge"
	"go.abhg.dev/gs/internal/forge/forgetest"
	"go.abhg.dev/gs/internal/forge/github"
	githubgateway "go.abhg.dev/gs/internal/gateway/github"
	"go.abhg.dev/gs/internal/git"
	"go.abhg.dev/gs/internal/httptest"
	"go.abhg.dev/gs/internal/silog/silogtest"
	"golang.org/x/oauth2"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
)

// This file tests basic, end-to-end interactions with the GitHub API
// using recorded fixtures.

var _fixtures = fixturetest.Config{Update: forgetest.Update}

// testConfig returns the GitHub test configuration and sanitizers for VCR fixtures.
// In update mode, loads from testconfig.yaml.
// In replay mode, returns canonical placeholders.
func testConfig(t *testing.T) (cfg forgetest.ForgeConfig, sanitizers []httptest.Sanitizer) {
	config := forgetest.Config(t)
	cfg = config.GitHub
	canonical := forgetest.CanonicalGitHubConfig()
	sanitizers = forgetest.ConfigSanitizers(cfg, canonical)
	return cfg, sanitizers
}

// TODO: delete newRecorder when tests have been migrated to forgetest.
func newRecorder(
	t *testing.T,
	name string,
	sanitizers []httptest.Sanitizer,
) *recorder.Recorder {
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("To update the test fixtures, run:")
			t.Logf("    GITHUB_TEST_OWNER=$owner GITHUB_TEST_REPO=$repo GITHUB_TOKEN=$token go test -update -run '^%s$'", t.Name())
		}
	})

	return forgetest.NewHTTPRecorder(t, name, sanitizers)
}

func newGateway(t *testing.T, httpClient *http.Client) *githubgateway.Gateway {
	t.Helper()
	client, err := githubgateway.NewGateway(
		github.DefaultAPIURL,
		&http.Client{Transport: httpClient.Transport},
		testTokenSource("token"),
	)
	require.NoError(t, err)
	return client
}

func TestIntegration_Repository(t *testing.T) {
	cfg, sanitizers := testConfig(t)
	remoteURL := "https://github.com/" + cfg.Owner + "/" + cfg.Repo
	rec := newRecorder(t, t.Name(), sanitizers)

	httpClient := rec.GetDefaultClient()
	token := forgetest.Token(t, remoteURL, "GITHUB_TOKEN")
	httpClient.Transport = &oauth2.Transport{
		Base:   httpClient.Transport,
		Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token}),
	}

	gatewayClient := newGateway(t, httpClient)
	_, err := github.NewRepository(t.Context(), new(github.Forge), cfg.Owner, cfg.Repo, silogtest.New(t), gatewayClient, "")
	require.NoError(t, err)
}

func TestIntegration(t *testing.T) {
	cfg, sanitizers := testConfig(t)
	remoteURL := "https://github.com/" + cfg.Owner + "/" + cfg.Repo
	pushRemoteURL := "https://github.com/" + cfg.ForkOwner + "/" + cfg.ForkRepo

	t.Cleanup(func() {
		if t.Failed() && !forgetest.Update() {
			t.Logf("To update the test fixtures, run:")
			t.Logf("    Configure testconfig.yaml and run: GITHUB_TOKEN=$token go test -update -run '^%s$'", t.Name())
		}
	})

	githubForge := github.Forge{
		Log: silogtest.New(t),
		Options: github.Options{
			Stacks: true,
		},
	}

	forgetest.RunIntegration(t, forgetest.IntegrationConfig{
		RemoteURL:      remoteURL,
		PushRemoteURL:  pushRemoteURL,
		Forge:          &githubForge,
		TestStacks:     true,
		TestMergeRange: true,
		Sanitizers:     sanitizers,
		OpenRepository: func(t *testing.T, httpClient *http.Client) forge.Repository {
			token := forgetest.Token(t, remoteURL, "GITHUB_TOKEN")
			httpClient.Transport = &oauth2.Transport{
				Base: httpClient.Transport,
				Source: oauth2.StaticTokenSource(&oauth2.Token{
					AccessToken: token,
				}),
			}

			gatewayClient := newGateway(t, httpClient)
			newRepo, err := github.NewRepository(
				t.Context(), &githubForge, cfg.Owner, cfg.Repo,
				silogtest.New(t), gatewayClient, "",
			)
			require.NoError(t, err)
			return newRepo
		},
		CloseChange: func(t *testing.T, repo forge.Repository, change forge.ChangeID) {
			require.NoError(t, github.CloseChange(t.Context(), repo.(*github.Repository), change.(*github.PR)))
		},
		SetChangeCheck: func(
			t *testing.T,
			httpClient *http.Client,
			_ forge.Repository,
			_ forge.ChangeID,
			headHash git.Hash,
			check forge.ChangeCheck,
		) {
			require.NoError(t, setGitHubChangeChecksState(
				t.Context(),
				httpClient,
				cfg.Owner,
				cfg.Repo,
				headHash,
				check,
			))
		},
		SetCommentsPageSize: github.SetListChangeCommentsPageSize,
		Reviewers:           []string{cfg.Reviewer},
		Assignees:           []string{cfg.Assignee},
	})
}

func TestIntegration_DivergentStackMerge(t *testing.T) {
	cfg, sanitizers := testConfig(t)
	remoteURL := "https://github.com/" + cfg.Owner + "/" + cfg.Repo

	aBranch := fixturetest.New(_fixtures, "aBranch", func() string {
		return "divergent-stack-a-" + randomString(8)
	}).Get(t)
	bBranch := fixturetest.New(_fixtures, "bBranch", func() string {
		return "divergent-stack-b-" + randomString(8)
	}).Get(t)
	cBranch := fixturetest.New(_fixtures, "cBranch", func() string {
		return "divergent-stack-c-" + randomString(8)
	}).Get(t)
	dBranch := fixturetest.New(_fixtures, "dBranch", func() string {
		return "divergent-stack-d-" + randomString(8)
	}).Get(t)

	// Recording provisions a disposable remote branch graph and captures its
	// API traffic. Replay begins below with the recorded branch names and HTTP
	// fixture, without touching GitHub.
	if forgetest.Update() {
		testRepo := forgetest.NewRepositoryBuilder(t, remoteURL)
		t.Cleanup(func() {
			for _, branch := range []string{dBranch, cBranch, bBranch, aBranch} {
				testRepo.DeleteRemoteBranch(branch)
			}
		})

		// Build a divergent branch graph.
		// A-B-C becomes the native stack;
		// D remains an unstacked pull request based on B.
		//
		//     main
		//       |
		//       A
		//       |
		//       B
		//      / \
		//     C   D
		testRepo.CheckoutBranch("main")
		for _, branch := range []string{aBranch, bBranch, cBranch} {
			testRepo.CreateBranch(branch)
			testRepo.CheckoutBranch(branch)
			testRepo.WriteFile(branch+".txt", "commit for "+branch)
			testRepo.AddAllAndCommit("commit for " + branch)
			testRepo.Push(branch)
		}

		testRepo.CheckoutBranch(bBranch)
		testRepo.CreateBranch(dBranch)
		testRepo.CheckoutBranch(dBranch)
		testRepo.WriteFile(dBranch+".txt", "commit for "+dBranch)
		testRepo.AddAllAndCommit("commit for " + dBranch)
		testRepo.Push(dBranch)
	}

	rec := newRecorder(t, t.Name(), sanitizers)
	httpClient := rec.GetDefaultClient()
	token := forgetest.Token(t, remoteURL, "GITHUB_TOKEN")
	httpClient.Transport = &oauth2.Transport{
		Base:   httpClient.Transport,
		Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token}),
	}

	gatewayClient := newGateway(t, httpClient)
	repo, err := github.NewRepository(
		t.Context(), &github.Forge{
			Options: github.Options{
				Stacks: true,
			},
		}, cfg.Owner, cfg.Repo,
		silogtest.New(t), gatewayClient, "",
	)
	require.NoError(t, err)
	a, err := repo.SubmitChange(t.Context(), forge.SubmitChangeRequest{
		Subject: "Divergent native stack A " + aBranch,
		Body:    "Divergent native stack integration test",
		Base:    "main",
		Head:    aBranch,
	})
	require.NoError(t, err, "create change A")
	b, err := repo.SubmitChange(t.Context(), forge.SubmitChangeRequest{
		Subject: "Divergent native stack B " + bBranch,
		Body:    "Divergent native stack integration test",
		Base:    aBranch,
		Head:    bBranch,
	})
	require.NoError(t, err, "create change B")
	c, err := repo.SubmitChange(t.Context(), forge.SubmitChangeRequest{
		Subject: "Divergent native stack C " + cBranch,
		Body:    "Divergent native stack integration test",
		Base:    bBranch,
		Head:    cBranch,
	})
	require.NoError(t, err, "create change C")
	d, err := repo.SubmitChange(t.Context(), forge.SubmitChangeRequest{
		Subject: "Divergent native stack D " + dBranch,
		Body:    "Divergent native stack integration test",
		Base:    bBranch,
		Head:    dBranch,
	})
	require.NoError(t, err, "create change D")

	require.NoError(t, repo.UpdateStack(t.Context(), []forge.StackChange{
		{Change: a.ID},
		{Change: b.ID, Base: a.ID},
		{Change: c.ID, Base: b.ID},
	}), "register A-B-C as a native stack")

	changeIDs := []forge.ChangeID{a.ID, b.ID, c.ID, d.ID}
	beforeMerge, err := repo.ChangeStatuses(t.Context(), changeIDs)
	require.NoError(t, err, "read change head hashes before merge")
	require.Len(t, beforeMerge, len(changeIDs), "pre-merge status count")
	dHeadHash := beforeMerge[3].HeadHash

	operation, err := repo.MergeRange(t.Context(), forge.MergeRangeRequest{
		Method: forge.MergeMethodSquash,
		Changes: []forge.MergeRangeChange{
			{
				Change:   a.ID,
				Base:     "main",
				Head:     aBranch,
				HeadHash: beforeMerge[0].HeadHash,
			},
			{
				Change:   b.ID,
				Base:     aBranch,
				Head:     bBranch,
				HeadHash: beforeMerge[1].HeadHash,
			},
			{
				Change:   c.ID,
				Base:     bBranch,
				Head:     cBranch,
				HeadHash: beforeMerge[2].HeadHash,
			},
		},
	})
	require.NoError(t, err, "start A-B-C squash merge")

	status, err := operation.Status(t.Context())
	require.NoError(t, err, "probe A-B-C merge operation")
	assert.Equal(t, forge.MergeOperationPending, status,
		"A-B-C merge operation status")

	// This should be plenty of time--probably.
	// Bump it up if this test is flaky during recording.
	if forgetest.Update() {
		select {
		case <-time.After(20 * time.Second):

		case <-t.Context().Done():
			require.FailNow(t, "divergent stack test context canceled")
		}
	}

	status, err = operation.Status(t.Context())
	require.NoError(t, err, "probe A-B-C merge operation")
	assert.Equal(t, forge.MergeOperationAccepted, status,
		"A-B-C merge operation status")

	mergedStatuses, err := repo.ChangeStatuses(t.Context(), changeIDs[:3])
	require.NoError(t, err, "read A-B-C states after merge")
	require.Len(t, mergedStatuses, 3, "A-B-C status count")
	for _, status := range mergedStatuses {
		assert.Equal(t, forge.ChangeMerged, status.State,
			"change %d state", status)
	}

	dStatuses, err := repo.ChangeStatuses(t.Context(), []forge.ChangeID{d.ID})
	require.NoError(t, err, "read change D state after A-B-C merge")
	require.Len(t, dStatuses, 1, "change D status count")
	assert.Equal(t, forge.ChangeOpen, dStatuses[0].State)
	assert.Equal(t, dHeadHash, dStatuses[0].HeadHash)

	dPullRequests, err := gatewayClient.PullRequestsForMergeRange(
		t.Context(),
		cfg.Owner,
		cfg.Repo,
		[]int{d.ID.(*github.PR).Number},
	)
	require.NoError(t, err, "read change D after A-B-C merge")
	require.Len(t, dPullRequests, 1, "change D pull request count")
	dPullRequest := dPullRequests[0]
	require.NotNil(t, dPullRequest, "change D pull request")
	assert.Equal(t, githubgateway.PullRequestStateOpen, dPullRequest.State)
	assert.Equal(t, bBranch, dPullRequest.BaseRefName)
	assert.Equal(t, dBranch, dPullRequest.HeadRefName)
	assert.Equal(t, dHeadHash.String(), dPullRequest.HeadRefOID)
	assert.Nil(t, dPullRequest.Stack)

	mergeability, err := repo.ChangeMergeability(t.Context(), d.ID)
	require.NoError(t, err, "read change D mergeability")
	assert.Equal(t, forge.ChangeMergeabilityReady, mergeability.State,
		"change D mergeability: %v", mergeability.Reason)
}

func TestIntegration_Repository_LabelCreateDelete(t *testing.T) {
	cfg, sanitizers := testConfig(t)
	remoteURL := "https://github.com/" + cfg.Owner + "/" + cfg.Repo
	label := fixturetest.New(_fixtures, "label1", func() string { return randomString(8) }).Get(t)

	rec := newRecorder(t, t.Name(), sanitizers)
	httpClient := rec.GetDefaultClient()
	token := forgetest.Token(t, remoteURL, "GITHUB_TOKEN")
	httpClient.Transport = &oauth2.Transport{
		Base:   httpClient.Transport,
		Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token}),
	}

	gatewayClient := newGateway(t, httpClient)
	repo, err := github.NewRepository(
		t.Context(), new(github.Forge), cfg.Owner, cfg.Repo, silogtest.New(t), gatewayClient, "",
	)
	require.NoError(t, err)

	id, err := repo.CreateLabel(t.Context(), label)
	require.NoError(t, err, "could not create label")
	t.Cleanup(func() {
		t.Logf("Deleting label: %s", label)
		ctx := context.WithoutCancel(t.Context())
		assert.NoError(t,
			repo.DeleteLabel(ctx, label), "could not delete label")
	})

	t.Run("createIsIdempotent", func(t *testing.T) {
		newID, err := repo.CreateLabel(t.Context(), label)
		require.NoError(t, err, "could not create label again")

		assert.Equal(t, id, newID, "label ID should be the same on idempotent create")
	})
}

func TestIntegration_Repository_notFoundError(t *testing.T) {
	cfg, sanitizers := testConfig(t)
	remoteURL := "https://github.com/" + cfg.Owner + "/" + cfg.Repo
	ctx := t.Context()
	rec := newRecorder(t, t.Name(), sanitizers)
	client := rec.GetDefaultClient()
	token := forgetest.Token(t, remoteURL, "GITHUB_TOKEN")
	client.Transport = &oauth2.Transport{
		Base:   client.Transport,
		Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token}),
	}
	gatewayClient := newGateway(t, client)
	_, err := github.NewRepository(ctx, new(github.Forge), cfg.Owner, "does-not-exist-repo", silogtest.New(t), gatewayClient, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, githubgateway.ErrNotFound)

	var gqlError *githubgateway.Error
	if assert.ErrorAs(t, err, &gqlError) {
		assert.Equal(t, "NOT_FOUND", gqlError.Type)
		assert.Equal(t, []any{"repository"}, gqlError.Path)
		assert.Contains(t, gqlError.Message, cfg.Owner+"/does-not-exist-repo")
	}
}

type testTokenSource string

func (s testTokenSource) Token(context.Context) (string, error) { return string(s), nil }

const _alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// randomString generates a random alphanumeric string of length n.
func randomString(n int) string {
	b := make([]byte, n)
	for i := range b {
		var buf [1]byte
		_, _ = rand.Read(buf[:])
		idx := int(buf[0]) % len(_alnum)
		b[i] = _alnum[idx]
	}
	return string(b)
}

func setGitHubChangeChecksState(
	ctx context.Context,
	httpClient *http.Client,
	owner string,
	repo string,
	headHash git.Hash,
	check forge.ChangeCheck,
) error {
	// GitHub's GraphQL schema exposes the status rollup we read,
	// but commit status creation remains a REST API operation.
	// Check runs are a separate GitHub App-authenticated mechanism,
	// so these tests create classic commit statuses instead.
	body, err := json.Marshal(gitHubStatusRequest{
		State:       gitHubStatusState(check.State),
		Context:     check.Name,
		Description: "Synthetic status for git-spice integration tests",
	})
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf(
			"https://api.github.com/repos/%s/%s/statuses/%s",
			owner, repo, headHash,
		),
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("post status: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("post status: %s: %s", resp.Status, body)
	}
	return nil
}

type gitHubStatusRequest struct {
	State       string `json:"state"`
	Context     string `json:"context"`
	Description string `json:"description"`
}

func gitHubStatusState(state forge.ChangeCheckState) string {
	switch state {
	case forge.ChangeCheckPending:
		return "pending"
	case forge.ChangeCheckPassed:
		return "success"
	case forge.ChangeCheckFailed:
		return "failure"
	default:
		return "error"
	}
}
