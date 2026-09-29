// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package events

import "github.com/konradasb/rungar/internal/types"

var _ Recorder = (*Log)(nil)

// Recorder records events. Record must not block.
type Recorder interface {
	Record(e types.Event)
}

// Discard is a Recorder that records nothing.
var Discard Recorder = discard{}

type discard struct{}

func (discard) Record(types.Event) {}
