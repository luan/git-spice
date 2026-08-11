package git_test

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/git"
	"go.uber.org/mock/gomock"
)

func TestWorktree_Absorb(t *testing.T) {
	t.Parallel()

	t.Run("defaults", func(t *testing.T) {
		mockExecer := git.NewMockExecer(gomock.NewController(t))
		_, wt := git.NewFakeRepository(t, "", mockExecer)

		mockExecer.EXPECT().
			Run(gomock.Any()).
			DoAndReturn(func(cmd *exec.Cmd) error {
				assert.Equal(t, []string{"git", "absorb", "--base", "base", "--and-rebase"}, cmd.Args)
				assert.Same(t, os.Stdout, cmd.Stdout)
				assert.Contains(t, cmd.Env, "GIT_SEQUENCE_EDITOR=true")
				return nil
			})

		require.NoError(t, wt.Absorb(t.Context(), git.AbsorbRequest{Base: "base"}))
	})

	t.Run("allOptions", func(t *testing.T) {
		mockExecer := git.NewMockExecer(gomock.NewController(t))
		_, wt := git.NewFakeRepository(t, "", mockExecer)

		mockExecer.EXPECT().
			Run(gomock.Any()).
			DoAndReturn(func(cmd *exec.Cmd) error {
				assert.Equal(t, []string{
					"git", "absorb", "--base", "base", "--dry-run", "--force-author",
					"--force", "--whole-file", "--one-fixup-per-commit", "--squash",
					"--message", "why",
				}, cmd.Args)
				return nil
			})

		require.NoError(t, wt.Absorb(t.Context(), git.AbsorbRequest{
			Base:              "base",
			DryRun:            true,
			ForceAuthor:       true,
			Force:             true,
			WholeFile:         true,
			OneFixupPerCommit: true,
			Squash:            true,
			Message:           "why",
		}))
	})

	t.Run("requiresBase", func(t *testing.T) {
		_, wt := git.NewFakeRepository(t, "", git.NewMockExecer(gomock.NewController(t)))
		assert.ErrorContains(t, wt.Absorb(t.Context(), git.AbsorbRequest{}), "base is required")
	})
}
