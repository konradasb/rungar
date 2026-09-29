// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types

import (
	"fmt"

	"github.com/docker/go-units"
)

// Resources are CPU and memory, for backends that size machines by them. Rungar
// never compares them: a provider says whether a runner fits by creating it or
// refusing it.
type Resources struct {
	VCPUs  int
	Memory Size
}

// String describes r for a person: "2 vCPU, 4 GiB".
func (r Resources) String() string {
	return fmt.Sprintf("%d vCPU, %s", r.VCPUs,
		units.CustomSize("%.4g %s", float64(r.Memory), 1024, binarySizeUnits))
}
