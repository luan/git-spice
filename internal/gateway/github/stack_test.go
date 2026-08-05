package github

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGateway_ListStacks(t *testing.T) {
	gateway := newTestGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/repos/octo/hello/stacks", r.URL.Path)
		return stackResponse(http.StatusOK, `[{"number":7,"pull_requests":[{"number":41},{"number":42}]}]`), nil
	}))

	stacks, err := gateway.ListStacks(t.Context(), "octo", "hello")
	require.NoError(t, err)
	require.Len(t, stacks, 1)
	assert.Equal(t, []int{41, 42}, stacks[0].PullRequests)
}

func TestGateway_GetStack(t *testing.T) {
	gateway := newTestGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/repos/octo/hello/stacks/7", r.URL.Path)
		return stackResponse(http.StatusOK, `{"number":7,"pull_requests":[{"number":41},{"number":42}]}`), nil
	}))

	stack, err := gateway.GetStack(t.Context(), "octo", "hello", 7)
	require.NoError(t, err)
	assert.Equal(t, []int{41, 42}, stack.PullRequests)
}

func TestGateway_FindStackForPullRequest(t *testing.T) {
	gateway := newTestGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/repos/octo/hello/stacks", r.URL.Path)
		assert.Equal(t, "42", r.URL.Query().Get("pull_request"))
		assert.Equal(t, "Bearer token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
		assert.Equal(t, githubAPIVersion, r.Header.Get("X-GitHub-Api-Version"))
		return stackResponse(http.StatusOK, `[{"number":7,"pull_requests":[{"number":41},{"number":42}]}]`), nil
	}))

	stack, err := gateway.FindStackForPullRequest(t.Context(), "octo", "hello", 42)
	require.NoError(t, err)
	require.NotNil(t, stack)
	assert.Equal(t, 7, stack.Number)
	assert.Equal(t, []int{41, 42}, stack.PullRequests)
}

func TestGateway_FindStackForPullRequest_notStacked(t *testing.T) {
	gateway := newResponseGateway(t, `[]`)
	stack, err := gateway.FindStackForPullRequest(t.Context(), "octo", "hello", 42)
	require.NoError(t, err)
	assert.Nil(t, stack)
}

func TestGateway_CreateStack(t *testing.T) {
	gateway := newTestGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/repos/octo/hello/stacks", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.JSONEq(t, `{"pull_requests":[41,42]}`, string(body))
		return stackResponse(http.StatusCreated, `{"number":7,"pull_requests":[{"number":41},{"number":42}]}`), nil
	}))

	stack, err := gateway.CreateStack(t.Context(), "octo", "hello", []int{41, 42})
	require.NoError(t, err)
	assert.Equal(t, []int{41, 42}, stack.PullRequests)
}

func TestGateway_AddToStack(t *testing.T) {
	gateway := newTestGateway(t, roundTripFunc(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/repos/octo/hello/stacks/7/add", r.URL.Path)
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.JSONEq(t, `{"pull_requests":[43]}`, string(body))
		return stackResponse(http.StatusOK, `{"number":7,"pull_requests":[{"number":41},{"number":42},{"number":43}]}`), nil
	}))

	stack, err := gateway.AddToStack(t.Context(), "octo", "hello", 7, []int{43})
	require.NoError(t, err)
	assert.Equal(t, []int{41, 42, 43}, stack.PullRequests)
}

func TestGateway_StackHTTPError(t *testing.T) {
	gateway := newTestGateway(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return stackResponse(http.StatusBadRequest, strings.Repeat("x", maxErrorBody+100)), nil
	}))

	_, err := gateway.CreateStack(t.Context(), "octo", "hello", []int{41, 42})
	require.Error(t, err)
	assert.ErrorContains(t, err, "400 Bad Request")
	assert.Less(t, len(err.Error()), maxErrorBody+200)
}

func TestGateway_StackUnsupportedAPI(t *testing.T) {
	gateway := newTestGateway(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return stackResponse(http.StatusGone, `{"message":"The specified API version is not supported."}`), nil
	}))

	_, err := gateway.FindStackForPullRequest(t.Context(), "octo", "hello", 42)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnsupportedAPI)
}

func stackResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     strconv.Itoa(status) + " " + http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

var _ TokenSource = tokenSourceFunc(func(context.Context) (string, error) { return "", nil })
