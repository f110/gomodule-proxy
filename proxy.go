package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/go-github/v40/github"
	"golang.org/x/net/html"
	"golang.org/x/xerrors"
)

const (
	moduleProxyUserAgent = "gomodule-proxy/v0.1 github.com/f110/gomodule-proxy"
)

type ModuleProxy struct {
	conf Config

	fetcher      *ModuleFetcher
	httpClient   *http.Client
	githubClient *github.Client
}

func NewModuleProxy(conf Config, moduleDir string, githubClient *github.Client) *ModuleProxy {
	return &ModuleProxy{
		conf:         conf,
		fetcher:      NewModuleFetcher(moduleDir),
		githubClient: githubClient,
		httpClient:   &http.Client{},
	}
}

func (m *ModuleProxy) IsProxy(module string) bool {
	for _, v := range m.conf {
		if v.match.MatchString(module) {
			return true
		}
	}

	return false
}

func (m *ModuleProxy) IsUpstream(module string) bool {
	return !m.IsProxy(module)
}

type Info struct {
	Version string    `json:"version"`
	Time    time.Time `json:"time"`
}

func (m *ModuleProxy) Versions(_ context.Context, module string) ([]string, error) {
	modRoot, err := m.fetcher.Fetch(module)
	if err != nil {
		return nil, xerrors.Errorf(": %w", err)
	}
	for _, mod := range modRoot.Modules {
		if mod.Path == module {
			return mod.Versions, nil
		}
	}

	return nil, xerrors.Errorf("%s is not found", module)
}

func (m *ModuleProxy) GetInfo(ctx context.Context, module, version string) (Info, error) {
	vcs, repoRoot, err := m.getGoImport(ctx, module)
	if err != nil {
		return Info{}, xerrors.Errorf(": %w", err)
	}
	if !(vcs == "git" && strings.Contains(repoRoot, "github.com")) {
		return Info{}, xerrors.Errorf("the module is not hosted by github.com doesn't supported")
	}
	u, err := url.Parse(repoRoot)
	if err != nil {
		return Info{}, xerrors.Errorf(": %w", err)
	}
	s := strings.Split(u.Path, "/")
	owner, repo := s[1], s[2]

	commit, _, err := m.githubClient.Repositories.GetCommit(context.Background(), owner, repo, version, &github.ListOptions{})
	if err != nil {
		return Info{}, xerrors.Errorf(": %w", err)
	}

	return Info{
		Version: fmt.Sprintf("v0.0.0-%s-%s", commit.Commit.Committer.GetDate().Format("20060102150405"), commit.GetSHA()[0:12]),
		Time:    commit.Commit.Committer.GetDate(),
	}, nil
}

func (m *ModuleProxy) GetGoMod(ctx context.Context, module, version string) (string, error) {
	vcs, repoRoot, err := m.getGoImport(ctx, module)
	if err != nil {
		return "", xerrors.Errorf(": %w", err)
	}
	if !(vcs == "git" && strings.Contains(repoRoot, "github.com")) {
		return "", xerrors.Errorf("the module is not hosted by github.com doesn't supported")
	}
	u, err := url.Parse(repoRoot)
	if err != nil {
		return "", xerrors.Errorf(": %w", err)
	}
	s := strings.Split(u.Path, "/")
	owner, repo := s[1], s[2]
	_ = owner
	_ = repo

	return "", nil
}

func (m *ModuleProxy) getGoImport(ctx context.Context, module string) (vcs string, repoRoot string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://%s?go-get=1", module), nil)
	if err != nil {
		return "", "", xerrors.Errorf(": %w", err)
	}
	res, err := m.httpClient.Do(req)
	if err != nil {
		return "", "", xerrors.Errorf(": %w", err)
	}
	defer res.Body.Close()

	switch res.StatusCode {
	case http.StatusOK:
	default:
		return "", "", xerrors.Errorf("got %d expect 200", res.StatusCode)
	}

	doc, err := html.Parse(res.Body)
	if err != nil {
		return "", "", xerrors.Errorf(": %w", err)
	}
	var f func(node *html.Node)
	f = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "meta" {
			found := false
			var content string
			for _, v := range node.Attr {
				if v.Key == "name" && v.Val == "go-import" {
					found = true
					continue
				}
				if v.Key == "content" {
					content = v.Val
				}
			}
			if found {
				prefix, v, root := m.findGoImport(content)
				_ = prefix
				vcs = v
				repoRoot = root
				if vcs != "" && repoRoot != "" {
					// Stop walking
					return
				}
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	if vcs == "" || repoRoot == "" {
		return "", "", xerrors.Errorf("not found go-import")
	}

	return
}

func (m *ModuleProxy) findGoImport(content string) (prefix string, vcs string, root string) {
	// <meta name="go-import" content="github.com/f110/mono git https://github.com/f110/mono.git">
	if fields := strings.Fields(content); len(fields) == 3 && fields[1] != "mod" {
		return fields[0], fields[1], fields[2]
	}

	return
}

type httpTransport struct{}

var _ http.RoundTripper = &httpTransport{}

func (tr *httpTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", moduleProxyUserAgent)

	return http.DefaultTransport.RoundTrip(req)
}
