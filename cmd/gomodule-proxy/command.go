package main

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/google/go-github/v40/github"
	"github.com/spf13/pflag"
	"go.f110.dev/xerrors"
	"golang.org/x/oauth2"

	"go.f110.dev/gomodule-proxy/cmd/gomodule-proxy/internal/config"
	"go.f110.dev/gomodule-proxy/internal/gomodule"
)

type goModuleProxyCommand struct {
	ConfigPath   string
	ModuleDir    string
	Addr         string
	UpstreamURL  string
	GitHubToken  string
	GitHubAPIURL string

	upstream     *url.URL
	config       config.Config
	githubClient *github.Client
}

func newGoModuleProxyCommand() *goModuleProxyCommand {
	return &goModuleProxyCommand{
		Addr:         ":7589",
		UpstreamURL:  "https://proxy.golang.org",
		GitHubAPIURL: "https://api.github.com/",
	}
}

func (c *goModuleProxyCommand) Flags(fs *pflag.FlagSet) {
	fs.StringVarP(&c.ConfigPath, "config", "c", c.ConfigPath, "Configuration file path")
	fs.StringVar(&c.ModuleDir, "mod-dir", c.ModuleDir, "Module directory")
	fs.StringVar(&c.Addr, "addr", c.Addr, "Listen addr")
	fs.StringVar(&c.UpstreamURL, "upstream", c.UpstreamURL, "Upstream module proxy URL")
	fs.StringVar(&c.GitHubToken, "github-token", c.GitHubToken, "GitHub API token")
	fs.StringVar(&c.GitHubAPIURL, "github-api-url", c.GitHubAPIURL, "URL of GitHub REST endpoint")
}

func (c *goModuleProxyCommand) RequiredFlags() []string {
	return []string{"config"}
}

func (c *goModuleProxyCommand) Init() error {
	if c.IsDebug() {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	}

	conf, err := config.ReadConfig(c.ConfigPath)
	if err != nil {
		return err
	}
	c.config = conf

	uu, err := url.Parse(c.UpstreamURL)
	if err != nil {
		return xerrors.WithStack(err)
	}
	c.upstream = uu

	gu, err := url.Parse(c.GitHubAPIURL)
	if err != nil {
		return xerrors.WithStack(err)
	}

	var tc *http.Client
	if os.Getenv("GITHUB_TOKEN") != "" {
		c.GitHubToken = os.Getenv("GITHUB_TOKEN")
	}
	if c.GitHubToken != "" {
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: c.GitHubToken},
		)
		tc = oauth2.NewClient(context.Background(), ts)
	}
	githubClient := github.NewClient(tc)
	githubClient.BaseURL = gu
	c.githubClient = githubClient

	return nil
}

func (c *goModuleProxyCommand) Run() error {
	stopErrCh := make(chan error, 1)
	startErrCh := make(chan error, 1)

	var modules []*regexp.Regexp
	for _, v := range c.config {
		re, err := regexp.Compile(v.ModuleName)
		if err != nil {
			return xerrors.WithStack(err)
		}
		modules = append(modules, re)
	}
	proxy := gomodule.NewModuleProxy(modules, c.ModuleDir, c.githubClient)
	server := gomodule.NewProxyServer(c.Addr, c.upstream, proxy, c.IsDebug())

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		defer cancel()

		select {
		case <-ctx.Done():
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			log.Print("Shutting down the server")
			if err := server.Stop(ctx); err != nil {
				stopErrCh <- err
			}
			cancel()
			log.Print("Server shutdown successfully")
			close(stopErrCh)
		case <-stopErrCh:
			return
		}
	}()

	go func() {
		if err := server.Start(); err != nil {
			startErrCh <- err
		}
	}()

	// Wait for stopping a server
	select {
	case err, ok := <-startErrCh:
		if ok {
			return err
		}
	case err, ok := <-stopErrCh:
		if ok {
			return err
		}
	}

	return nil
}

func (c *goModuleProxyCommand) IsDebug() bool {
	return os.Getenv("DEBUG") != ""
}
