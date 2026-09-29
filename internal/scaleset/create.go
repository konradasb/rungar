// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/types"
)

// createRunner registers a runner with GitHub and has the fleet create its
// machine on the first provider that takes it. A registration no machine was
// created for is removed again.
func (s *scaleSet) createRunner(ctx context.Context) (types.Runner, error) {
	start := s.now()

	// Nothing is registered with GitHub when no provider would be tried.
	if err := s.providers.CanPlace(); err != nil {
		return types.Runner{}, err
	}

	name, err := runnerName(s.spec.Name)
	if err != nil {
		return types.Runner{}, err
	}

	// Marked before registering, so removeOrphans never takes it for one.
	s.mu.Lock()
	s.creating[name] = true
	s.mu.Unlock()

	jit, err := s.github.GenerateJitRunnerConfig(ctx,
		&ghscaleset.RunnerScaleSetJitRunnerSetting{Name: name}, int(s.id.Load()))
	if err != nil {
		s.mu.Lock()
		delete(s.creating, name)
		s.mu.Unlock()

		return types.Runner{}, fmt.Errorf("register runner %q with GitHub: %w", name, err)
	}

	// A machine not created within the start timeout would be written off.
	createCtx, cancel := context.WithTimeout(ctx, s.spec.StartTimeout)
	defer cancel()

	provider, release, err := s.providers.Place(createCtx, s.runnersByProvider(),
		func(candidate string) types.MachineSpec {
			return s.machineSpec(name, jit.EncodedJITConfig, candidate)
		})
	if err != nil {
		// Kept from adoption in the same step as it stops being created, as
		// for any runner removed, should a provider have left its machine.
		s.mu.Lock()
		delete(s.creating, name)
		if provider != "" {
			s.left[name] = s.now()
		}
		s.mu.Unlock()

		s.tryRemoveRegistration(ctx, name)
		if provider != "" {
			s.deleteStrandedMachine(ctx, provider, name)
		}

		return types.Runner{}, fmt.Errorf("create runner %q: %w", name, err)
	}
	// Released once the runner is counted as the scale set's, below.
	defer release()

	runner := types.Runner{
		Name:      name,
		ScaleSet:  s.spec.Name,
		Provider:  provider,
		State:     types.RunnerStarting,
		Revision:  s.spec.RunnerRevisions[provider],
		CreatedAt: s.now(),
	}
	if spec := s.spec.RunnerSpecs[provider]; spec != nil {
		runner.Size = spec.Describe()
	}

	// Counted in the same step as it stops being created, so that
	// reconcileFleet never finds it untracked and adopts it. The scale set
	// keeps a copy of its own, which others change under mu.
	tracked := runner
	s.mu.Lock()
	delete(s.creating, name)
	s.runners[name] = &tracked
	s.mu.Unlock()

	createDuration := runner.CreatedAt.Sub(start)

	s.logger.Info("runner created",
		slog.String("runner", name),
		slog.String("provider", provider),
		slog.String("size", runner.Size),
		slog.Duration("create_duration", createDuration))
	s.metrics.ObserveRunnerCreateDuration(s.spec.Name, provider, createDuration)
	s.events.Record(createdEvent(runner, createDuration))

	return runner, nil
}

// deleteStrandedMachine deletes the machine a runner that could not be
// created may have left on a provider. The caller has put the runner among
// those that have left, so that reconciliation leaves the name alone
// meanwhile.
func (s *scaleSet) deleteStrandedMachine(ctx context.Context, provider, name string) {
	if err := s.deleteMachine(ctx, provider, name); err != nil {
		s.logger.Warn("cannot delete the machine of a runner that could not be created; reconciliation will",
			slog.String("runner", name), slog.String("provider", provider), slog.Any("error", err))
	}
}

// deadlineGrace is how long after max_age and a reconcile pass a machine's
// deadline falls, for Rungar to remove the runner first: a provider deleting
// the machine would have the runner taken for lost rather than expired.
const deadlineGrace = 10 * time.Minute

// machineSpec returns the machine a runner gets on a provider.
func (s *scaleSet) machineSpec(name, jitConfig, provider string) types.MachineSpec {
	spec := types.MachineSpec{
		Name:      name,
		Labels:    types.RunnerLabels(s.installation, s.spec.Name, name, s.spec.RunnerRevisions[provider]),
		JITConfig: jitConfig,
		Runner:    s.spec.RunnerSpecs[provider],
	}
	if s.spec.MaxAge > 0 {
		spec.Deadline = s.now().Add(s.spec.MaxAge + s.interval + deadlineGrace)
	}

	return spec
}

// runnerSuffixBytes is how many random bytes end a runner's name, in hex.
const runnerSuffixBytes = 4

// runnerName returns a new runner's name: the scale set's, and a random suffix
// so that two daemons naming runners at once do not collide.
func runnerName(scaleSet string) (string, error) {
	var b [runnerSuffixBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate runner name: %w", err)
	}

	return types.RunnerName(scaleSet, hex.EncodeToString(b[:]))
}

// isRunnerNameOf reports whether runnerName could have given name to a runner
// of scaleSet.
func isRunnerNameOf(scaleSet, name string) bool {
	blank := strings.Repeat("0", 2*runnerSuffixBytes)

	want, err := types.RunnerName(scaleSet, blank)
	if err != nil {
		return false
	}

	suffix, ok := strings.CutPrefix(name, strings.TrimSuffix(want, blank))
	if !ok || len(suffix) != len(blank) {
		return false
	}

	_, err = hex.DecodeString(suffix)

	return err == nil
}
