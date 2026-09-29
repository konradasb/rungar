// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"log/slog"

	"google.golang.org/grpc"

	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/grpcapi"
	"github.com/konradasb/rungar/internal/scaleset"
)

// serveAPI serves the gRPC API on the Unix socket until stop is called.
func serveAPI(ctx context.Context, socket string, sets *scaleset.Manager, eventLog *events.Log,
	info grpcapi.DaemonInfo, logger *slog.Logger,
) (stop func(), err error) {
	listener, err := listenSocket(ctx, socket)
	if err != nil {
		return nil, err
	}

	// Stop waits for handlers, so providers are not closed under one.
	server := grpc.NewServer(
		grpc.WaitForHandlers(true),
		grpc.ChainUnaryInterceptor(grpcapi.UnaryErrorCodeInterceptor),
		grpc.ChainStreamInterceptor(grpcapi.StreamErrorCodeInterceptor),
	)
	grpcapi.NewServer(grpcapi.Config{ScaleSets: sets, Events: eventLog, Info: info}).Register(server)

	go func() {
		// Serve returns nil once stopped.
		if err := server.Serve(listener); err != nil {
			logger.Error("the command line's socket stopped serving", slog.Any("error", err))
		}
	}()

	// Stopped rather than drained: a call may be waiting on a reconcile.
	return server.Stop, nil
}
