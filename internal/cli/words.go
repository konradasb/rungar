// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

// The words of the status columns, which palette.status colours. A word
// shared by more than one column is named status*; the rest are named for the
// column they are in.
const (
	// statusOK marks a working provider, and working credentials.
	statusOK = "OK"
	// statusUnreachable marks a provider, or a scale set's GitHub, that
	// cannot be reached.
	statusUnreachable = "UNREACHABLE"
)

// Words of a scale set's STATUS column.
const (
	scaleSetListening   = "LISTENING"
	scaleSetStarting    = "STARTING"
	scaleSetWaiting     = "WAITING"
	scaleSetHoldingBack = "HOLDING BACK"
	scaleSetPaused      = "PAUSED"

	// scaleSetLeftover is a scale set no longer configured whose runners are
	// still on the fleet.
	scaleSetLeftover = "LEFTOVER"
)

// Words of a provider's STATUS column.
const (
	providerDisabled = "DISABLED"
	providerDraining = "DRAINING"
	providerHeld     = "HELD"
)

// Words of a runner's STATE, GITHUB and MACHINE columns.
const (
	runnerStarting = "Starting"
	runnerIdle     = "Idle"
	runnerBusy     = "Busy"

	gitHubBusy         = "Busy"
	gitHubIdle         = "Idle"
	gitHubOffline      = "Offline"
	gitHubUnregistered = "Not registered"

	// machineStarting is the same word as runnerStarting, and coloured
	// with it.
	machineStarting = "Starting"
	machineRunning  = "Running"
	machineStopped  = "Stopped"
)

// Words of an event's kind and action.
const (
	eventRunner   = "Runner"
	eventProvider = "Provider"
	eventScaleSet = "Scale set"

	eventCreated    = "Created"
	eventAdopted    = "Adopted"
	eventConnected  = "Connected"
	eventJobStarted = "Job started"
	eventRemoved    = "Removed"
	eventLost       = "Lost"
	eventFull       = "Full"
	eventFailing    = "Failing"
	eventPaused     = "Paused"
	eventResumed    = "Resumed"

	eventMinRunnersChanged = "Min runners changed"
)
