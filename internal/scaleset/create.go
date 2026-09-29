// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/types"
)

// createRunner registers a runner with GitHub and has the fleet make its
// machine on the first provider that takes it. A registration no machine was
// made for is removed again.
func (s *scaleSet) createRunner(ctx context.Context) (types.Runner, error) {
	// Nothing is registered with GitHub when no provider would be tried.
	if err := s.fleet.CanPlace(); err != nil {
		return types.Runner{}, err
	}

	name, err := runnerName(s.spec.Name)
	if err != nil {
		return types.Runner{}, err
	}

	jit, err := s.github.GenerateJitRunnerConfig(ctx,
		&ghscaleset.RunnerScaleSetJitRunnerSetting{Name: name}, int(s.id.Load()))
	if err != nil {
		return types.Runner{}, fmt.Errorf("register runner %q with GitHub: %w", name, err)
	}

	// A machine not made within the start timeout would be written off anyway.
	createCtx, cancel := context.WithTimeout(ctx, s.spec.StartTimeout)
	defer cancel()

	s.mu.Lock()
	s.creating[name] = true
	s.mu.Unlock()

	on, release, err := s.fleet.Place(createCtx, s.runnersPerProvider(), func(provider string) types.MachineSpec {
		return s.machineSpec(name, jit.EncodedJITConfig, provider)
	})

	s.mu.Lock()
	delete(s.creating, name)
	s.mu.Unlock()

	if err != nil {
		s.tryDeregister(ctx, name)
		if on != "" {
			s.discard(ctx, on, name)
		}

		return types.Runner{}, fmt.Errorf("start runner %q: %w", name, err)
	}
	// Released once the runner is counted as the scale set's, below.
	defer release()

	runner := &types.Runner{
		Name:      name,
		ScaleSet:  s.spec.Name,
		Provider:  on,
		State:     types.RunnerStarting,
		Revision:  s.spec.RunnerRevisions[on],
		CreatedAt: time.Now(),
	}
	if spec := s.spec.RunnerSpecs[on]; spec != nil {
		runner.Size = spec.Describe()
	}

	s.mu.Lock()
	s.runners[name] = runner
	s.mu.Unlock()

	s.logger.Info("runner started",
		slog.String("runner", name),
		slog.String("provider", on),
		slog.String("size", runner.Size))
	s.events.Record(createdEvent(*runner))

	return *runner, nil
}

// discard removes the machine a runner that could not be started may have
// left on a provider. Reconciliation leaves the name alone meanwhile, as for
// any runner removed.
func (s *scaleSet) discard(ctx context.Context, on, name string) {
	s.mu.Lock()
	s.removed[name] = time.Now()
	s.mu.Unlock()

	if err := s.deleteMachine(ctx, on, name); err != nil {
		s.logger.Warn("cannot remove the machine of a runner that could not be started; reconciliation will",
			slog.String("runner", name), slog.String("provider", on), slog.Any("error", err))
	}
}

// machineSpec returns the machine a runner gets on a provider.
func (s *scaleSet) machineSpec(name, jitConfig, on string) types.MachineSpec {
	return types.MachineSpec{
		Name:      name,
		Labels:    types.RunnerLabels(s.installation, s.spec.Name, name, s.spec.RunnerRevisions[on]),
		JITConfig: jitConfig,
		Runner:    s.spec.RunnerSpecs[on],
	}
}

// runnerName returns a new runner's name: the scale set's, and a random suffix
// so that two daemons naming runners at once do not collide.
func runnerName(scaleSet string) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate runner name: %w", err)
	}

	return types.RunnerName(scaleSet, hex.EncodeToString(b[:]))
}
