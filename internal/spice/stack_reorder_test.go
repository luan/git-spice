package spice

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/git"
	"go.abhg.dev/gs/internal/git/gittest"
	"go.abhg.dev/gs/internal/silog/silogtest"
	"go.abhg.dev/gs/internal/spice/state"
	"go.abhg.dev/gs/internal/text"
)

func TestService_StackReorder(t *testing.T) {
	fixture, err := gittest.LoadFixtureScript([]byte(text.Dedent(`
		git init
		git config user.name Test
		git config user.email test@example.com
		git config commit.gpgSign false
		git commit --allow-empty -m 'Initial commit'

		git checkout -b a
		git add a.txt
		git commit -m 'Add A'
		git checkout -b b
		git add b.txt
		git commit -m 'Add B'
		git checkout -b c
		git add c.txt
		git commit -m 'Add C'

		-- a.txt --
		a
		-- b.txt --
		b
		-- c.txt --
		c
	`)))
	require.NoError(t, err)
	t.Cleanup(fixture.Cleanup)

	ctx := t.Context()
	wt, err := git.OpenWorktree(ctx, fixture.Dir(), git.OpenOptions{Log: silogtest.New(t)})
	require.NoError(t, err)
	store := NewMemoryStore(t)
	tx := store.BeginBranchTx()
	for _, req := range []state.UpsertRequest{
		{Name: "a", Base: "main", BaseHash: "main"},
		{Name: "b", Base: "a", BaseHash: "a"},
		{Name: "c", Base: "b", BaseHash: "b"},
	} {
		require.NoError(t, tx.Upsert(ctx, req))
	}
	require.NoError(t, tx.Commit(ctx, "setup"))

	svc := NewTestService(wt.Repository(), wt, store, nil, silogtest.New(t))
	result, err := svc.StackReorder(ctx, &StackReorderRequest{Stack: []string{"c", "a", "b"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"c", "a", "b"}, result.Stack)

	for branch, base := range map[string]string{"c": "main", "a": "c", "b": "a"} {
		tracked, err := svc.LookupBranch(ctx, branch)
		require.NoError(t, err)
		assert.Equal(t, base, tracked.Base)
	}

	for _, want := range []struct {
		branch  string
		base    string
		subject string
	}{
		{branch: "c", base: "main", subject: "Add C"},
		{branch: "a", base: "c", subject: "Add A"},
		{branch: "b", base: "a", subject: "Add B"},
	} {
		messages, err := wt.Repository().CommitMessageRange(ctx, want.branch, want.base)
		require.NoError(t, err)
		require.Len(t, messages, 1)
		assert.Equal(t, want.subject, messages[0].Subject)
	}
}

func TestService_StackReorder_rejectsIncompleteOrDuplicateStack(t *testing.T) {
	assert.False(t, sameBranches([]string{"a", "b", "c"}, []string{"a", "b"}))
	assert.False(t, sameBranches([]string{"a", "b", "c"}, []string{"a", "b", "b"}))
	assert.False(t, sameBranches([]string{"a", "b", "c"}, []string{"a", "b", "d"}))
	assert.True(t, sameBranches([]string{"a", "b", "c"}, []string{"c", "a", "b"}))
}
