package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.abhg.dev/gs/internal/spice"
	"go.abhg.dev/gs/internal/spice/spicetest"
)

func TestSelectStackBranches(t *testing.T) {
	t.Run("Linear", func(t *testing.T) {
		graph := spicetest.NewBranchGraph(t, spicetest.BranchGraphConfig{
			Trunk: "main",
			Branches: []spice.LoadBranchItem{
				{Name: "a", Base: "main"},
				{Name: "b", Base: "a"},
				{Name: "c", Base: "b"},
			},
		})

		branches, err := selectStackBranches(graph, "c", "main")
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b", "c"}, branches)
	})

	t.Run("NonLinear", func(t *testing.T) {
		graph := spicetest.NewBranchGraph(t, spicetest.BranchGraphConfig{
			Trunk: "main",
			Branches: []spice.LoadBranchItem{
				{Name: "a", Base: "main"},
				{Name: "b", Base: "a"},
				{Name: "c", Base: "a"},
				{Name: "d", Base: "b"},
			},
		})

		_, err := selectStackBranches(graph, "a", "main")
		require.Error(t, err)
		assert.ErrorContains(t, err, "cannot submit nonlinear stack")
		assert.ErrorContains(t, err, "a has 2 branches above it")
	})

	t.Run("LeafInFork", func(t *testing.T) {
		graph := spicetest.NewBranchGraph(t, spicetest.BranchGraphConfig{
			Trunk: "main",
			Branches: []spice.LoadBranchItem{
				{Name: "a", Base: "main"},
				{Name: "b", Base: "a"},
				{Name: "c", Base: "a"},
				{Name: "d", Base: "b"},
			},
		})

		_, err := selectStackBranches(graph, "d", "main")
		require.Error(t, err)
		assert.ErrorContains(t, err, "cannot submit nonlinear stack")
		assert.ErrorContains(t, err, "a has 2 branches above it")
	})
}
