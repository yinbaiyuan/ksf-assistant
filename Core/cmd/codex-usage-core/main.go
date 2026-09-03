package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"codexusagebar/core/internal/rpc"
	"codexusagebar/core/internal/service"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	core := service.New()
	defer core.Close()
	if err := rpc.New(core, os.Stdin, os.Stdout).Serve(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
