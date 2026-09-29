// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// ageSince returns how long before now t was, or "-" if t is unset.
func ageSince(now time.Time, t *timestamppb.Timestamp) string {
	if t == nil {
		return "-"
	}

	return age(now.Sub(t.AsTime()))
}

// age formats d in its largest whole unit, from seconds to days: "3h".
func age(d time.Duration) string {
	switch {
	case d < 0:
		return "-"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// plural returns n and noun, with noun pluralised unless n is 1: "3 runners".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return strconv.Itoa(n) + " " + noun + "s"
}

// byCount returns one if n is 1, and many otherwise.
func byCount(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return many
}
