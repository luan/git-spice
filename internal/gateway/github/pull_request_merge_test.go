package github

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGateway_MergePullRequest_stackErrorRemainsUnprocessable(t *testing.T) {
	gateway := newTestGateway(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return stackResponse(http.StatusOK, `{
			"errors":[{
				"type":"UNPROCESSABLE",
				"message":"This pull request is part of a stack and must be merged using the asynchronous merge REST API."
			}]
		}`), nil
	}))

	err := gateway.MergePullRequest(t.Context(), &MergePullRequestInput{
		PullRequestID: "pr-id",
	})
	require.ErrorIs(t, err, ErrUnprocessable)
}

func TestGateway_MergePullRequest_otherUnprocessable(t *testing.T) {
	gateway := newTestGateway(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return stackResponse(http.StatusOK, `{
			"errors":[{
				"type":"UNPROCESSABLE",
				"message":"Pull request is not mergeable"
			}]
		}`), nil
	}))

	err := gateway.MergePullRequest(t.Context(), &MergePullRequestInput{
		PullRequestID: "pr-id",
	})
	require.ErrorIs(t, err, ErrUnprocessable)
}
