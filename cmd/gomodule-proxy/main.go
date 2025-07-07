package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

func goModuleProxy() error {
	proxy := newGoModuleProxyCommand()

	cmd := &cobra.Command{
		Use: "gomodule-proxy",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := proxy.Init(); err != nil {
				return err
			}
			return proxy.Run()
		},
	}
	proxy.Flags(cmd.Flags())
	for _, v := range proxy.RequiredFlags() {
		if err := cmd.MarkFlagRequired(v); err != nil {
			return err
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cmd.ExecuteContext(ctx)
}

func main() {
	if err := goModuleProxy(); err != nil {
		format := "%v\n"
		if os.Getenv("DEBUG") != "" {
			format = "%+v\n"
		}
		fmt.Fprintf(os.Stderr, format, err)
		os.Exit(1)
	}
}
