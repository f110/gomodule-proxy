package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
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
	Versions []*ModuleVersion

	modFilePath string
	dir         string
	repoRoot    *vcs.RepoRoot
}

type ModuleVersion struct {
	Version string
	Time    time.Time
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
	if err := f.setTagSyncDefaultCommand(repoRoot, dir); err != nil {
		return nil, xerrors.Errorf(": %w", err)
	}
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

	return moduleRoot, nil
}

func (f *ModuleFetcher) setTagSyncDefaultCommand(repoRoot *vcs.RepoRoot, dir string) error {
	if repoRoot.VCS.Cmd != "git" {
		return nil
	}

	repo, err := git.PlainOpen(dir)
	if err != nil {
		return xerrors.Errorf(": %w", err)
	}
	remote, err := repo.Remote("origin")
	if err != nil {
		return xerrors.Errorf(": %w", err)
	}
	refs, err := remote.List(&git.ListOptions{})
	if err != nil {
		return xerrors.Errorf(": %w", err)
	}
	var headRef *plumbing.Reference
	for _, ref := range refs {
		if strings.HasPrefix(ref.Name().String(), "refs/pull") {
			continue
		}
		if ref.Name().String() == "HEAD" {
			headRef = ref
			break
		}
	}
	if headRef.Target().Short() != "master" {
		repoRoot.VCS.TagSyncDefault = fmt.Sprintf("checkout %s", headRef.Target().Short())
	}

	return nil
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
		if err := repoRoot.VCS.TagSync(dir, ""); err != nil {
			return xerrors.Errorf(": %w", err)
		}
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

func (m *ModuleRoot) Archive(module, version string) (io.Reader, error) {
	var mod *Module
	for _, v := range m.Modules {
		if v.Path == module {
			mod = v
			break
		}
	}
	isTag := false
	for _, v := range mod.Versions {
		if version == v.Version {
			isTag = true
			break
		}
	}
	excludeDirs := make(map[string]struct{})
	for _, v := range m.Modules {
		if v == mod {
			continue
		}
		excludeDirs[filepath.Dir(v.modFilePath)+"/"] = struct{}{}
	}

	if isTag {
		if err := m.repoRoot.VCS.TagSync(m.dir, version); err != nil {
			return nil, xerrors.Errorf(": %w", err)
		}
		buf := new(bytes.Buffer)
		w := zip.NewWriter(buf)
		relPath := filepath.Join(m.dir, strings.TrimPrefix(mod.Path, m.repoRoot.Root))
		gitDir := filepath.Join(m.dir, ".git") + "/"
		modDir := mod.Path + "@" + version
		foundLicenseFile := false
		err := filepath.Walk(relPath, func(path string, info fs.FileInfo, err error) error {
			if err != nil {
				return xerrors.Errorf(": %w", err)
			}
			if info.IsDir() {
				return nil
			}
			if strings.HasPrefix(path, gitDir) {
				return nil
			}
			for k := range excludeDirs {
				if strings.HasPrefix(path, k) {
					return nil
				}
			}

			p := strings.TrimPrefix(path, relPath)
			p = p[1:]
			p = filepath.Join(modDir, p)
			if p == filepath.Join(modDir, "LICENSE") {
				foundLicenseFile = true
			}
			f, err := w.Create(p)
			if err != nil {
				return xerrors.Errorf(": %w", err)
			}
			fBuf, err := os.ReadFile(path)
			if err != nil {
				return xerrors.Errorf(": %w", err)
			}
			_, err = f.Write(fBuf)
			if err != nil {
				return xerrors.Errorf(": %w", err)
			}
			return nil
		})
		if err != nil {
			return nil, xerrors.Errorf(": %w", err)
		}

		// Find and pack LICENSE file
		if !foundLicenseFile {
			d := relPath
			for {
				if _, err := os.Stat(filepath.Join(d, "LICENSE")); os.IsNotExist(err) {
					if d == m.dir {
						break
					}
					d = filepath.Dir(d)
					continue
				}

				f, err := w.Create(filepath.Join(modDir, "LICENSE"))
				if err != nil {
					return nil, xerrors.Errorf(": %w", err)
				}
				fBuf, err := os.ReadFile(filepath.Join(d, "LICENSE"))
				if err != nil {
					return nil, xerrors.Errorf(": %w", err)
				}
				_, err = f.Write(fBuf)
				if err != nil {
					return nil, xerrors.Errorf(": %w", err)
				}
				break
			}
		}

		w.Close()
		return buf, nil
	}

	return nil, xerrors.New("specified commit is not support")
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

	repo, err := git.PlainOpen(m.dir)
	if err != nil {
		return xerrors.Errorf(": %w", err)
	}
	var allVer []*ModuleVersion
	for _, ver := range versions {
		if !semver.IsValid(ver) {
			continue
		}
		modVer := &ModuleVersion{Version: ver}
		ref, err := repo.Reference(plumbing.NewTagReferenceName(ver), true)
		if err == nil {
			obj, err := repo.Object(plumbing.AnyObject, ref.Hash())
			if err == nil {
				switch v := obj.(type) {
				case *object.Tag:
					modVer.Time = v.Tagger.When.In(time.UTC)
				case *object.Commit:
					modVer.Time = v.Author.When.In(time.UTC)
				}
			} else {
				log.Printf("Failed to get tag object %s %s: %v", ver, ref.Hash().String(), err)
			}
		} else {
			log.Printf("Failed ref %s: %v", ver, err)
		}
		if modVer.Time.IsZero() {
			log.Printf("Failed to get time %s", ver)
		}
		allVer = append(allVer, modVer)
	}

	for _, v := range m.Modules {
		v.setVersions(allVer)
	}

	return nil
}

func (m *Module) ModuleFile(version string) ([]byte, error) {
	isTag := false
	for _, v := range m.Versions {
		if version == v.Version {
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

func (m *Module) setVersions(vers []*ModuleVersion) {
	relPath := strings.TrimPrefix(m.Path, m.repoRoot.Root)
	if len(relPath) > 0 {
		relPath = relPath[1:]
	}

	var modVer []*ModuleVersion
	for _, ver := range vers {
		if len(relPath) > 0 && strings.HasPrefix(ver.Version, relPath) {
			modVer = append(modVer, ver)
		}
	}
	if len(modVer) == 0 {
		for _, ver := range vers {
			if !semver.IsValid(ver.Version) {
				continue
			}
			modVer = append(modVer, ver)
		}
	}
	sort.Slice(modVer, func(i, j int) bool {
		cmp := semver.Compare(modVer[i].Version, modVer[j].Version)
		if cmp != 0 {
			return cmp < 0
		}
		return modVer[i].Version < modVer[j].Version
	})
	m.Versions = modVer
}
