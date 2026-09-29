// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestErrorsCarryTheStatusLine(t *testing.T) {
	f := newFakePVE(t)
	p := f.open(t)
	p.client.auth = "PVEAPIToken=wrong"

	_, err := p.List(context.Background(), nil)

	var api *apiError
	if !errors.As(err, &api) || api.Status != http.StatusUnauthorized || api.Message != "authentication failure" {
		t.Fatalf("List() = %v, want the status line's authentication failure", err)
	}
}

func TestWaitForATaskThatFails(t *testing.T) {
	f := newFakePVE(t)
	f.polls = 3
	f.exits["qmstop"] = "some failure"
	p := f.open(t)

	f.addVM(&fakeVM{resource: resource{Type: "qemu", Node: "pve1", VMID: 300, Name: "rungar-a", Status: "running"}})

	err := p.Delete(context.Background(), "rungar-a")

	var te *taskError
	if !errors.As(err, &te) || te.ExitStatus != "some failure" {
		t.Fatalf("Delete() = %v, want the stop task's failure", err)
	}
}

func TestTaskNodeIsReadFromTheUPID(t *testing.T) {
	node, err := taskNode("UPID:pve2:000A1B2C:0123ABCD:65B4F1E0:qmclone:9000:rungar@pve!rungar:")
	if err != nil || node != "pve2" {
		t.Errorf("taskNode() = %q, %v; want pve2", node, err)
	}

	if _, err := taskNode("not a upid"); err == nil {
		t.Error("taskNode(not a upid) = nil error")
	}
}

// TestMessagesStandForTheirSentinels pins each phrase the provider recognises
// in Proxmox VE's messages to the sentinel it stands for, as an answer's
// status line and as a task's exit status.
func TestMessagesStandForTheirSentinels(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    error // nil: none
	}{
		{"a VMID taken", "unable to create VM 105: config file already exists", errVMIDTaken},
		{"a template on local storage", "can't clone VM to node 'pve2' (VM uses local storage)", errLocalStorage},
		{"a VM gone", "Configuration file 'nodes/pve1/qemu-server/105.conf' does not exist", errVMNotFound},
		{"no such VM", "no such VM ('105')", errVMNotFound},
		{"a VM not running", "VM 105 not running", errNotRunning},
		{"a guest agent not running", "QEMU guest agent is not running", errNotRunning},
		{"no space left", "clone failed: write error: No space left on device", errOutOfCapacity},
		{"not enough space", "storage 'local-lvm': not enough space", errOutOfCapacity},
		{"out of memory", "start failed: out of memory", errOutOfCapacity},
		{"cannot allocate memory", "kvm: cannot allocate memory", errOutOfCapacity},
		{"not enough memory", "start failed: not enough memory available", errOutOfCapacity},
		{"anything else", "clone failed: storage is locked", nil},
	}

	sentinels := []error{errVMIDTaken, errLocalStorage, errVMNotFound, errNotRunning, errOutOfCapacity}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: http.StatusInternalServerError, Status: "500 " + tt.message}
			errs := map[string]error{
				"answer": errorOf(resp, nil),
				"task":   &taskError{ExitStatus: tt.message, sentinels: sentinelsOf(tt.message)},
			}

			for kind, err := range errs {
				for _, sentinel := range sentinels {
					if got, want := errors.Is(err, sentinel), errors.Is(tt.want, sentinel); got != want {
						t.Errorf("%s %q: errors.Is(%v) = %v, want %v", kind, tt.message, sentinel, got, want)
					}
				}
			}
		})
	}
}

func TestCloseCancelsCallsInFlight(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	c := newClient(server.URL, "PVEAPIToken=x=y", http.DefaultTransport, time.Minute)

	done := make(chan error, 1)
	go func() { done <- c.get(context.Background(), "/version", nil, nil) }()

	c.close()
	c.close() // a second close is harmless

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("get() = %v, want it cancelled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the call was not cancelled by close")
	}
}
