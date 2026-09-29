// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package grpcapi implements the RungarService gRPC service defined in
// proto/rungar/v1, over the scale set manager and the event log.
package grpcapi

import (
	"google.golang.org/grpc"

	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/scaleset"
	"github.com/konradasb/rungar/internal/types"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// Config holds the dependencies for creating a Server.
type Config struct {
	ScaleSets *scaleset.Manager
	Events    *events.Log

	// Info describes the running daemon.
	Info types.DaemonInfo
}

// Server implements rungarv1.RungarServiceServer. Its methods, and the
// conversions they use, are in a file per resource.
type Server struct {
	scaleSets *scaleset.Manager
	events    *events.Log
	info      types.DaemonInfo
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
