package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"ksfassistant/core/internal/rpc"
	"ksfassistant/core/internal/service"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = os.Stdin.Close()
	}()
	core := service.New()
	defer core.Close()
	return rpc.New(core, os.Stdin, os.Stdout).Serve(ctx)
}
