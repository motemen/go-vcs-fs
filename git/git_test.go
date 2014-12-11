package git

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/godoc/vfs"
)

var _ = vfs.FileSystem((*Repository)(nil))

func TestStat_dir(t *testing.T) {
	repo := Repository{}

	fi, err := repo.Stat("git")
	require.NoError(t, err)

	assert.True(t, fi.IsDir())
	assert.Equal(t, "git", fi.Name())
}

func TestStat_file(t *testing.T) {
	repo := Repository{}

	fi, err := repo.Stat("git/git.go")
	require.NoError(t, err)

	assert.False(t, fi.IsDir())
	assert.Equal(t, "git.go", fi.Name())

	assert.Equal(t, "git", fi.(*treeEntry).parent)
}

func TestReadDir(t *testing.T) {
	repo := Repository{}

	files, err := repo.ReadDir("git")
	require.NoError(t, err)

	assert.Len(t, files, 2)
}

func TestOpen(t *testing.T) {
	repo := Repository{}

	_, err := repo.Open("git/git.go")
	require.NoError(t, err)
}
