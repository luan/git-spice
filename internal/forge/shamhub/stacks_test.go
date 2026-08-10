package shamhub

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/forge"
)

func TestStackRepository_UpdateStack(t *testing.T) {
	sh, repo := newMergeabilityTestRepository(t)
	seedStackChanges(sh)
	stacks := &stackRepository{forgeRepository: repo}

	require.NoError(t, stacks.UpdateStack(t.Context(), []forge.StackChange{
		{Change: ChangeID(3), Base: ChangeID(1)},
		{Change: ChangeID(2), Base: ChangeID(1)},
		{Change: ChangeID(1)},
	}))

	assert.Equal(t, map[int]int{1: 0, 2: 1, 3: 1},
		sh.stackBases[repoID{Owner: "alice", Name: "example"}])
}

func TestStackRepository_UpdateStack_replacesTouchedComponent(t *testing.T) {
	sh, repo := newMergeabilityTestRepository(t)
	seedStackChanges(sh)
	stacks := &stackRepository{forgeRepository: repo}

	require.NoError(t, stacks.UpdateStack(t.Context(), []forge.StackChange{
		{Change: ChangeID(1)},
		{Change: ChangeID(2), Base: ChangeID(1)},
		{Change: ChangeID(4), Base: ChangeID(2)},
	}))
	require.NoError(t, stacks.UpdateStack(t.Context(), []forge.StackChange{
		{Change: ChangeID(2)},
		{Change: ChangeID(4), Base: ChangeID(2)},
	}))

	assert.Equal(t, map[int]int{2: 0, 4: 2},
		sh.stackBases[repoID{Owner: "alice", Name: "example"}])
}

func TestShamHub_UpdateStack_repositoryIsolation(t *testing.T) {
	sh, _ := newMergeabilityTestRepository(t)
	seedStackChanges(sh)
	sh.changes = append(sh.changes,
		shamChange{
			Number: 1,
			Base:   &shamBranch{Owner: "bob", Repo: "other", Name: "main"},
			Head:   &shamBranch{Owner: "bob", Repo: "other", Name: "bottom"},
		},
		shamChange{
			Number: 2,
			Base:   &shamBranch{Owner: "bob", Repo: "other", Name: "bottom"},
			Head:   &shamBranch{Owner: "bob", Repo: "other", Name: "top"},
		},
	)

	require.NoError(t, sh.updateStack("alice", "example", []stackChange{
		{Number: 1},
		{Number: 2, Base: 1},
	}))
	require.NoError(t, sh.updateStack("bob", "other", []stackChange{
		{Number: 1},
		{Number: 2, Base: 1},
	}))

	assert.Equal(t, map[int]int{1: 0, 2: 1},
		sh.stackBases[repoID{Owner: "alice", Name: "example"}])
	assert.Equal(t, map[int]int{1: 0, 2: 1},
		sh.stackBases[repoID{Owner: "bob", Name: "other"}])
}

func seedStackChanges(sh *ShamHub) {
	sh.changes = append(sh.changes,
		shamChange{
			Number: 1,
			Base:   &shamBranch{Owner: "alice", Repo: "example", Name: "main"},
			Head:   &shamBranch{Owner: "alice", Repo: "example", Name: "bottom"},
		},
		shamChange{
			Number: 2,
			Base:   &shamBranch{Owner: "alice", Repo: "example", Name: "bottom"},
			Head:   &shamBranch{Owner: "alice", Repo: "example", Name: "left"},
		},
		shamChange{
			Number: 3,
			Base:   &shamBranch{Owner: "alice", Repo: "example", Name: "bottom"},
			Head:   &shamBranch{Owner: "alice", Repo: "example", Name: "right"},
		},
		shamChange{
			Number: 4,
			Base:   &shamBranch{Owner: "alice", Repo: "example", Name: "left"},
			Head:   &shamBranch{Owner: "alice", Repo: "example", Name: "leaf"},
		},
	)
}
