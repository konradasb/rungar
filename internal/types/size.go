// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types

import (
	"github.com/docker/go-units"
	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
)

// Size is a number of bytes, written in a configuration as 4GiB or 512MiB, or
// as a plain number of bytes. Every unit is a power of 1024, whether spelled
// GiB or GB, as docker --memory reads it.
type Size int64

// Bytes returns s in bytes.
func (s Size) Bytes() int64 { return int64(s) }

// String formats s as a configuration writes it: "4GiB".
func (s Size) String() string {
	return units.CustomSize("%.4g%s", float64(s), 1024, binarySizeUnits)
}

// binarySizeUnits are the units sizes are formatted in.
var binarySizeUnits = []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}

// UnmarshalYAML reads a size written as bytes or with a unit.
func (s *Size) UnmarshalYAML(value *yaml.Node) error {
	var n int64
	if err := value.Decode(&n); err == nil {
		if n < 0 {
			return errdefs.InvalidArgument("invalid size %d: a size cannot be negative", n)
		}
		*s = Size(n)

		return nil
	}

	bytes, err := units.RAMInBytes(value.Value)
	if err != nil {
		return errdefs.InvalidArgument("invalid size %q: want a size like 512MiB or 4GiB", value.Value)
	}
	if bytes < 0 {
		return errdefs.InvalidArgument("invalid size %q: a size cannot be negative", value.Value)
	}

	*s = Size(bytes)

	return nil
}
