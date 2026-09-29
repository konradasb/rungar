// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package docker

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

// TestAgainstDocker makes, lists and removes a container on the Docker daemon
// of this machine. It runs only with RUNGAR_DOCKER_TEST=1, against DOCKER_HOST
// if it is set, and pulls alpine:3.20 if the host lacks it.
func TestAgainstDocker(t *testing.T) {
	if os.Getenv("RUNGAR_DOCKER_TEST") != "1" {
		t.Skip("set RUNGAR_DOCKER_TEST=1 to run against this machine's Docker daemon")
	}

	doc := ""
	if host := os.Getenv("DOCKER_HOST"); host != "" {
		doc = "address: " + host
	}
	p := open(t, configure(t, doc))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	name := "rungar-docker-test-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	labels := map[string]string{"rungar.sh/managed": "true", "rungar.sh/test": name}

	spec := runner(t, "image: alpine:3.20\ncommand: [sleep, '30']\ncpus: 0.5\nmemory: 128MiB")
	machine := types.MachineSpec{Name: name, Labels: labels, JITConfig: "not-a-registration", Runner: spec}

	if err := p.Create(ctx, machine); err != nil {
		t.Fatalf("Create() = %v", err)
	}
	t.Cleanup(func() { _ = p.Delete(context.Background(), name) })

	machines, err := p.List(ctx, labels)
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(machines) != 1 || machines[0].Name != name || !machines[0].State.Alive() {
		t.Fatalf("List() = %+v, want the one container, alive", machines)
	}
	if machines[0].Labels["rungar.sh/test"] != name {
		t.Errorf("labels = %v, want Rungar's back", machines[0].Labels)
	}

	if err := p.Delete(ctx, name); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if err := p.Delete(ctx, name); err != nil {
		t.Errorf("Delete() of a container gone = %v, want nil", err)
	}

	machines, err = p.List(ctx, labels)
	if err != nil || len(machines) != 0 {
		t.Errorf("List() after Delete() = %+v, %v; want nothing", machines, err)
	}
}
