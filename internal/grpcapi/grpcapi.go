// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package grpcapi implements the RungarService gRPC service defined in
// proto/rungar/v1, over the scale set manager and the event log.
package grpcapi

import (
	"context"

	"google.golang.org/grpc"

	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/scaleset"
	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// ScaleSetManager is what the service asks of the scale sets, their runners
// and the providers they run on. *scaleset.Manager is one.
type ScaleSetManager interface {
	ConfiguredScaleSets() []types.ScaleSet
	ScaleSets(ctx context.Context) (types.ScaleSetList, error)
	ScaleSet(ctx context.Context, name, runnerGroup string) (types.ScaleSet, error)
	Reconcile(ctx context.Context, names []string) ([]types.ScaleSet, error)
	SetPaused(name string, paused bool) (types.ScaleSet, error)
	RemoveScaleSet(ctx context.Context, name, runnerGroup string) (types.ScaleSetRemoval, error)

	Runners(ctx context.Context, filter scaleset.RunnerFilter) (types.RunnerList, error)
	RemoveRunner(ctx context.Context, name string) (types.Runner, error)

	Providers(ctx context.Context, name string) ([]types.Provider, error)
	ProvidersAndRunners(ctx context.Context) ([]types.Provider, types.RunnerList, error)
	SetProviderDisabled(ctx context.Context, name string, disabled bool) (types.Provider, error)
}

// EventLog is what the service asks of the event log. *events.Log is one.
type EventLog interface {
	List(f events.Filter, limit int) []events.Event
	Subscribe(f events.Filter, limit int) ([]events.Event, *events.Subscription)
}

var (
	_ ScaleSetManager = (*scaleset.Manager)(nil)
	_ EventLog        = (*events.Log)(nil)
)

// Config holds the dependencies for creating a Server.
type Config struct {
	ScaleSets ScaleSetManager
	Events    EventLog

	// Info describes the running daemon.
	Info DaemonInfo
}

// Server implements rungarv1.RungarServiceServer. Its methods, and the
// conversions they use, are in a file per resource.
type Server struct {
	scaleSets ScaleSetManager
	events    EventLog
	info      DaemonInfo
}

var _ rungarv1.RungarServiceServer = (*Server)(nil)

// NewServer returns a Server over cfg's dependencies.
func NewServer(cfg Config) *Server {
	return &Server{scaleSets: cfg.ScaleSets, events: cfg.Events, info: cfg.Info}
}

// Register registers the service with gs.
func (s *Server) Register(gs *grpc.Server) {
	rungarv1.RegisterRungarServiceServer(gs, s)
}
