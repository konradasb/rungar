// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package fleet

import (
	"context"
	"errors"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// ProviderCallRecorder records the calls made to providers. *metrics.Metrics
// implements it. Its method must not block.
type ProviderCallRecorder interface {
	// ObserveProviderCall records a call that took d and ended with result.
	ObserveProviderCall(provider, call string, result types.CallResult, d time.Duration)
}

// measuredProvider is a provider whose calls are timed and counted.
type measuredProvider struct {
	provider.Provider

	name     string
	recorder ProviderCallRecorder
}

func (p measuredProvider) List(ctx context.Context, selector map[string]string) ([]types.Machine, error) {
	start := time.Now()
	machines, err := p.Provider.List(ctx, selector)
	p.record("list", start, err)

	return machines, err
}

func (p measuredProvider) Create(ctx context.Context, spec types.MachineSpec) error {
	start := time.Now()
	err := p.Provider.Create(ctx, spec)
	p.record("create", start, err)

	return err
}

func (p measuredProvider) Delete(ctx context.Context, name string) error {
	start := time.Now()
	err := p.Provider.Delete(ctx, name)
	p.record("delete", start, err)

	return err
}

// record hands the recorder a call begun at start. One its caller cancelled
// says nothing of the provider, and is left out.
func (p measuredProvider) record(call string, start time.Time, err error) {
	result := types.CallOK

	switch {
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, errdefs.ErrNoCapacity):
		result = types.CallNoCapacity
	case err != nil:
		result = types.CallError
	}

	p.recorder.ObserveProviderCall(p.name, call, result, time.Since(start))
}
