package github

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const maxStackPullRequests = 100

// PullRequestStack describes a native pull request stack.
type PullRequestStack struct {
	// Number is the number GitHub assigned to this stack.
	// It identifies the stack within its repository and is unrelated to member
	// pull request numbers or entry positions.
	Number int

	// OpenPullRequests lists the open stack members from the base upward.
	OpenPullRequests []int
}

// StackUpdatePullRequest is the compact pull request projection needed to
// reconcile native-stack membership.
type StackUpdatePullRequest struct {
	// State is the pull request lifecycle state.
	State PullRequestState

	// HeadRepositoryOwner is the login that owns the head repository.
	HeadRepositoryOwner string

	// HeadRepositoryName is the head repository name.
	HeadRepositoryName string

	// Stack is the native stack containing the pull request.
	// It is nil when the pull request was not stacked in the initial query or
	// when its referenced stack stopped resolving before the follow-up query.
	Stack *PullRequestStack
}

// PullRequestsForStackUpdate loads pull request eligibility and native-stack
// membership in input order.
// A nil result entry means that GitHub did not find the corresponding pull
// request.
// When Stack is non-nil, it includes all open members in base-up order.
//
// The result combines two API snapshots because GitHub exposes stack identity
// on the pull request and ordered membership on a separate stack node.
// If a referenced stack stops resolving between those requests, the pull
// request result remains present with Stack nil.
//
// GitHub exposes PullRequestStack as a GraphQL node, so this operation loads
// each unique stack once rather than repeating its entries for every member.
// See https://docs.github.com/en/graphql/reference/pulls#pullrequeststack.
func (c *Gateway) PullRequestsForStackUpdate(
	ctx context.Context,
	owner string,
	repo string,
	numbers []int,
) ([]*StackUpdatePullRequest, error) {
	if len(numbers) == 0 {
		return nil, nil
	}

	variables := make(map[string]any, len(numbers)+2)
	variables["owner"] = owner
	variables["repo"] = repo

	// Build one aliased repository selection per pull request number:
	//
	// query($owner:String!$repo:String!$pr0:Int!$pr1:Int!) {
	//   repository(owner: $owner, name: $repo) {
	//     pr0: pullRequest(number: $pr0) {
	//       state
	//       headRepository { owner { login } name }
	//       stack { id }
	//     }
	//     pr1: pullRequest(number: $pr1) {
	//       state
	//       headRepository { owner { login } name }
	//       stack { id }
	//     }
	//   }
	// }
	var variableDefinitions strings.Builder
	variableDefinitions.WriteString("$owner:String!,$repo:String!")
	var selections strings.Builder

	// Indexed aliases preserve one response slot for every input number,
	// including duplicate numbers and pull requests GitHub does not find.
	for i, number := range numbers {
		alias := "pr" + strconv.Itoa(i)
		fmt.Fprintf(&variableDefinitions, ",$%s:Int!", alias)
		if i > 0 {
			selections.WriteByte(',')
		}
		fmt.Fprintf(
			&selections,
			"%[1]s:pullRequest(number: $%[1]s){state,headRepository{owner{login},name},stack{id}}",
			alias,
		)
		variables[alias] = number
	}

	var result struct {
		Repository map[string]*struct {
			State          PullRequestState `json:"state"`
			HeadRepository struct {
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
				Name string `json:"name"`
			} `json:"headRepository"`
			Stack *struct {
				ID ID `json:"id"`
			} `json:"stack"`
		} `json:"repository"`
	}
	query := compactGraphQL(fmt.Sprintf(`
		query(%s){
			repository(owner: $owner, name: $repo){%s}
		}
	`, variableDefinitions.String(), selections.String()))
	if err := c.executeGQL(ctx, query, variables, &result); err != nil {
		return nil, fmt.Errorf("query pull requests for stack update: %w", err)
	}

	pullRequests := make([]*StackUpdatePullRequest, len(numbers))
	pullRequestsAwaitingStackByID := make(map[ID][]*StackUpdatePullRequest)
	var stackIDsToResolve []ID
	for i := range numbers {
		res := result.Repository["pr"+strconv.Itoa(i)]
		if res == nil {
			continue
		}

		pullRequest := &StackUpdatePullRequest{
			State:               res.State,
			HeadRepositoryOwner: res.HeadRepository.Owner.Login,
			HeadRepositoryName:  res.HeadRepository.Name,
		}
		pullRequests[i] = pullRequest

		if res.Stack == nil {
			continue
		}
		stackID := res.Stack.ID
		if _, seen := pullRequestsAwaitingStackByID[stackID]; !seen {
			stackIDsToResolve = append(stackIDsToResolve, stackID)
		}
		pullRequestsAwaitingStackByID[stackID] = append(
			pullRequestsAwaitingStackByID[stackID],
			pullRequest,
		)
	}
	if len(stackIDsToResolve) == 0 {
		return pullRequests, nil
	}

	// Resolve the ordered open members for every unique stack ID in one query.
	// A node may disappear after the pull request query; in that case, leaving
	// Stack nil preserves the best snapshot this non-atomic operation obtained.
	resolvedStacksByID, err := c.pullRequestStacksByID(ctx, stackIDsToResolve)
	if err != nil {
		return nil, err
	}
	for stackID, awaitingPullRequests := range pullRequestsAwaitingStackByID {
		resolvedStack, ok := resolvedStacksByID[stackID]
		if !ok {
			continue
		}
		for _, pullRequest := range awaitingPullRequests {
			pullRequest.Stack = resolvedStack
		}
	}
	return pullRequests, nil
}

// pullRequestStacksByID loads the ordered open members of each native stack.
// GitHub caps native stacks at 100 pull requests, so one entries page is the
// complete stack rather than a truncated projection.
func (c *Gateway) pullRequestStacksByID(
	ctx context.Context,
	stackIDs []ID,
) (map[ID]*PullRequestStack, error) {
	var result struct {
		Nodes []*struct {
			ID      ID  `json:"id"`
			Number  int `json:"number"`
			Entries struct {
				Nodes []struct {
					PullRequest *struct {
						Number int              `json:"number"`
						State  PullRequestState `json:"state"`
					} `json:"pullRequest"`
				} `json:"nodes"`
			} `json:"entries"`
		} `json:"nodes"`
	}
	query := compactGraphQL(`
		query($ids:[ID!]!){
			nodes(ids: $ids){
				... on PullRequestStack{
					id,number,
					entries(first: 100){nodes{pullRequest{number,state}}}
				}
			}
		}
	`)
	if err := c.executeGQL(ctx, query, struct {
		IDs []ID `json:"ids"`
	}{stackIDs}, &result); err != nil {
		return nil, fmt.Errorf("query pull request stacks: %w", err)
	}

	stacksByID := make(map[ID]*PullRequestStack, len(result.Nodes))
	for _, res := range result.Nodes {
		if res == nil {
			continue
		}
		stack := &PullRequestStack{Number: res.Number}
		for _, entry := range res.Entries.Nodes {
			if entry.PullRequest != nil && entry.PullRequest.State == PullRequestStateOpen {
				stack.OpenPullRequests = append(
					stack.OpenPullRequests,
					entry.PullRequest.Number,
				)
			}
		}
		stacksByID[res.ID] = stack
	}

	return stacksByID, nil
}

// CreatePullRequestStackInput identifies the pull requests for a new stack.
type CreatePullRequestStackInput struct {
	// Owner is the login that owns the repository.
	Owner string // required

	// Repo is the repository name.
	Repo string // required

	// PullRequests lists pull request numbers from the base upward.
	// GitHub accepts between 2 and 100 members.
	PullRequests []int // required
}

// CreatePullRequestStack creates a stack from pull requests ordered from the
// base upward.
// See https://docs.github.com/en/rest/pulls/stacks#create-a-pull-request-stack.
func (c *Gateway) CreatePullRequestStack(
	ctx context.Context,
	input *CreatePullRequestStackInput,
) error {
	if err := validateStackPullRequestCount(len(input.PullRequests), 2); err != nil {
		return err
	}

	req := struct {
		PullRequests []int `json:"pull_requests"`
	}{PullRequests: input.PullRequests}
	if err := c.postREST(
		ctx,
		[]string{"repos", input.Owner, input.Repo, "stacks"},
		&req,
		nil,
	); err != nil {
		return fmt.Errorf("create pull request stack: %w", err)
	}
	return nil
}

// AddPullRequestsToStackInput identifies pull requests to add to an existing
// stack.
type AddPullRequestsToStackInput struct {
	// Owner is the login that owns the repository.
	Owner string // required

	// Repo is the repository name.
	Repo string // required

	// StackNumber identifies the stack within the repository.
	StackNumber int // required

	// PullRequests lists pull request numbers from the current top upward.
	// GitHub accepts between 1 and 100 members.
	PullRequests []int // required
}

// AddPullRequestsToStack adds pull requests above an existing stack.
// See https://docs.github.com/en/rest/pulls/stacks#add-pull-requests-to-a-pull-request-stack.
func (c *Gateway) AddPullRequestsToStack(
	ctx context.Context,
	input *AddPullRequestsToStackInput,
) error {
	if err := validateStackPullRequestCount(len(input.PullRequests), 1); err != nil {
		return err
	}

	req := struct {
		PullRequests []int `json:"pull_requests"`
	}{PullRequests: input.PullRequests}
	if err := c.postREST(
		ctx,
		[]string{
			"repos",
			input.Owner,
			input.Repo,
			"stacks",
			strconv.Itoa(input.StackNumber),
			"add",
		},
		&req,
		nil,
	); err != nil {
		return fmt.Errorf("add pull requests to stack: %w", err)
	}
	return nil
}

func validateStackPullRequestCount(count, minimum int) error {
	if count < minimum || count > maxStackPullRequests {
		return fmt.Errorf(
			"pull request count must be between %d and %d: %d",
			minimum,
			maxStackPullRequests,
			count,
		)
	}
	return nil
}
