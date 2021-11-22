package main

import (
	"context"
	"fmt"
	"net/http"
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
	Version string
	Time    time.Time
}

func (m *ModuleProxy) Versions(_ context.Context, module string) ([]string, error) {
	modRoot, err := m.fetcher.Fetch(module)
	if err != nil {
		return nil, xerrors.Errorf(": %w", err)
	}
	for _, mod := range modRoot.Modules {
		if mod.Path == module {
			var versions []string
			for _, v := range mod.Versions {
				versions = append(versions, v.Version)
			}
			return versions, nil
		}
	}

	return nil, xerrors.Errorf("%s is not found", module)
}

func (m *ModuleProxy) GetInfo(_ context.Context, module, version string) (Info, error) {
	modRoot, err := m.fetcher.Fetch(module)
	if err != nil {
		return Info{}, xerrors.Errorf(": %w", err)
	}

	var mod *Module
	for _, v := range modRoot.Modules {
		if v.Path == module {
			mod = v
			break
		}
	}
	if mod == nil {
		return Info{}, xerrors.Errorf("%s is not found", module)
	}
	for _, v := range mod.Versions {
		if v.Version == version {
			return Info{Version: v.Version, Time: v.Time}, nil
		}
	}

	return Info{}, xerrors.Errorf("%s is not found in %s", version, module)
}

func (m *ModuleProxy) GetGoMod(_ context.Context, module, version string) (string, error) {
	modRoot, err := m.fetcher.Fetch(module)
	if err != nil {
		return "", xerrors.Errorf(": %w", err)
	}

	var mod *Module
	for _, v := range modRoot.Modules {
		if v.Path == module {
			mod = v
			break
		}
	}
	if mod == nil {
		return "", xerrors.Errorf("%s is not found", module)
	}

	goMod, err := mod.ModuleFile(version)
	if err != nil {
		return "", xerrors.Errorf(": %w", err)
	}

	return string(goMod), nil
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
