// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package daemon implements rungar serve: it loads the configuration, connects
// to the providers and GitHub, runs the scale sets, and serves the API on a
// Unix socket and the metrics over HTTP.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	ghscaleset "github.com/actions/scaleset"
	"golang.org/x/sync/errgroup"

	"github.com/konradasb/rungar/internal/config"
	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/fleet"
	"github.com/konradasb/rungar/internal/grpcapi"
	"github.com/konradasb/rungar/internal/metrics"
	"github.com/konradasb/rungar/internal/scaleset"
	"github.com/konradasb/rungar/internal/version"
)

// The daemon's metrics satisfy the recorders the scale sets and the fleet
// consume, which neither package imports to check itself.
var (
	_ scaleset.MetricsRecorder   = (*metrics.Metrics)(nil)
	_ fleet.ProviderCallRecorder = (*metrics.Metrics)(nil)
)

// Serve runs the daemon from the configuration file until ctx is cancelled.
func Serve(ctx context.Context, configFile string) error {
	cfg, err := config.Load(configFile)
	if err != nil {
		return err
	}

	handler, err := cfg.LogHandler(os.Stderr)
	if err != nil {
		return err
	}

	logger := slog.New(handler)
	daemonMetrics := metrics.New()

	logger.Info("starting", slog.String("version", version.String()),
		slog.String("installation", cfg.Installation),
		slog.Int("providers", len(cfg.Providers)),
		slog.Int("scale_sets", len(cfg.ScaleSets)))

	for _, d := range cfg.Deprecations() {
		logger.Warn("deprecated configuration: rungar config migrate rewrites it",
			slog.String("file", configFile), slog.String("deprecation", d.String()))
	}

	warnExposedSecretFiles(logger, cfg.SecretFiles())

	eventLog, err := events.Open(events.Config{
		File:     cfg.Events.File,
		MaxCount: cfg.Events.MaxCount,
		MaxAge:   cfg.Events.MaxAge,
		Logger:   componentLogger(logger, "events"),
	})
	if err != nil {
		return fmt.Errorf("open the event log: %w", err)
	}
	// Closed last: everything below records or reads events until it stops.
	defer func() {
		if err := eventLog.Close(); err != nil {
			logger.Warn("cannot close the event log; its remaining events may not be written", slog.Any("error", err))
		}
	}()

	restClient, err := newRESTClient(cfg.GitHub, daemonMetrics)
	if err != nil {
		return fmt.Errorf("github REST API: %w", err)
	}

	scaleSetClient, err := newScaleSetClient(cfg.GitHub, daemonMetrics)
	if err != nil {
		return fmt.Errorf("github scale set API: %w", err)
	}

	fleetManager, err := openFleet(ctx, cfg, eventLog, daemonMetrics, logger)
	if err != nil {
		return err
	}
	defer func() {
		if err := fleetManager.Close(); err != nil {
			logger.Warn("cannot close the providers", slog.Any("error", err))
		}
	}()

	sets, err := scaleset.New(scaleset.Config{
		ScaleSets:         cfg.ScaleSets,
		Installation:      cfg.Installation,
		ReconcileInterval: cfg.ReconcileInterval,
		Fleet:             fleetManager,
		GitHub:            scaleSetClient,
		NewScaleSetClient: func() (*ghscaleset.Client, error) { return newScaleSetClient(cfg.GitHub, daemonMetrics) },
		GitHubRunners:     restClient,
		Events:            eventLog,
		Metrics:           daemonMetrics,
		Logger:            componentLogger(logger, "scaleset"),
		ListenerLogger:    componentLogger(logger, "listener"),
	})
	if err != nil {
		return fmt.Errorf("scale sets: %w", err)
	}

	info := grpcapi.DaemonInfo{
		StartedAt:         time.Now(),
		ConfigFile:        configFile,
		Installation:      cfg.Installation,
		GitHubURL:         cfg.GitHub.URL,
		GitHubScope:       cfg.GitHub.Scope(),
		GitHubCredentials: cfg.GitHub.CredentialSummary(),
	}

	stopAPI, err := serveAPI(ctx, cfg.Socket, sets, eventLog, info, logger)
	if err != nil {
		return err
	}
	defer stopAPI()

	logger.Info("serving the command line", slog.String("socket", cfg.Socket))
	if len(cfg.ScaleSets) == 0 {
		logger.Warn("no scale sets are configured: the daemon serves the command line and runs no runners")
	}

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error { return sets.Run(ctx) })

	if cfg.Metrics.Enabled {
		g.Go(func() error { return serveMetrics(ctx, cfg.Metrics.Listen, daemonMetrics, logger) })
	}

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
}

// openFleet connects to every configured provider, timing and counting the
// calls made to each with calls. On failure, the providers already opened are
// closed.
func openFleet(
	ctx context.Context, cfg *config.Config, eventLog *events.Log, calls fleet.ProviderCallRecorder, logger *slog.Logger,
) (*fleet.Manager, error) {
	providers := make([]fleet.ProviderConfig, 0, len(cfg.Providers))

	for _, p := range cfg.Providers {
		backend, err := p.Config().Open(ctx, componentLogger(logger, "provider").With(slog.String("provider", p.Name)))
		if err != nil {
			for _, opened := range providers {
				_ = opened.Backend.Close()
			}

			return nil, fmt.Errorf("provider %q: %w", p.Name, err)
		}

		providers = append(providers, fleet.ProviderConfig{
			Name:       p.Name,
			Type:       p.Type,
			Weight:     p.Weight,
			MaxRunners: p.MaxRunners,
			Endpoint:   p.Config().Endpoint(),
			Disabled:   p.Disabled,
			Backend:    backend,
		})
	}

	return fleet.New(providers, fleet.Config{
		// A scale set waiting for room retries every reconcile interval, so
		// its hold lasts two and is renewed before it lapses.
		HoldDown: 2 * cfg.ReconcileInterval,
		Events:   eventLog,
		Calls:    calls,
		Logger:   componentLogger(logger, "fleet"),
	}), nil
}

// componentLogger returns logger with a component attribute, such as
// component=fleet.
func componentLogger(logger *slog.Logger, name string) *slog.Logger {
	return logger.With(slog.String("component", name))
}
