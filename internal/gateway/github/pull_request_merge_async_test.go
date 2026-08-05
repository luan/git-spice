package github

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGateway_MergePullRequestAsync(t *testing.T) {
	gateway := newTestGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/repos/octo/hello/pulls/42/merge-async", r.URL.Path)
		assert.Equal(t, githubAPIVersion, r.Header.Get("X-GitHub-Api-Version"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.JSONEq(t, `{
			"sha":"abc123",
			"merge_method":"squash",
			"merge_action":"default"
		}`, string(body))
		return stackResponse(http.StatusAccepted, `{
			"status":"pending",
			"details":{"uuid":"merge-uuid","expected_head_sha":"abc123","merge_method":"squash"}
		}`), nil
	}))

	result, err := gateway.MergePullRequestAsync(t.Context(), "octo", "hello", 42, &MergePullRequestAsyncInput{
		SHA:         "abc123",
		MergeMethod: "squash",
		MergeAction: "default",
	})
	require.NoError(t, err)
	assert.Equal(t, "pending", result.Status)
	assert.Equal(t, "merge-uuid", result.Details.UUID)
}

func TestGateway_MergePullRequestAsync_inProgress(t *testing.T) {
	gateway := newTestGateway(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return stackResponse(http.StatusConflict, `{
			"status":"pending",
			"details":{
				"uuid":"merge-uuid",
				"expected_head_sha":"abc123",
				"merge_method":"squash",
				"merge_action":"default"
			}
		}`), nil
	}))

	result, err := gateway.MergePullRequestAsync(t.Context(), "octo", "hello", 42, &MergePullRequestAsyncInput{
		SHA:         "abc123",
		MergeMethod: "squash",
		MergeAction: "default",
	})
	require.NoError(t, err)
	assert.Equal(t, "merge-uuid", result.Details.UUID)
}

func TestGateway_MergePullRequestAsync_rejectsDifferentInProgressRequest(t *testing.T) {
	gateway := newTestGateway(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return stackResponse(http.StatusConflict, `{
			"status":"pending",
			"details":{
				"uuid":"merge-uuid",
				"expected_head_sha":"different",
				"merge_method":"merge",
				"merge_action":"direct_merge"
			}
		}`), nil
	}))

	_, err := gateway.MergePullRequestAsync(t.Context(), "octo", "hello", 42, &MergePullRequestAsyncInput{
		SHA:         "abc123",
		MergeMethod: "squash",
		MergeAction: "default",
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "expects head")
}

func TestGateway_MergePullRequestAsync_rejectsDifferentInProgressAction(t *testing.T) {
	gateway := newTestGateway(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return stackResponse(http.StatusConflict, `{
			"status":"pending",
			"details":{
				"uuid":"merge-uuid",
				"expected_head_sha":"abc123",
				"merge_method":"squash",
				"merge_action":"direct_merge"
			}
		}`), nil
	}))

	_, err := gateway.MergePullRequestAsync(t.Context(), "octo", "hello", 42, &MergePullRequestAsyncInput{
		SHA:         "abc123",
		MergeMethod: "squash",
		MergeAction: "default",
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "uses action")
}

func TestGateway_PullRequestAsyncMerge(t *testing.T) {
	gateway := newTestGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/repos/octo/hello/pulls/42/merge-async/merge-uuid", r.URL.Path)
		return stackResponse(http.StatusOK, `{
			"status":"merged",
			"details":{"sha":"merged-sha","message":"Pull request merged"}
		}`), nil
	}))

	result, err := gateway.PullRequestAsyncMerge(t.Context(), "octo", "hello", 42, "merge-uuid")
	require.NoError(t, err)
	assert.Equal(t, "merged", result.Status)
	assert.Equal(t, "merged-sha", result.Details.SHA)
}
