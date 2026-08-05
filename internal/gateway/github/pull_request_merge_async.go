package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// MergePullRequestAsyncInput configures GitHub's asynchronous pull request
// merge endpoint.
type MergePullRequestAsyncInput struct {
	SHA         string `json:"sha,omitempty"`
	MergeMethod string `json:"merge_method,omitempty"`
	MergeAction string `json:"merge_action,omitempty"`
}

// MergePullRequestAsyncResult reports the state of an asynchronous merge.
type MergePullRequestAsyncResult struct {
	Status  string                             `json:"status"`
	Details MergePullRequestAsyncResultDetails `json:"details"`
}

// MergePullRequestAsyncResultDetails identifies and describes one asynchronous
// merge operation.
type MergePullRequestAsyncResultDetails struct {
	Message         string `json:"message"`
	UUID            string `json:"uuid"`
	SHA             string `json:"sha"`
	ExpectedHeadSHA string `json:"expected_head_sha"`
	MergeMethod     string `json:"merge_method"`
	MergeAction     string `json:"merge_action"`
}

// MergePullRequestAsync requests an asynchronous merge for a pull request.
// GitHub uses this endpoint for pull requests that belong to native stacks.
func (c *Gateway) MergePullRequestAsync(
	ctx context.Context,
	owner, repo string,
	pullNumber int,
	input *MergePullRequestAsyncInput,
) (*MergePullRequestAsyncResult, error) {
	path := fmt.Sprintf("repos/%s/%s/pulls/%d/merge-async", owner, repo, pullNumber)
	var result MergePullRequestAsyncResult
	_, err := c.executeREST(ctx, http.MethodPut, path, input, &result)
	var restErr *RESTError
	if !errors.As(err, &restErr) || restErr.StatusCode != http.StatusConflict {
		return &result, err
	}

	if err := json.Unmarshal([]byte(restErr.Body), &result); err != nil {
		return nil, fmt.Errorf("decode in-progress asynchronous merge: %w", err)
	}
	if result.Status != "pending" || result.Details.UUID == "" {
		return nil, fmt.Errorf("asynchronous merge conflict did not identify a pending request")
	}
	if input.SHA != "" && result.Details.ExpectedHeadSHA != input.SHA {
		return nil, fmt.Errorf(
			"in-progress asynchronous merge expects head %q, requested %q",
			result.Details.ExpectedHeadSHA,
			input.SHA,
		)
	}
	if input.MergeMethod != "" && result.Details.MergeMethod != input.MergeMethod {
		return nil, fmt.Errorf(
			"in-progress asynchronous merge uses method %q, requested %q",
			result.Details.MergeMethod,
			input.MergeMethod,
		)
	}
	if input.MergeAction != "" && result.Details.MergeAction != input.MergeAction {
		return nil, fmt.Errorf(
			"in-progress asynchronous merge uses action %q, requested %q",
			result.Details.MergeAction,
			input.MergeAction,
		)
	}
	return &result, nil
}

// PullRequestAsyncMerge gets the latest result for an asynchronous merge.
func (c *Gateway) PullRequestAsyncMerge(
	ctx context.Context,
	owner, repo string,
	pullNumber int,
	uuid string,
) (*MergePullRequestAsyncResult, error) {
	path := fmt.Sprintf(
		"repos/%s/%s/pulls/%d/merge-async/%s",
		owner,
		repo,
		pullNumber,
		uuid,
	)
	var result MergePullRequestAsyncResult
	if _, err := c.executeREST(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
