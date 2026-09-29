// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import (
	"net"

	"github.com/konradasb/rungar/internal/errdefs"
)

// MetricsConfig configures the Prometheus endpoint, served on /metrics.
type MetricsConfig struct {
	// Enable serves the metrics. The endpoint has no authentication.
	Enable bool `yaml:"enable,omitempty"`

	// Listen is the address the metrics are served on, as host:port.
	// Widening it makes them readable by anyone who can reach it. Unset is
	// 127.0.0.1:9102.
	Listen string `yaml:"listen,omitempty"`
}

func (m *MetricsConfig) validate() error {
	if !m.Enable {
		return nil
	}
	if _, _, err := net.SplitHostPort(m.Listen); err != nil {
		return errdefs.InvalidArgument("invalid metrics.listen %q: want HOST:PORT", m.Listen)
	}

	return nil
}
