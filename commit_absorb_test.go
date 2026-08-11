package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.abhg.dev/gs/internal/handler/absorb"
	"go.abhg.dev/gs/internal/spice"
)

func TestCommitAbsorbContinueCommand(t *testing.T) {
	cmd := commitAbsorbCmd{Options: absorb.Options{
		Restack:           spice.AutoRestackNone,
		DryRun:            true,
		ForceAuthor:       true,
		Force:             true,
		WholeFile:         true,
		OneFixupPerCommit: true,
		Squash:            true,
		Message:           "why",
	}}

	assert.Equal(t, []string{
		"commit", "absorb",
		"--no-restack",
		"--dry-run",
		"--force-author",
		"--force",
		"--whole-file",
		"--one-fixup-per-commit",
		"--squash",
		"--message", "why",
	}, cmd.continueCommand())
}
