package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const githubAPIVersion = "2026-03-10"

// Stack is an ordered group of pull requests, from bottom to top.
type Stack struct {
	ID           int       `json:"id"`
	Number       int       `json:"number"`
	NodeID       string    `json:"node_id"`
	URL          string    `json:"url"`
	Base         StackBase `json:"base"`
	Open         bool      `json:"open"`
	CreatedAt    string    `json:"created_at"`
	PullRequests []int     `json:"-"`
}

// StackBase identifies the base ref of a stack.
type StackBase struct {
	Ref string `json:"ref"`
	SHA string `json:"sha,omitempty"`
}

func (s *Stack) UnmarshalJSON(data []byte) error {
	type stackAlias Stack
	var wire struct {
		stackAlias
		PullRequests []struct {
			Number int `json:"number"`
		} `json:"pull_requests"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*s = Stack(wire.stackAlias)
	s.PullRequests = make([]int, len(wire.PullRequests))
	for i, pr := range wire.PullRequests {
		s.PullRequests[i] = pr.Number
	}
	return nil
}

// ListStacks returns the repository's GitHub stacks.
func (c *Gateway) ListStacks(ctx context.Context, owner, repo string) ([]Stack, error) {
	path := fmt.Sprintf("repos/%s/%s/stacks", owner, repo)
	var stacks []Stack
	if _, err := c.executeREST(ctx, http.MethodGet, path, nil, &stacks); err != nil {
		return nil, err
	}
	if stacks == nil {
		stacks = []Stack{}
	}
	return stacks, nil
}

// FindStackForPullRequest returns the stack containing prNumber, if any.
func (c *Gateway) FindStackForPullRequest(
	ctx context.Context,
	owner, repo string,
	prNumber int,
) (*Stack, error) {
	path := fmt.Sprintf("repos/%s/%s/stacks", owner, repo)
	query := url.Values{"pull_request": {strconv.Itoa(prNumber)}}
	var stacks []Stack
	if _, err := c.executeREST(ctx, http.MethodGet, path+"?"+query.Encode(), nil, &stacks); err != nil {
		return nil, err
	}
	if len(stacks) == 0 {
		return nil, nil
	}
	return &stacks[0], nil
}

// GetStack returns one GitHub stack by its repository-local number.
func (c *Gateway) GetStack(
	ctx context.Context,
	owner, repo string,
	stackNumber int,
) (*Stack, error) {
	path := fmt.Sprintf("repos/%s/%s/stacks/%d", owner, repo, stackNumber)
	var stack Stack
	if _, err := c.executeREST(ctx, http.MethodGet, path, nil, &stack); err != nil {
		return nil, err
	}
	return &stack, nil
}

// CreateStack creates a GitHub stack from pull requests ordered bottom to top.
func (c *Gateway) CreateStack(
	ctx context.Context,
	owner, repo string,
	pullRequests []int,
) (*Stack, error) {
	path := fmt.Sprintf("repos/%s/%s/stacks", owner, repo)
	return c.writeStack(ctx, path, pullRequests)
}

// AddToStack appends pull requests to the top of a GitHub stack.
func (c *Gateway) AddToStack(
	ctx context.Context,
	owner, repo string,
	stackNumber int,
	pullRequests []int,
) (*Stack, error) {
	path := fmt.Sprintf("repos/%s/%s/stacks/%d/add", owner, repo, stackNumber)
	return c.writeStack(ctx, path, pullRequests)
}

func (c *Gateway) writeStack(ctx context.Context, path string, pullRequests []int) (*Stack, error) {
	var stack Stack
	_, err := c.executeREST(ctx, http.MethodPost, path, struct {
		PullRequests []int `json:"pull_requests"`
	}{PullRequests: pullRequests}, &stack)
	if err != nil {
		return nil, err
	}
	return &stack, nil
}

// RESTError reports a non-successful GitHub REST response.
type RESTError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *RESTError) Error() string {
	return fmt.Sprintf("GitHub REST HTTP status %s: %s", e.Status, e.Body)
}

func (e *RESTError) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return e.StatusCode == http.StatusNotFound
	case ErrForbidden:
		return e.StatusCode == http.StatusForbidden
	case ErrUnprocessable:
		return e.StatusCode == http.StatusUnprocessableEntity
	case ErrUnsupportedAPI:
		return e.StatusCode == http.StatusGone
	default:
		return false
	}
}

func (c *Gateway) executeREST(
	ctx context.Context,
	method, path string,
	input, result any,
) (int, error) {
	var body io.Reader
	if input != nil {
		var encoded bytes.Buffer
		if err := json.NewEncoder(&encoded).Encode(input); err != nil {
			return 0, fmt.Errorf("encode GitHub REST request: %w", err)
		}
		body = &encoded
	}

	endpoint := c.restEndpoint + "/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return 0, fmt.Errorf("build GitHub REST request: %w", err)
	}
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return 0, fmt.Errorf("get GitHub token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("send GitHub REST request: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		diagnostic, readErr := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
		if readErr != nil {
			return res.StatusCode, fmt.Errorf("GitHub REST HTTP status %s: read response: %w", res.Status, readErr)
		}
		return res.StatusCode, &RESTError{
			StatusCode: res.StatusCode,
			Status:     res.Status,
			Body:       strings.TrimSpace(string(diagnostic)),
		}
	}
	if res.StatusCode == http.StatusNoContent || result == nil {
		return res.StatusCode, nil
	}
	if err := json.NewDecoder(res.Body).Decode(result); err != nil {
		return res.StatusCode, fmt.Errorf("decode GitHub REST response: %w", err)
	}
	return res.StatusCode, nil
}
