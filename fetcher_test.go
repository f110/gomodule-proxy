package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/vcs"
)

func TestModuleRoot(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/f110/gomodule-proxy-test"), 0644)
	require.NoError(t, err)
	_, err = wt.Add("go.mod")
	require.NoError(t, err)
	err = os.MkdirAll(filepath.Join(dir, "pkg/api"), 0755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(dir, "pkg/api/go.mod"), []byte("module github.com/f110/gomodule-proxy-test/pkg/api"), 0644)
	require.NoError(t, err)
	commitHash, err := wt.Commit("init", &git.CommitOptions{
		Author: &object.Signature{
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	require.NoError(t, err)
	_, err = repo.CreateTag("v1.0.0", commitHash, &git.CreateTagOptions{
		Tagger: &object.Signature{
			Email: "test@example.com",
			When:  time.Now(),
		},
		Message: "v1.0.0",
	})
	require.NoError(t, err)
	_, err = repo.CreateTag("pkg/api/v1.5.0", commitHash, &git.CreateTagOptions{
		Tagger: &object.Signature{
			Email: "test@example.com",
			When:  time.Now(),
		},
		Message: "pkg/api/v1.5.0",
	})
	require.NoError(t, err)

	repoRoot := &vcs.RepoRoot{
		VCS:  vcs.ByCmd("git"),
		Root: "github.com/f110/gomodule-proxy-test",
	}
	moduleRoot := &ModuleRoot{
		dir:      dir,
		repoRoot: repoRoot,
	}
	modules, err := moduleRoot.findModules()
	require.NoError(t, err)
	moduleRoot.Modules = modules
	err = moduleRoot.findVersions()
	require.NoError(t, err)
	for _, v := range modules {
		t.Logf("%s: %v", v.Path, v.Versions)
		switch v.Path {
		case "github.com/f110/gomodule-proxy-test":
			assert.ElementsMatch(t, []string{"v1.0.0"}, v.Versions)
		case "github.com/f110/gomodule-proxy-test/pkg/api":
			assert.ElementsMatch(t, []string{"pkg/api/v1.5.0"}, v.Versions)
		}
	}
}
