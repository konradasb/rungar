// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types

import (
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
)

// ScaleSetSpec is a configured scale set: a GitHub runner scale set, and the
// runners Rungar makes for it.
type ScaleSetSpec struct {
	// Name is the scale set's name on GitHub, and the label workflows
	// target it by. A daemon starting looks it up by name and adopts the
	// scale set GitHub already has.
	Name string `yaml:"name"`

	// Labels are further labels workflows may target it by, beside its
	// name. They need a feature flag on GitHub Enterprise Server before
	// 3.21. GitHub keeps the labels a scale set was created with, so
	// changing them does nothing to one that exists: delete it on GitHub
	// to have Rungar create it with these.
	Labels []string `yaml:"labels,omitempty"`

	// RunnerGroup is the runner group the scale set belongs to, which
	// decides which repositories may use it. Unset is GitHub's "Default"
	// group.
	RunnerGroup string `yaml:"runner_group,omitempty"`

	// MinRunners are kept idle and ready with nothing queued, so that a job
	// does not wait for a machine to boot. Unset is 0; it may not be more
	// than max_runners.
	MinRunners int `yaml:"min_runners,omitempty"`

	// MaxRunners is the most runners alive at once, across every provider.
	// It is also the capacity reported to GitHub, which stops GitHub
	// assigning more jobs than the scale set can run. It is required, and
	// at least 1.
	MaxRunners int `yaml:"max_runners"`

	// Priority decides who gets the providers when there is not room for
	// everyone. Higher wins, and scale sets of the same priority take turns.
	// A scale set that finds a provider full holds lower priorities back from
	// it for a while, so that the next room freed there is its own, rather
	// than filled by smaller runners. Nothing is reserved in advance, and a
	// running runner is never taken away.
	Priority int `yaml:"priority,omitempty"`

	// Providers are what the scale set's runners are placed on, in order of
	// preference: a name, or a name and a runner block for that provider
	// alone, written over the provider's.
	//
	//	providers:
	//	  - compute1
	//	  - name: compute2
	//	    runner: {vcpus: 8, memory: 16GiB}
	Providers []ProviderRef `yaml:"providers"`

	// Placement is the order the providers are tried in: spread tries first
	// the one with the fewest of the scale set's runners, which evens them
	// out; pack tries them in the order written, which fills them one after
	// another. A provider that is full refuses, and the next is tried. Unset
	// is spread.
	Placement Placement `yaml:"placement,omitempty"`

	// StartTimeout is how long a runner has to connect to GitHub before it
	// is replaced: a new runner from when it was made, an idle one from when
	// it was first found disconnected. It does not bound a job. Unset is 5m;
	// at least 30s.
	StartTimeout time.Duration `yaml:"start_timeout,omitempty"`

	// MaxIdleAge is how old a runner not running a job may get before it is
	// replaced, one at a time. It keeps a reserve from running jobs on an
	// image long out of date, as with a moving tag such as latest. Unset
	// is never; at least start_timeout.
	MaxIdleAge time.Duration `yaml:"max_idle_age,omitempty"`

	// MaxAge is how old any runner may get before it is removed, even one
	// running a job, which fails. It bounds a job that hangs. Unset is
	// never; at least start_timeout.
	MaxAge time.Duration `yaml:"max_age,omitempty"`

	// Paused keeps the scale set from taking jobs: GitHub is told it has no
	// room, no runner is made, not even min_runners, its idle runners are
	// removed and its busy ones are left to finish. Its jobs wait on GitHub,
	// which cancels one left queued for a day. rungar scale-sets pause and
	// rungar scale-sets resume change it until the daemon restarts.
	Paused bool `yaml:"paused,omitempty"`

	// RunnerSpecs maps each provider to the runner as it parsed the merged
	// blocks: the provider's own, then the scale set's for that provider
	// over it. Set when the configuration is loaded.
	RunnerSpecs map[string]RunnerSpec `yaml:"-"`

	// RunnerRevisions maps each provider to the RunnerRevision of its
	// RunnerSpecs entry. A runner of another revision is replaced once idle.
	RunnerRevisions map[string]string `yaml:"-"`
}

// ProviderRef is one of a scale set's providers, with the runner block for the
// scale set's runners on it. Without a block it is written as the name.
type ProviderRef struct {
	// Name is the provider's name.
	Name string `yaml:"name"`

	// RunnerBlock is what the scale set's runners on this provider are made
	// of, in the provider type's terms: a size, say. It is written over the
	// provider's own runner block, so it need only say what differs.
	RunnerBlock yaml.Node `yaml:"runner,omitempty"`
}

// UnmarshalYAML reads a provider as a name or as a mapping.
func (r *ProviderRef) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&r.Name)
	}

	type plain ProviderRef

	return node.Decode((*plain)(r))
}

// DefaultRunnerGroup is GitHub's default runner group, open to every
// repository.
const DefaultRunnerGroup = "Default"

// MinStartTimeout is the shortest start_timeout: any shorter and runners would
// be replaced before they could boot.
const MinStartTimeout = 30 * time.Second

// ProviderNames returns the names of the scale set's providers, in order.
func (s ScaleSetSpec) ProviderNames() []string {
	names := make([]string, 0, len(s.Providers))
	for _, p := range s.Providers {
		names = append(names, p.Name)
	}

	return names
}

// AllLabels returns every label workflows may target the scale set by: its
// name, then its labels, without duplicates. Labels are compared ignoring
// case, as GitHub does.
func (s ScaleSetSpec) AllLabels() []string {
	labels := make([]string, 0, len(s.Labels)+1)
	seen := map[string]bool{}

	for _, label := range append([]string{s.Name}, s.Labels...) {
		key := strings.ToLower(label)
		if label == "" || seen[key] {
			continue
		}
		seen[key] = true
		labels = append(labels, label)
	}

	return labels
}

// RunsOn returns the workflow line that targets the scale set, quoted where
// YAML needs it. It uses the name alone, which is always one of the labels;
// the others are whatever GitHub kept from the scale set's creation.
func (s ScaleSetSpec) RunsOn() string {
	b, err := yaml.Marshal(map[string]string{"runs-on": s.Name})
	if err != nil {
		return "runs-on: " + s.Name
	}

	return strings.TrimSpace(string(b))
}

// Validate reports whether the scale set can be run.
func (s ScaleSetSpec) Validate() error {
	switch {
	case s.Name == "":
		return errdefs.InvalidArgument("a scale set needs a name, which is the label workflows target it by")
	case strings.ContainsAny(s.Name, " \t"):
		return errdefs.InvalidArgument("invalid scale set name %q: a label may not contain whitespace", s.Name)
	case s.MaxRunners <= 0:
		return errdefs.InvalidArgument("scale set %q: max_runners must be at least 1", s.Name)
	case s.MinRunners < 0:
		return errdefs.InvalidArgument("scale set %q: min_runners cannot be negative", s.Name)
	case s.MinRunners > s.MaxRunners:
		return errdefs.InvalidArgument("scale set %q: min_runners (%d) is more than max_runners (%d)",
			s.Name, s.MinRunners, s.MaxRunners)
	case len(s.Providers) == 0:
		return errdefs.InvalidArgument("scale set %q: providers is required: what its runners are placed on",
			s.Name)
	case s.StartTimeout < MinStartTimeout:
		return errdefs.InvalidArgument("scale set %q: start_timeout must be at least %s, the time a runner "+
			"has to boot and connect", s.Name, MinStartTimeout)
	case s.MaxIdleAge != 0 && s.MaxIdleAge < s.StartTimeout:
		return errdefs.InvalidArgument("scale set %q: max_idle_age (%s) is less than start_timeout (%s): "+
			"runners would be replaced before they could connect", s.Name, s.MaxIdleAge, s.StartTimeout)
	case s.MaxAge != 0 && s.MaxAge < s.StartTimeout:
		return errdefs.InvalidArgument("scale set %q: max_age (%s) is less than start_timeout (%s): "+
			"runners would be removed before they could connect", s.Name, s.MaxAge, s.StartTimeout)
	}

	// Runners are named after the scale set.
	if _, err := RunnerName(s.Name, "00000000"); err != nil {
		return err
	}

	if err := s.Placement.Validate(); err != nil {
		return errdefs.InvalidArgument("scale set %q: %s", s.Name, err)
	}

	seen := map[string]bool{}
	for _, p := range s.Providers {
		switch {
		case p.Name == "":
			return errdefs.InvalidArgument("scale set %q: a provider needs a name", s.Name)
		case seen[p.Name]:
			return errdefs.InvalidArgument("scale set %q: provider %q is listed twice", s.Name, p.Name)
		}
		seen[p.Name] = true
	}

	return nil
}

// ScaleSet is a scale set as configured, and as the daemon and GitHub have it.
// One not configured, found only by its runners or on GitHub, has a Spec of
// its name alone.
type ScaleSet struct {
	Spec   ScaleSetSpec
	Status ScaleSetStatus
}

// ScaleSetStatus is what the daemon is doing with a scale set, and what GitHub
// has of it.
type ScaleSetStatus struct {
	Configured bool
	Phase      ScaleSetPhase

	// Paused reports whether the scale set is paused now, which Spec.Paused
	// says only of when the daemon started.
	Paused bool

	// Desired is how many runners GitHub's latest statistics call for; it is
	// meaningful only when DesiredKnown.
	Desired      int
	DesiredKnown bool

	// Runners are the runners the daemon knows of.
	Runners []Runner

	// HoldingBackReason says why the scale set holds back from a provider for
	// a higher priority, or is empty.
	HoldingBackReason string

	// FleetRunners is how many of its runners the providers list.
	FleetRunners int

	// GitHub is nil when GitHub was not asked.
	GitHub *GitHubScaleSet
}

// ScaleSetPhase is how far a configured scale set is from taking jobs.
type ScaleSetPhase string

// The phases a configured scale set passes through.
const (
	// ScaleSetStarting is a scale set being found or made on GitHub, and its
	// runners adopted.
	ScaleSetStarting ScaleSetPhase = "starting"

	// ScaleSetWaitingForSession is a scale set waiting for another daemon's
	// message session on it to end.
	ScaleSetWaitingForSession ScaleSetPhase = "waiting-for-session"

	// ScaleSetListening is a scale set taking jobs.
	ScaleSetListening ScaleSetPhase = "listening"
)

// GitHubScaleSet is what GitHub has of a scale set.
type GitHubScaleSet struct {
	// Found reports whether GitHub has a scale set of that name in the
	// runner group, and Err why GitHub could not be asked.
	Found bool
	Err   error

	ID          int
	RunnerGroup string
	Labels      []string
	CreatedAt   time.Time

	// Statistics is nil when GitHub gave none.
	Statistics *ScaleSetStatistics
}

// ScaleSetStatistics are GitHub's counts of a scale set's jobs and runners.
type ScaleSetStatistics struct {
	AssignedJobs      int
	RunningJobs       int
	RegisteredRunners int
	BusyRunners       int
	IdleRunners       int
}

// ScaleSetList is the scale sets, and the providers that could not be listed
// for their runners mapped to why.
type ScaleSetList struct {
	Items       []ScaleSet
	Unreachable map[string]error
}

// ScaleSetRemoval is the result of one attempt at removing a scale set: the
// runners removed, how many were left running jobs and, once none are, the ID
// of the scale set removed from GitHub, or 0 if GitHub had none.
type ScaleSetRemoval struct {
	Removed    []Runner
	BusyLeft   int
	ScaleSetID int
}
