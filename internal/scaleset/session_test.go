// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"errors"
	"strings"
	"testing"

	"github.com/konradasb/rungar/internal/version"
)

func TestSessionOwnerIsDistinctive(t *testing.T) {
	owner := sessionOwner("rungar-vm")

	if owner == "" {
		t.Fatal("sessionOwner() returned nothing")
	}
	if !strings.HasPrefix(owner, "rungar") {
		t.Errorf("sessionOwner() = %q, want it to name Rungar", owner)
	}
}

func TestSystemInfoNamesRungar(t *testing.T) {
	info := systemInfo(7)

	if info.System != "rungar" || info.ScaleSetID != 7 || info.Version != version.Version {
		t.Errorf("systemInfo(7) = %+v, want Rungar's, of scale set 7", info)
	}
}

func TestIsSessionConflict(t *testing.T) {
	conflict := errors.New(`failed to create message session: request POST https://broker.actions.githubusercontent.com/` +
		`rest/_apis/runtime/runnerscalesets/3/sessions failed(status="409 Conflict"): unexpected status code 409 ` +
		`Conflict: GitHub.Actions.Runtime.WebApi.RunnerScaleSetSessionConflictException, GitHub.Actions.Runtime.WebApi: ` +
		`The actions runner scaleset rungar-c2-m4 already has an active session.`)

	if !isSessionConflict(conflict) {
		t.Error("GitHub's refusal of a second session was not recognised")
	}
	if isSessionConflict(errors.New("401 Unauthorized")) {
		t.Error("another failure was taken for a session conflict")
	}
}
