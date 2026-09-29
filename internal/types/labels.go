// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types

// Labels on every machine Rungar makes. The fleet is Rungar's only record of its
// runners: a daemon finds them again by these.
const (
	// LabelManaged marks a machine as Rungar's.
	LabelManaged = "rungar.sh/managed"

	// LabelInstallation is the runner's installation, which keeps two
	// installations sharing a fleet from adopting each other's runners.
	LabelInstallation = "rungar.sh/installation"

	// LabelScaleSet is the scale set the runner belongs to.
	LabelScaleSet = "rungar.sh/scale-set"

	// LabelRunner is the runner's name.
	LabelRunner = "rungar.sh/runner"

	// LabelRevision is the revision of the runner spec the machine was made
	// from; see RunnerRevision.
	LabelRevision = "rungar.sh/revision"
)

// ScaleSetSelector returns the labels every machine of a scale set carries.
func ScaleSetSelector(installation, scaleSet string) map[string]string {
	return map[string]string{
		LabelManaged:      "true",
		LabelInstallation: installation,
		LabelScaleSet:     scaleSet,
	}
}

// RunnerLabels returns the labels a runner's machine carries.
func RunnerLabels(installation, scaleSet, runner, revision string) map[string]string {
	labels := ScaleSetSelector(installation, scaleSet)
	labels[LabelRunner] = runner
	labels[LabelRevision] = revision

	return labels
}

// Matches reports whether labels carry every label in selector.
func Matches(labels, selector map[string]string) bool {
	for k, v := range selector {
		if got, ok := labels[k]; !ok || got != v {
			return false
		}
	}

	return true
}
