// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package types

// CallResult is how a call to a provider ended, as the metrics label it.
type CallResult string

const (
	// CallOK is a call that succeeded.
	CallOK CallResult = "ok"

	// CallNoCapacity is a call refused because the provider, or every
	// provider tried, had no room: an errdefs.ErrNoCapacity error.
	CallNoCapacity CallResult = "no_capacity"

	// CallError is a call that failed for any other reason.
	CallError CallResult = "error"
)
