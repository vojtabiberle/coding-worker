package main

import (
	"coding-worker/cli"
	"coding-worker/worker"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, e := worker.Open()
	if e == nil {
		defer a.Store.Close()
		e = cli.Execute(ctx, a, os.Args[1:], os.Stdout)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
