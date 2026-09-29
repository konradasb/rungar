// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/events"
	"github.com/konradasb/rungar/internal/types"
)

// ageEnded has the leaving runner's machine found ended longer than d ago.
func ageEnded(t *testing.T, s *scaleSet, name string, d time.Duration) {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	l, ok := s.leaving[name]
	if !ok {
		t.Fatalf("runner %s is not leaving", name)
	}
	l.endedAt = l.endedAt.Add(-d - time.Second)
}

// isLeaving reports whether the runner is leaving the scale set.
func isLeaving(s *scaleSet, name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.leaving[name]
	return ok
}

// TestAnEndedRunnersOutcomeFollowsGitHub checks a runner whose machine ended
// is taken as having completed its job once GitHub no longer has it, as lost
// once GitHub still has it after the start timeout, and as undecided before.
func TestAnEndedRunnersOutcomeFollowsGitHub(t *testing.T) {
	const timeout = time.Minute

	tests := []struct {
		name     string
		status   types.GitHubStatus
		endedFor time.Duration
		want     outcome
	}{
		{"unregistered at once", types.GitHubNotRegistered, 0, outcome{removal: types.RemovalJobCompleted}},
		{"unregistered late", types.GitHubNotRegistered, 2 * timeout, outcome{removal: types.RemovalJobCompleted}},
		{"offline within the timeout", types.GitHubOffline, timeout, outcome{}},
		{"busy within the timeout", types.GitHubBusy, timeout / 2, outcome{}},
		{"idle within the timeout", types.GitHubIdle, 0, outcome{}},
		{"offline after the timeout", types.GitHubOffline, timeout + time.Second, outcome{loss: types.LossEnded}},
		{"busy after the timeout", types.GitHubBusy, timeout + time.Second, outcome{loss: types.LossEnded}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := endedOutcome(tt.status, tt.endedFor, timeout); got != tt.want {
				t.Errorf("endedOutcome(%s, %s) = %+v, want %+v", tt.status, tt.endedFor, got, tt.want)
			}
		})
	}
}

// TestAnEndedRunnerReadsTheSameWhicheverWayItsMachineEnded checks a runner's
// outcome is decided by GitHub, not by how its provider ends machines: each
// story is recorded the same whether the provider lists the ended machine as
// stopped or no longer lists it.
func TestAnEndedRunnerReadsTheSameWhicheverWayItsMachineEnded(t *testing.T) {
	endings := []struct {
		name string
		end  func(f *fakeScaleSetProviders)
	}{
		{"stopped", func(f *fakeScaleSetProviders) {
			f.mu.Lock()
			defer f.mu.Unlock()
			for i := range f.machines["a"] {
				f.machines["a"][i].State = types.MachineStopped
			}
		}},
		{"vanished", func(f *fakeScaleSetProviders) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.machines["a"] = nil
		}},
	}

	stories := []struct {
		name string

		// github is what GitHub says of the runner once its machine ended,
		// and aged whether the start timeout has passed since.
		github statusFunc
		aged   bool

		want                []happened
		removed             []types.RemovalReason
		lost                []types.LossReason
		registrationRemoved bool
	}{
		{
			name:    "its job completed",
			github:  registered(nil),
			want:    []happened{{action: events.ActionRemoved, reason: string(types.RemovalJobCompleted)}},
			removed: []types.RemovalReason{types.RemovalJobCompleted},
		},
		{
			name:                "it crashed",
			github:              says(true, false),
			aged:                true,
			want:                []happened{{action: events.ActionLost, reason: string(types.LossEnded)}},
			lost:                []types.LossReason{types.LossEnded},
			registrationRemoved: true,
		},
		{
			name: "it crashed running a job",
			github: func(context.Context, string) (answer, error) {
				return answer{Registered: true, Busy: true}, nil
			},
			aged: true,
			want: []happened{{action: events.ActionLost, reason: string(types.LossEnded)}},
			lost: []types.LossReason{types.LossEnded},
		},
		{
			name:   "GitHub has not caught up",
			github: says(true, true),
		},
		{
			name: "GitHub cannot be asked",
			github: func(context.Context, string) (answer, error) {
				return answer{}, errors.New("rate limited")
			},
			aged: true,
		},
	}

	for _, ending := range endings {
		for _, story := range stories {
			t.Run(ending.name+"/"+story.name, func(t *testing.T) {
				fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
				d := &registrationRemovals{}
				var gh *fakeGitHub
				s := newTestScaleSet(fleet, removingRegistrationsWith(d), func(o *fakeGitHub) { gh = o })
				ctx := context.Background()

				r, err := s.createRunner(ctx)
				if err != nil {
					t.Fatal(err)
				}
				before := len(recordedBy(t, s.events))

				gh.status = story.github
				ending.end(fleet)

				if err := s.reconcileFleet(ctx); err != nil {
					t.Fatal(err)
				}
				if s.runnerCount() != 0 {
					t.Errorf("runnerCount() = %d, want 0: a runner whose machine ended is replaced at once",
						s.runnerCount())
				}
				if story.aged && isLeaving(s, r.Name) {
					ageEnded(t, s, r.Name, s.spec.StartTimeout)
					if err := s.reconcileFleet(ctx); err != nil {
						t.Fatal(err)
					}
				}

				want := slices.Clone(story.want)
				for i := range want {
					want[i].name = r.Name
				}
				if got := happenings(recordedBy(t, s.events)[before:]); !slices.Equal(got, want) {
					t.Errorf("events = %+v, want %+v", got, want)
				}
				if got := removals(t, s); !slices.Equal(got, story.removed) {
					t.Errorf("counted removals %v, want %v", got, story.removed)
				}
				if got := losses(t, s); !slices.Equal(got, story.lost) {
					t.Errorf("counted losses %v, want %v", got, story.lost)
				}
				if got := d.got(); slices.Contains(got, r.Name) != story.registrationRemoved {
					t.Errorf("removed registrations %v; want that of %s removed: %v",
						got, r.Name, story.registrationRemoved)
				}
				if fleet.providerOf(r.Name) != "" {
					t.Error("the ended machine was left on the provider")
				}
				if decided := len(story.want) > 0; isLeaving(s, r.Name) == decided {
					t.Errorf("leaving = %v, want %v", !decided, decided)
				}
			})
		}
	}
}

// TestJobCompletionSettlesARunnerWhoseMachineEnded checks a job's completion,
// arriving after its runner's machine was found ended, decides the outcome
// without waiting for GitHub to drop the registration.
func TestJobCompletionSettlesARunnerWhoseMachineEnded(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	s := newTestScaleSet(fleet, runningAJob)
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := len(recordedBy(t, s.events))

	fleet.mu.Lock()
	fleet.machines["a"] = nil
	fleet.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if !isLeaving(s, r.Name) {
		t.Fatal("the runner was settled while GitHub still had it running a job")
	}

	if err := s.remove(ctx, r.Name, types.RemovalJobCompleted, false); err != nil {
		t.Fatalf("remove() = %v", err)
	}

	want := []happened{{action: events.ActionRemoved, name: r.Name, reason: string(types.RemovalJobCompleted)}}
	if got := happenings(recordedBy(t, s.events)[before:]); !slices.Equal(got, want) {
		t.Errorf("events = %+v, want %+v", got, want)
	}
	if isLeaving(s, r.Name) {
		t.Error("the runner is still leaving")
	}
}

// TestARemovalIsRecordedOnceItsMachineIsDeleted checks a runner whose machine
// cannot be deleted is kept leaving, neither counted nor adopted again, and
// recorded with its reason once the machine is deleted.
func TestARemovalIsRecordedOnceItsMachineIsDeleted(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	s := newTestScaleSet(fleet, statusWith(says(true, true)))
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := len(recordedBy(t, s.events))

	fleet.deleteErr = errors.New("permission denied")
	if err := s.remove(ctx, r.Name, types.RemovalScaledDown, false); err == nil {
		t.Fatal("remove() = nil, want the error deleting the machine")
	}

	s.mu.Lock()
	s.left = map[string]time.Time{}
	s.mu.Unlock()
	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}
	if s.runnerCount() != 0 {
		t.Errorf("runnerCount() = %d, want 0: the machine is leaving, not adopted again", s.runnerCount())
	}
	if got := recordedBy(t, s.events)[before:]; len(got) != 0 {
		t.Errorf("events = %+v, want none before the machine is deleted", happenings(got))
	}

	fleet.deleteErr = nil
	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}

	want := []happened{{action: events.ActionRemoved, name: r.Name, reason: string(types.RemovalScaledDown)}}
	if got := happenings(recordedBy(t, s.events)[before:]); !slices.Equal(got, want) {
		t.Errorf("events = %+v, want %+v", got, want)
	}
	if got := removals(t, s); !slices.Equal(got, []types.RemovalReason{types.RemovalScaledDown}) {
		t.Errorf("counted removals %v, want [scaled_down]", got)
	}
	if fleet.providerOf(r.Name) != "" {
		t.Error("the machine was left on the provider")
	}
}

// TestALeavingRunnerIsNotAnOrphan checks the registration of a runner whose
// machine ended is left for the runner to settle, not removed as an orphan's.
func TestALeavingRunnerIsNotAnOrphan(t *testing.T) {
	fleet := newFakeScaleSetProviders(providerRoom{name: "a", vcpus: 8, memoryGiB: 16})
	d := &registrationRemovals{}
	var gh *fakeGitHub
	s := newTestScaleSet(fleet, removingRegistrationsWith(d), statusWith(says(true, false)),
		func(o *fakeGitHub) { gh = o })
	ctx := context.Background()

	r, err := s.createRunner(ctx)
	if err != nil {
		t.Fatal(err)
	}

	gh.mu.Lock()
	gh.offline = []string{r.Name}
	gh.mu.Unlock()

	fleet.mu.Lock()
	fleet.machines["a"] = nil
	fleet.mu.Unlock()

	if err := s.reconcileFleet(ctx); err != nil {
		t.Fatal(err)
	}

	s.mu.Lock()
	_, orphaned := s.orphans[r.Name]
	s.mu.Unlock()

	if orphaned {
		t.Error("the leaving runner's registration was taken for an orphan's")
	}
	if got := d.got(); len(got) != 0 {
		t.Errorf("removed registrations %v, want nothing yet", got)
	}
}
