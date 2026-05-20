package main

import (
	"context"
	"os"
	"os/signal"
	"postit/internal/web"
	"syscall"
)

// runWeb starts the HTTP server and blocks until a shutdown signal is received.
func runWeb(deps *AppDeps, port int, enableMock bool) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		cancel()
	}()

	server := web.NewServer(
		deps.Store, deps.Processor, deps.Client,
		deps.Collection, deps.FlatList, enableMock,
	)
	return server.Start(ctx, port)
}
