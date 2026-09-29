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
	"github.com/konradasb/rungar/internal/metrics"
	"github.com/konradasb/rungar/internal/scaleset"
	"github.com/konradasb/rungar/internal/types"
	"github.com/konradasb/rungar/internal/version"
)

// Serve runs the daemon from the configuration file until ctx is cancelled.
func Serve(ctx context.Context, configFile string) error {
	cfg, err := config.Load(configFile)
	if err != nil {
		return err
	}

	level, err := cfg.Level()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	m := metrics.New()

	logger.Info("starting", slog.String("version", version.String()),
		slog.String("installation", cfg.Installation),
		slog.Int("providers", len(cfg.Providers)),
		slog.Int("scale_sets", len(cfg.ScaleSets)))

	warnExposed(logger, cfg.SecretFiles())

	eventLog, err := events.Open(events.Config{
		Path:     cfg.Events.File,
		MaxCount: cfg.Events.MaxCount,
		MaxAge:   cfg.Events.MaxAge,
		Logger:   component(logger, "events"),
	})
	if err != nil {
		return err
	}
	// Closed last: everything below records or reads events until it stops.
	defer func() {
		if err := eventLog.Close(); err != nil {
			logger.Warn("closing the events", slog.Any("error", err))
		}
	}()

	restClient, err := newRESTClient(cfg.GitHub, m)
	if err != nil {
		return err
	}

	scaleSetClient, err := newScaleSetClient(cfg.GitHub, m)
	if err != nil {
		return err
	}

	providers, err := openFleet(cfg, eventLog, logger)
	if err != nil {
		return err
	}
	defer func() {
		if err := providers.Close(); err != nil {
			logger.Warn("closing providers", slog.Any("error", err))
		}
	}()

	sets, err := scaleset.New(scaleset.Config{
		ScaleSets:         cfg.ScaleSets,
		Installation:      cfg.Installation,
		ReconcileInterval: cfg.ReconcileInterval,
		Fleet:             providers,
		GitHub:            scaleSetClient,
		NewScaleSetClient: func() (*ghscaleset.Client, error) { return newScaleSetClient(cfg.GitHub, m) },
		GitHubRunners:     restClient,
		Events:            eventLog,
		Metrics:           m,
		Logger:            logger,
	})
	if err != nil {
		return err
	}

	info := types.DaemonInfo{
		StartTime:         time.Now(),
		ConfigFile:        configFile,
		Installation:      cfg.Installation,
		GitHubURL:         cfg.GitHub.URL,
		GitHubScope:       cfg.GitHub.Scope(),
		GitHubCredentials: cfg.GitHub.Summary(),
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

	if cfg.Metrics.Enable {
		g.Go(func() error { return serveMetrics(ctx, cfg.Metrics.Listen, m, logger) })
	}

	if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("rungar: %w", err)
	}

	return nil
}

// openFleet connects to every configured provider. On failure, the providers
// already opened are closed.
func openFleet(cfg *config.Config, recorder events.Recorder, logger *slog.Logger) (*fleet.Manager, error) {
	providers := make([]fleet.Provider, 0, len(cfg.Providers))

	for _, p := range cfg.Providers {
		backend, err := p.Settings().Open(component(logger, "provider").With(slog.String("provider", p.Name)))
		if err != nil {
			for _, opened := range providers {
				_ = opened.Backend.Close()
			}

			return nil, fmt.Errorf("provider %q: %w", p.Name, err)
		}

		providers = append(providers, fleet.Provider{
			Name:       p.Name,
			Type:       p.Type,
			Weight:     p.Weight,
			MaxRunners: p.MaxRunners,
			Endpoint:   p.Settings().Endpoint(),
			Disabled:   p.Disabled,
			Backend:    backend,
		})
	}

	return fleet.New(providers, fleet.Options{
		// A scale set waiting for room retries every reconcile interval, so
		// its hold lasts two and is renewed before it lapses.
		HoldDown: 2 * cfg.ReconcileInterval,
		Events:   recorder,
		Logger:   component(logger, "fleet"),
	}), nil
}

// component returns logger with a component attribute, such as
// component=fleet.
func component(logger *slog.Logger, name string) *slog.Logger {
	return logger.With(slog.String("component", name))
}
