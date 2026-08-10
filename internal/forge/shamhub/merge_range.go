package shamhub

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.abhg.dev/gs/internal/forge"
)

type mergeRangeChange struct {
	Number   int    `json:"number"`
	Base     string `json:"base"`
	Head     string `json:"head"`
	HeadHash string `json:"headHash"`
}

type mergeRangeRequest struct {
	Owner string `path:"owner" json:"-"`
	Repo  string `path:"repo" json:"-"`

	Changes     []mergeRangeChange `json:"changes"`
	MergeMethod string             `json:"mergeMethod,omitempty"`
}

type mergeRangeResponse struct{}

var _ = shamhubRESTHandler(
	"POST /{owner}/{repo}/change/merge-range",
	(*ShamHub).handleMergeRange,
)

func (sh *ShamHub) handleMergeRange(
	ctx context.Context,
	req *mergeRangeRequest,
) (*mergeRangeResponse, error) {
	method := MergeMethod(req.MergeMethod)
	if method == "" {
		sh.mu.RLock()
		method = sh.defaultMergeMethod
		sh.mu.RUnlock()
	} else if _, err := parseMergeMethod(string(method)); err != nil {
		return nil, badRequestErrorf("%s", err)
	}

	if err := sh.mergeRange(ctx, req.Owner, req.Repo, req.Changes, method); err != nil {
		return nil, err
	}
	return &mergeRangeResponse{}, nil
}

// preparedMergeRange holds validated server state used to construct and
// publish one atomic range merge.
type preparedMergeRange struct {
	// rootBaseHash is the target branch value used by the publishing CAS.
	rootBaseHash string

	// changes retains the validated server records in bottom-to-top order.
	changes []preparedMergeRangeChange
}

// preparedMergeRangeChange pairs a validated change snapshot with the mutable
// entry updated only after atomic publication succeeds.
type preparedMergeRangeChange struct {
	// index is the snapshot's position in ShamHub.changes.
	index int

	// change is the validated snapshot used to construct the merge result.
	change shamChange

	// headHash is the resolved head value validated against the request.
	headHash string
}

// mergeRange validates and builds the result before one compare-and-swap
// publishes it. Commit construction may leave unreachable objects on failure,
// but the root ref and in-memory change states remain unchanged. Holding mu
// across the ref update and state transition makes those observable effects
// one operation to ShamHub clients.
func (sh *ShamHub) mergeRange(
	ctx context.Context,
	owner string,
	repo string,
	changes []mergeRangeChange,
	method MergeMethod,
) error {
	if owner == "" || repo == "" {
		return errors.New("owner and repo are required")
	}
	if len(changes) == 0 {
		return errors.New("changes must not be empty")
	}

	sh.mu.Lock()
	defer sh.mu.Unlock()

	prepared, err := sh.prepareMergeRange(ctx, owner, repo, changes)
	if err != nil {
		return err
	}

	commit, err := sh.buildMergeRangeCommit(ctx, owner, repo, method, prepared)
	if err != nil {
		return err
	}

	rootRef := "refs/heads/" + prepared.changes[0].change.Base.Name
	if err := sh.gitCmd(
		ctx,
		owner,
		repo,
		"update-ref",
		rootRef,
		commit,
		prepared.rootBaseHash,
	).Run(); err != nil {
		return fmt.Errorf("update root ref: %w", err)
	}

	for _, change := range prepared.changes {
		sh.changes[change.index].State = shamChangeMerged
		sh.changes[change.index].HeadHash = change.headHash
	}
	return nil
}

func (sh *ShamHub) prepareMergeRange(
	ctx context.Context,
	owner string,
	repo string,
	requested []mergeRangeChange,
) (preparedMergeRange, error) {
	byNumber := make(map[int]int, len(requested))
	for i, change := range sh.changes {
		if change.Base.Owner == owner && change.Base.Repo == repo {
			byNumber[change.Number] = i
		}
	}

	prepared := preparedMergeRange{
		changes: make([]preparedMergeRangeChange, len(requested)),
	}
	for i, expected := range requested {
		changeIndex, ok := byNumber[expected.Number]
		if !ok {
			return preparedMergeRange{}, fmt.Errorf(
				"change %d (%s/%s) not found", expected.Number, owner, repo,
			)
		}
		change := sh.changes[changeIndex]
		if change.State != shamChangeOpen {
			return preparedMergeRange{}, fmt.Errorf(
				"change %d is not open", expected.Number,
			)
		}
		if change.Draft {
			return preparedMergeRange{}, fmt.Errorf(
				"change %d is a draft", expected.Number,
			)
		}
		if change.Base.Name != expected.Base {
			return preparedMergeRange{}, fmt.Errorf(
				"change %d base branch is %q, expected %q",
				expected.Number, change.Base.Name, expected.Base,
			)
		}
		if change.Head.Name != expected.Head {
			return preparedMergeRange{}, fmt.Errorf(
				"change %d head branch is %q, expected %q",
				expected.Number, change.Head.Name, expected.Head,
			)
		}
		if expected.HeadHash == "" {
			return preparedMergeRange{}, fmt.Errorf(
				"change %d head hash is required", expected.Number,
			)
		}
		if i > 0 && expected.Base != requested[i-1].Head {
			return preparedMergeRange{}, fmt.Errorf(
				"change %d base branch is %q, expected prior head %q",
				expected.Number, expected.Base, requested[i-1].Head,
			)
		}

		baseHash, err := sh.gitCmd(
			ctx,
			owner,
			repo,
			"rev-parse",
			"refs/heads/"+change.Base.Name+"^{commit}",
		).OutputChomp()
		if err != nil {
			return preparedMergeRange{}, fmt.Errorf(
				"resolve change %d base: %w", expected.Number, err,
			)
		}
		headHash, err := sh.gitCmd(
			ctx,
			change.Head.Owner,
			change.Head.Repo,
			"rev-parse",
			"refs/heads/"+change.Head.Name+"^{commit}",
		).OutputChomp()
		if err != nil {
			return preparedMergeRange{}, fmt.Errorf(
				"resolve change %d head: %w", expected.Number, err,
			)
		}
		if headHash != expected.HeadHash {
			return preparedMergeRange{}, fmt.Errorf(
				"change %d head hash mismatch: expected %q, got %q",
				expected.Number, expected.HeadHash, headHash,
			)
		}

		// Commit construction runs in the receiving repository.
		// After validating a fork head in its source repository,
		// import its objects into the receiving repository.
		if change.Head.Owner != owner || change.Head.Repo != repo {
			if err := sh.gitCmd(
				ctx,
				owner,
				repo,
				"fetch",
				"--no-write-fetch-head",
				sh.repoDir(change.Head.Owner, change.Head.Repo),
				change.Head.Name,
			).Run(); err != nil {
				return preparedMergeRange{}, fmt.Errorf(
					"fetch change %d head objects: %w", expected.Number, err,
				)
			}
		}

		// Branch-name alignment is insufficient for an atomic range merge.
		// Each base ref must resolve to the previous validated head commit.
		if i > 0 && baseHash != prepared.changes[i-1].headHash {
			return preparedMergeRange{}, fmt.Errorf(
				"change %d base hash is %q, expected prior head %q",
				expected.Number, baseHash, prepared.changes[i-1].headHash,
			)
		}
		if err := sh.gitCmd(
			ctx,
			owner,
			repo,
			"merge-base",
			"--is-ancestor",
			baseHash,
			headHash,
		).Run(); err != nil {
			return preparedMergeRange{}, fmt.Errorf(
				"change %d head is not based on %q: %w",
				expected.Number, expected.Base, err,
			)
		}

		if i == 0 {
			prepared.rootBaseHash = baseHash
		}
		prepared.changes[i] = preparedMergeRangeChange{
			index:    changeIndex,
			change:   change,
			headHash: headHash,
		}
	}
	return prepared, nil
}

func (sh *ShamHub) buildMergeRangeCommit(
	ctx context.Context,
	owner string,
	repo string,
	method MergeMethod,
	prepared preparedMergeRange,
) (string, error) {
	switch method {
	case MergeMethodMerge:
		top := prepared.changes[len(prepared.changes)-1]
		tree, err := sh.gitCmd(
			ctx,
			owner,
			repo,
			"merge-tree",
			"--write-tree",
			prepared.rootBaseHash,
			top.headHash,
		).OutputChomp()
		if err != nil {
			return "", fmt.Errorf("merge range trees: %w", err)
		}
		message := fmt.Sprintf(
			"Merge changes #%d through #%d",
			prepared.changes[0].change.Number,
			top.change.Number,
		)
		return sh.commitRangeTree(
			ctx,
			owner,
			repo,
			tree,
			[]string{prepared.rootBaseHash, top.headHash},
			message,
			top.headHash,
		)

	case MergeMethodSquash:
		// Every stacked head tree contains the changes below it. Re-parenting
		// those trees in range order produces one squashed commit per change
		// without reconstructing or replaying individual patches.
		parent := prepared.rootBaseHash
		for _, change := range prepared.changes {
			tree, err := sh.gitCmd(
				ctx,
				owner,
				repo,
				"rev-parse",
				change.headHash+"^{tree}",
			).OutputChomp()
			if err != nil {
				return "", fmt.Errorf(
					"resolve change %d tree: %w", change.change.Number, err,
				)
			}
			message := fmt.Sprintf(
				"%s (#%d)\n\n%s",
				change.change.Subject,
				change.change.Number,
				change.change.Body,
			)
			parent, err = sh.commitRangeTree(
				ctx,
				owner,
				repo,
				tree,
				[]string{parent},
				message,
				change.headHash,
			)
			if err != nil {
				return "", err
			}
		}
		return parent, nil

	default:
		return "", fmt.Errorf("unsupported merge method %q", method)
	}
}

func (sh *ShamHub) commitRangeTree(
	ctx context.Context,
	owner string,
	repo string,
	tree string,
	parents []string,
	message string,
	timeSource string,
) (string, error) {
	commitTimeText, err := sh.gitCmd(
		ctx,
		owner,
		repo,
		"log",
		"-1",
		"--format=%cI",
		timeSource,
	).OutputChomp()
	if err != nil {
		return "", fmt.Errorf("read commit time: %w", err)
	}
	commitTime, err := time.Parse(time.RFC3339, commitTimeText)
	if err != nil {
		return "", fmt.Errorf("parse commit time: %w", err)
	}

	args := []string{"commit-tree"}
	for _, parent := range parents {
		args = append(args, "-p", parent)
	}
	args = append(args, "-m", message, tree)
	commit, err := sh.gitCmd(ctx, owner, repo, args...).
		AppendEnv(
			"GIT_COMMITTER_NAME=ShamHub",
			"GIT_COMMITTER_EMAIL=shamhub@example.com",
			"GIT_AUTHOR_NAME=ShamHub",
			"GIT_AUTHOR_EMAIL=shamhub@example.com",
			"GIT_COMMITTER_DATE="+commitTime.Format(time.RFC3339),
			"GIT_AUTHOR_DATE="+commitTime.Format(time.RFC3339),
		).
		OutputChomp()
	if err != nil {
		return "", fmt.Errorf("create merge commit: %w", err)
	}
	return commit, nil
}

var _ forge.WithMergeRange = (*stackRepository)(nil)

// MergeRange asks ShamHub to atomically merge an aligned bottom-to-top range.
func (r *stackRepository) MergeRange(
	ctx context.Context,
	request forge.MergeRangeRequest,
) (forge.MergeOperation, error) {
	req := mergeRangeRequest{
		Changes: make([]mergeRangeChange, len(request.Changes)),
	}
	for i, change := range request.Changes {
		req.Changes[i] = mergeRangeChange{
			Number:   int(change.Change.(ChangeID)),
			Base:     change.Base,
			Head:     change.Head,
			HeadHash: change.HeadHash.String(),
		}
	}
	switch request.Method {
	case forge.MergeMethodMerge, forge.MergeMethodSquash:
		req.MergeMethod = request.Method.String()
	case forge.MergeMethodDefault:
	default:
		r.log.Warn(
			"Unsupported merge method; using forge default",
			"method", request.Method,
		)
	}

	var res mergeRangeResponse
	if err := r.client.Post(
		ctx,
		r.apiURL.JoinPath(r.owner, r.repo, "change", "merge-range").String(),
		req,
		&res,
	); err != nil {
		return nil, fmt.Errorf("merge range: %w", err)
	}
	return nil, nil
}
