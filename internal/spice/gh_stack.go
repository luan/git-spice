package spice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.abhg.dev/gs/internal/git"
	"go.abhg.dev/gs/internal/spice/state"
)

type ghStackState struct {
	SchemaVersion int       `json:"schemaVersion"`
	Repository    string    `json:"repository,omitempty"`
	Stacks        []ghStack `json:"stacks"`
}

type ghStack struct {
	ID       string          `json:"id,omitempty"`
	Number   int             `json:"number,omitempty"`
	Trunk    ghStackBranch   `json:"trunk"`
	Branches []ghStackBranch `json:"branches"`
}

type ghStackBranch struct {
	Branch string     `json:"branch"`
	Head   string     `json:"head,omitempty"`
	Base   string     `json:"base,omitempty"`
	PR     *ghStackPR `json:"pullRequest,omitempty"`
}

type ghStackPR struct {
	Number int    `json:"number"`
	ID     string `json:"id,omitempty"`
	URL    string `json:"url,omitempty"`
	Merged bool   `json:"merged,omitempty"`
}

func (s *Service) reconcileGHStack(ctx context.Context) error {
	repo, ok := s.repo.(interface{ GitDir() string })
	if !ok {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(repo.GitDir(), "gh-stack"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read gh-stack state: %w", err)
	}

	var file ghStackState
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("parse gh-stack state: %w", err)
	}
	if file.SchemaVersion != 1 {
		return fmt.Errorf("unsupported gh-stack state schema version %d", file.SchemaVersion)
	}

	current, err := s.wt.CurrentBranch(ctx)
	if err != nil {
		return nil
	}
	stackIndex := -1
	for i, stack := range file.Stacks {
		for _, branch := range stack.Branches {
			if branch.Branch != current {
				continue
			}
			if stackIndex >= 0 {
				return fmt.Errorf("current branch %q belongs to multiple gh-stacks", current)
			}
			stackIndex = i
			break
		}
	}
	if stackIndex < 0 {
		if _, err := s.store.LookupBranch(ctx, current); err == nil {
			return fmt.Errorf(
				"current branch %q is tracked by Git-Spice but absent from gh-stack state",
				current,
			)
		} else if !errors.Is(err, state.ErrNotExist) {
			return fmt.Errorf("load current branch %q: %w", current, err)
		}
		return nil
	}

	stack := file.Stacks[stackIndex]
	if stack.Trunk.Branch != s.store.Trunk() {
		return fmt.Errorf(
			"gh-stack trunk %q does not match Git-Spice trunk %q",
			stack.Trunk.Branch,
			s.store.Trunk(),
		)
	}

	tx := s.store.BeginBranchTx()
	base := s.store.Trunk()
	changed := false
	seen := make(map[string]struct{}, len(stack.Branches))
	for _, branch := range stack.Branches {
		if branch.Branch == "" {
			return errors.New("gh-stack branch name is empty")
		}
		if _, ok := seen[branch.Branch]; ok {
			return fmt.Errorf("gh-stack branch %q appears more than once", branch.Branch)
		}
		seen[branch.Branch] = struct{}{}
		if _, err := s.repo.PeelToCommit(ctx, branch.Branch); err != nil {
			return fmt.Errorf("resolve gh-stack branch %q: %w", branch.Branch, err)
		}
		if branch.Base != "" {
			if _, err := s.repo.PeelToCommit(ctx, branch.Base); err != nil {
				return fmt.Errorf("resolve gh-stack base for branch %q: %w", branch.Branch, err)
			}
		}

		req := state.UpsertRequest{
			Name:     branch.Branch,
			Base:     base,
			BaseHash: git.Hash(branch.Base),
		}
		if branch.PR != nil && branch.PR.Number > 0 && branch.PR.ID != "" {
			metadata, err := json.Marshal(map[string]any{
				"pr": map[string]any{
					"number": branch.PR.Number,
					"gqlID":  branch.PR.ID,
				},
			})
			if err != nil {
				return fmt.Errorf("encode gh-stack pull request: %w", err)
			}
			req.ChangeForge = "github"
			req.ChangeMetadata = metadata
			upstream := branch.Branch
			req.UpstreamBranch = &upstream
		}

		existing, err := s.store.LookupBranch(ctx, branch.Branch)
		if err == nil && existing.Base == req.Base &&
			(req.BaseHash == "" || existing.BaseHash == req.BaseHash) &&
			(len(req.ChangeMetadata) == 0 ||
				(existing.ChangeForge == req.ChangeForge && bytes.Equal(existing.ChangeMetadata, req.ChangeMetadata))) &&
			(req.UpstreamBranch == nil || existing.UpstreamBranch == *req.UpstreamBranch) {
			base = branch.Branch
			continue
		}
		if err != nil && !errors.Is(err, state.ErrNotExist) {
			return fmt.Errorf("load gh-stack branch %q: %w", branch.Branch, err)
		}
		if err := tx.Upsert(ctx, req); err != nil {
			return fmt.Errorf("track gh-stack branch %q: %w", branch.Branch, err)
		}
		changed = true
		base = branch.Branch
	}
	if !changed {
		return nil
	}
	if err := tx.Commit(ctx, "reconcile gh-stack local state"); err != nil {
		return fmt.Errorf("save gh-stack state: %w", err)
	}
	return nil
}

func (s *Service) syncGHStack(ctx context.Context) error {
	s.ghStackMu.Lock()
	if s.syncingGHStack {
		s.ghStackMu.Unlock()
		return nil
	}
	s.syncingGHStack = true
	s.ghStackMu.Unlock()
	defer func() {
		s.ghStackMu.Lock()
		s.syncingGHStack = false
		s.ghStackMu.Unlock()
	}()

	repo, ok := s.repo.(interface{ GitDir() string })
	if !ok {
		return nil
	}
	path := filepath.Join(repo.GitDir(), "gh-stack")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read gh-stack state: %w", err)
	}

	var file ghStackState
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("parse gh-stack state: %w", err)
	}
	if file.SchemaVersion != 1 {
		return fmt.Errorf("unsupported gh-stack state schema version %d", file.SchemaVersion)
	}

	branches := make(map[string]*state.LookupResponse)
	for name, err := range s.store.ListBranches(ctx) {
		if err != nil {
			return fmt.Errorf("list Git-Spice branches: %w", err)
		}
		branch, err := s.store.LookupBranch(ctx, name)
		if err != nil {
			return fmt.Errorf("load Git-Spice branch %q: %w", name, err)
		}
		branches[name] = branch
	}

	current, err := s.wt.CurrentBranch(ctx)
	if err != nil {
		return nil
	}
	if current == s.store.Trunk() || branches[current] == nil {
		kept := file.Stacks[:0]
		for _, stack := range file.Stacks {
			alive := false
			for _, branch := range stack.Branches {
				alive = alive || branches[branch.Branch] != nil
			}
			if alive {
				kept = append(kept, stack)
			}
		}
		if len(kept) == len(file.Stacks) {
			return nil
		}
		file.Stacks = kept
		return writeGHStack(path, &file, data)
	}

	desired := make(map[string]struct{})
	for branch := current; branch != s.store.Trunk(); {
		if _, duplicate := desired[branch]; duplicate {
			return fmt.Errorf("cycle in Git-Spice stack at branch %q", branch)
		}
		desired[branch] = struct{}{}
		state := branches[branch]
		if state == nil {
			return fmt.Errorf("Git-Spice stack references untracked base %q", branch)
		}
		branch = state.Base
	}

	stackIndex := -1
	for i, stack := range file.Stacks {
		matches := false
		for _, branch := range stack.Branches {
			_, matches = desired[branch.Branch]
			if matches {
				break
			}
		}
		if !matches {
			continue
		}
		if stackIndex >= 0 {
			return errors.New("Git-Spice stack overlaps multiple gh-stacks")
		}
		stackIndex = i
	}
	if stackIndex < 0 {
		file.Stacks = append(file.Stacks, ghStack{
			Trunk: ghStackBranch{Branch: s.store.Trunk()},
		})
		stackIndex = len(file.Stacks) - 1
	}

	stack := &file.Stacks[stackIndex]
	oldBranches := make(map[string]ghStackBranch, len(stack.Branches))
	for _, branch := range stack.Branches {
		oldBranches[branch.Branch] = branch
		if branches[branch.Branch] != nil {
			desired[branch.Branch] = struct{}{}
		}
	}

	ordered := make([]string, 0, len(desired))
	base := s.store.Trunk()
	for len(ordered) < len(desired) {
		next := ""
		for name := range desired {
			if branches[name].Base != base {
				continue
			}
			if next != "" {
				return fmt.Errorf("Git-Spice stack is not linear above %q", base)
			}
			next = name
		}
		if next == "" {
			return fmt.Errorf("Git-Spice stack cannot be ordered above %q", base)
		}
		ordered = append(ordered, next)
		base = next
	}

	trunkHead, err := s.repo.PeelToCommit(ctx, s.store.Trunk())
	if err != nil {
		return fmt.Errorf("resolve Git-Spice trunk: %w", err)
	}
	stack.Trunk.Branch = s.store.Trunk()
	stack.Trunk.Head = trunkHead.String()
	stack.Branches = make([]ghStackBranch, 0, len(ordered))
	for _, name := range ordered {
		ref := oldBranches[name]
		ref.Branch = name
		ref.Base = branches[name].BaseHash.String()
		if ref.PR == nil && branches[name].ChangeForge == "github" {
			var metadata struct {
				PR *ghStackPR `json:"pr"`
			}
			if err := json.Unmarshal(branches[name].ChangeMetadata, &metadata); err != nil {
				return fmt.Errorf("decode GitHub identity for branch %q: %w", name, err)
			}
			ref.PR = metadata.PR
		}
		stack.Branches = append(stack.Branches, ref)
	}
	return writeGHStack(path, &file, data)
}

func writeGHStack(path string, file *ghStackState, original []byte) error {
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode gh-stack state: %w", err)
	}
	data = append(data, '\n')

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat gh-stack state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gh-stack-*")
	if err != nil {
		return fmt.Errorf("create gh-stack state: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set gh-stack state permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write gh-stack state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close gh-stack state: %w", err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("verify gh-stack state: %w", err)
	}
	if !bytes.Equal(current, original) {
		return errors.New("gh-stack state changed during Git-Spice update")
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace gh-stack state: %w", err)
	}
	return nil
}
