// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"time"

	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/types"
)

// discardEvents is an EventRecorder that records nothing.
type discardEvents struct{}

func (discardEvents) Record(events.Event) {}

// discardMetrics is a MetricsRecorder that records nothing.
type discardMetrics struct{}

func (discardMetrics) SetPaused(string, bool)                                    {}
func (discardMetrics) SetMinRunners(string, int)                                 {}
func (discardMetrics) SetDesiredRunners(string, int)                             {}
func (discardMetrics) CountRunnerCreated(string, string)                         {}
func (discardMetrics) CountRunnerRemoved(string, string, types.RemovalReason)    {}
func (discardMetrics) CountRunnerLost(string, string, types.LossReason)          {}
func (discardMetrics) CountScaleUpFailed(string, types.CallResult)               {}
func (discardMetrics) CountJobStarted(string)                                    {}
func (discardMetrics) CountJobCompleted(string, string)                          {}
func (discardMetrics) SetJobsAssigned(string, int)                               {}
func (discardMetrics) ObserveJobWait(string, time.Duration, time.Duration)       {}
func (discardMetrics) ObserveRunnerCreateDuration(string, string, time.Duration) {}
func (discardMetrics) ObserveRunnerBootDuration(string, string, time.Duration)   {}
func (discardMetrics) SetLastPoll(string, time.Time)                             {}
func (discardMetrics) ObserveReconcile(string, time.Time, time.Duration, bool)   {}
func (discardMetrics) SetRunners(string, []types.Runner)                         {}
func (discardMetrics) SetProviderReachability([]types.ProviderSnapshot)          {}
