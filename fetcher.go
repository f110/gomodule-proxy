package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
	"golang.org/x/tools/go/vcs"
	"golang.org/x/xerrors"
)

type ModuleRoot struct {
	RootPath string
	Modules  []*Module

	dir      string
	repoRoot *vcs.RepoRoot
}

type Module struct {
	Path     string
	Versions []string

	modFilePath string
	dir         string
	repoRoot    *vcs.RepoRoot
}

type ModuleFetcher struct {
	baseDir string
}

func NewModuleFetcher(baseDir string) *ModuleFetcher {
	return &ModuleFetcher{baseDir: baseDir}
}

func (f *ModuleFetcher) Fetch(importPath string) (*ModuleRoot, error) {
	repoRoot, err := vcs.RepoRootForImportPath(importPath, false)
	if err != nil {
		return nil, xerrors.Errorf(": %w", err)
	}

	dir := filepath.Join(f.baseDir, repoRoot.Root)
	if err := f.updateOrCreate(repoRoot, dir); err != nil {
		return nil, xerrors.Errorf(": %w", err)
	}

	moduleRoot := NewModuleRoot(repoRoot, dir)
	modules, err := moduleRoot.findModules()
	if err != nil {
		return nil, xerrors.Errorf(": %w", err)
	}
	moduleRoot.Modules = modules

	if err := moduleRoot.findVersions(); err != nil {
		return nil, xerrors.Errorf(": %w", err)
	}

	return &ModuleRoot{
		RootPath: repoRoot.Root,
		Modules:  modules,
	}, nil
}

func (f *ModuleFetcher) updateOrCreate(repoRoot *vcs.RepoRoot, dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return xerrors.Errorf(": %w", err)
		}
		if err = repoRoot.VCS.Create(dir, repoRoot.Repo); err != nil {
			return xerrors.Errorf(": %w", err)
		}
	} else {
		if err = repoRoot.VCS.Download(dir); err != nil {
			return xerrors.Errorf(": %w", err)
		}
	}

	return nil
}

func NewModuleRoot(repoRoot *vcs.RepoRoot, dir string) *ModuleRoot {
	return &ModuleRoot{
		RootPath: repoRoot.Root,
		dir:      dir,
		repoRoot: repoRoot,
	}
}

func (m *ModuleRoot) findModules() ([]*Module, error) {
	var mods []*Module
	err := filepath.Walk(m.dir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return xerrors.Errorf(": %w", err)
		}
		if info.Name() != "go.mod" {
			return nil
		}
		buf, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		modFile, err := modfile.Parse(path, buf, nil)
		if err != nil {
			return err
		}
		mods = append(mods, &Module{
			Path:        modFile.Module.Mod.Path,
			modFilePath: path,
			dir:         m.dir,
			repoRoot:    m.repoRoot,
		})

		return nil
	})
	if err != nil {
		return nil, xerrors.Errorf(": %w", err)
	}

	return mods, nil
}

func (m *ModuleRoot) findVersions() error {
	if m.Modules == nil {
		return xerrors.New("should find the module first")
	}

	versions, err := m.repoRoot.VCS.Tags(m.dir)
	if err != nil {
		return xerrors.Errorf(": %w", err)
	}
	var allVer []string
	for _, ver := range versions {
		allVer = append(allVer, ver)
	}

	for _, v := range m.Modules {
		v.setVersions(allVer)
	}

	return nil
}

func (m *Module) ModuleFile(version string) ([]byte, error) {
	isTag := false
	for _, v := range m.Versions {
		if version == v {
			isTag = true
			break
		}
	}
	if isTag {
		if err := m.repoRoot.VCS.TagSync(m.dir, version); err != nil {
			return nil, xerrors.Errorf(": %w", err)
		}
		buf, err := os.ReadFile(m.modFilePath)
		if err != nil {
			return nil, xerrors.Errorf(": %w", err)
		}
		return buf, nil
	}

	return nil, xerrors.New("specified commit is not supported")
}

func (m *Module) setVersions(vers []string) {
	relPath := strings.TrimPrefix(m.Path, m.repoRoot.Root)
	if len(relPath) > 0 {
		relPath = relPath[1:]
	}

	var modVer []string
	for _, ver := range vers {
		if len(relPath) > 0 && strings.HasPrefix(ver, relPath) {
			modVer = append(modVer, ver)
		}
	}
	if len(modVer) == 0 {
		for _, ver := range vers {
			if !semver.IsValid(ver) {
				continue
			}
			modVer = append(modVer, ver)
		}
	}
	m.Versions = modVer
}
