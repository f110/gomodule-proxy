package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/pflag"
	"golang.org/x/xerrors"
)

type goModuleProxyCommand struct {
	Addr string
}

func newGoModuleProxyCommand() *goModuleProxyCommand {
	return &goModuleProxyCommand{
		Addr: ":7589",
	}
}

func (c *goModuleProxyCommand) Flags(fs *pflag.FlagSet) {
	fs.StringVar(&c.Addr, "addr", c.Addr, "Listen addr")
}

func (c *goModuleProxyCommand) RequiredFlags() []string {
	return []string{}
}

func (c *goModuleProxyCommand) Init() error {
	if os.Getenv("DEBUG") != "" {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	}
	return nil
}

func (c *goModuleProxyCommand) Run() error {
	stopErrCh := make(chan error, 1)
	startErrCh := make(chan error, 1)
	server := newProxyServer(c.Addr)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		defer cancel()

		select {
		case <-ctx.Done():
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			log.Print("Shutting down the server")
			if err := server.Stop(ctx); err != nil {
				stopErrCh <- xerrors.Errorf(": %w", err)
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
			startErrCh <- xerrors.Errorf(": %w", err)
		}
	}()

	// Wait for stop a server
	select {
	case err, ok := <-startErrCh:
		if ok {
			return xerrors.Errorf(": %w", err)
		}
	case err, ok := <-stopErrCh:
		if ok {
			return xerrors.Errorf(": %w", err)
		}
	}

	return nil
}
