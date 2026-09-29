// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types

// Labels on every machine Rungar creates. The fleet is Rungar's only record of
// its runners: a daemon finds them again by these.
const (
	// LabelPrefix starts the key of every label Rungar sets.
	LabelPrefix = "rungar.sh/"

	// LabelManaged marks a machine as Rungar's.
	LabelManaged = LabelPrefix + "managed"

	// LabelInstallation is the runner's installation, which keeps two
	// installations sharing a fleet from adopting each other's runners.
	LabelInstallation = LabelPrefix + "installation"

	// LabelScaleSet is the scale set the runner belongs to.
	LabelScaleSet = LabelPrefix + "scale-set"

	// LabelRunner is the runner's name.
	LabelRunner = LabelPrefix + "runner"

	// LabelRevision is the revision of the runner spec the machine was created
	// from; see RunnerRevision.
	LabelRevision = LabelPrefix + "revision"
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

// HasLabels reports whether labels carry every label in selector.
func HasLabels(labels, selector map[string]string) bool {
	for k, v := range selector {
		if got, ok := labels[k]; !ok || got != v {
			return false
		}
	}

	return true
}
