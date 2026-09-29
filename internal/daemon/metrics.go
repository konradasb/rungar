// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/konradasb/rungar/internal/config"
	"github.com/konradasb/rungar/internal/metrics"
)

// metricsShutdownTimeout bounds how long in-flight scrapes may take on stop.
const metricsShutdownTimeout = 5 * time.Second

// serveMetrics serves the Prometheus endpoint until ctx is cancelled.
func serveMetrics(ctx context.Context, listen string, m *metrics.Metrics, logger *slog.Logger) error {
	mux := http.NewServeMux()
	mux.Handle(config.MetricsPath, m.Handler())

	server := &http.Server{
		Addr:              listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), metricsShutdownTimeout)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Warn("metrics shutdown", slog.Any("error", err))
		}
	}()

	logger.Info("serving metrics", slog.String("listen", listen), slog.String("path", config.MetricsPath))

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("metrics: %w", err)
	}

	return nil
}
