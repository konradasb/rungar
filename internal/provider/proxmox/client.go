// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/konradasb/rungar/internal/provider"
)

const (
	// maxResponseSize bounds how much of an answer is read.
	maxResponseSize = 16 << 20

	// defaultPoll is how often a task or the guest agent is asked again.
	defaultPoll = time.Second
)

// client calls the Proxmox VE API. It is safe for concurrent use.
type client struct {
	// base is the API's root: https://HOST:PORT/api2/json.
	base string

	// auth is the Authorization header: PVEAPIToken=ID=SECRET.
	auth string

	http    *http.Client
	timeout time.Duration

	// poll is how often a task or the guest agent is asked again.
	poll time.Duration

	// inFlight is cancelled by close.
	inFlight provider.InFlight
}

// newClient returns a client of the API at base.
func newClient(base, auth string, transport http.RoundTripper, timeout time.Duration) *client {
	return &client{
		base:    base,
		auth:    auth,
		http:    &http.Client{Transport: transport},
		timeout: timeout,
		poll:    defaultPoll,
	}
}

// close cancels the calls in flight and closes idle connections. It may be
// called more than once.
func (c *client) close() {
	c.inFlight.Cancel()
	c.http.CloseIdleConnections()
}

// apiError is an error the API answered with. Proxmox VE puts its message in
// the status line, and any errors of the request's parameters in the body.
type apiError struct {
	Status  int
	Message string

	// sentinels are the errors its message stands for; see sentinelsOf.
	sentinels []error
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s (%d)", e.Message, e.Status)
}

// Unwrap returns the sentinel errors the message stands for.
func (e *apiError) Unwrap() []error { return e.sentinels }

// get calls GET path with query, decoding the answer's data into out.
func (c *client) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

// post calls POST path with form, decoding the answer's data into out.
func (c *client) post(ctx context.Context, path string, form url.Values, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, form, out)
}

// put calls PUT path with form.
func (c *client) put(ctx context.Context, path string, form url.Values) error {
	return c.do(ctx, http.MethodPut, path, nil, form, nil)
}

// delete calls DELETE path with query, decoding the answer's data into out.
func (c *client) delete(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodDelete, path, query, nil, out)
}

// do makes one call, bounded by the client's timeout and cancelled by close.
// out may be nil.
func (c *client) do(ctx context.Context, method, path string, query, form url.Values, out any) error {
	ctx, release := c.inFlight.Context(ctx)
	defer release()

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	target := c.base + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("read the answer to %s %s: %w", method, path, err)
	}

	if resp.StatusCode/100 != 2 {
		return errorOf(resp, raw)
	}

	if out == nil {
		return nil
	}

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode the answer to %s %s: %w", method, path, err)
	}
	if len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("decode the answer to %s %s: %w", method, path, err)
	}

	return nil
}

// errorOf returns the error an answer carries: the status line's message, and
// the errors of any parameters.
func errorOf(resp *http.Response, raw []byte) error {
	message := strings.TrimSpace(strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode)))
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}

	var body struct {
		Message string            `json:"message"`
		Errors  map[string]string `json:"errors"`
	}
	if json.Unmarshal(raw, &body) == nil {
		var b strings.Builder
		b.WriteString(message)
		if m := strings.TrimSpace(body.Message); m != "" && !strings.Contains(message, m) {
			b.WriteString(": " + m)
		}
		for _, param := range slices.Sorted(maps.Keys(body.Errors)) {
			b.WriteString("; " + param + ": " + strings.TrimSpace(body.Errors[param]))
		}
		message = b.String()
	}

	return &apiError{Status: resp.StatusCode, Message: message, sentinels: sentinelsOf(message)}
}

// task is a task's status.
type task struct {
	Status     string `json:"status"`
	ExitStatus string `json:"exitstatus"`
}

// wait waits for the task upid to end, asking every poll, and returns a
// *taskError carrying its exit status if it failed.
func (c *client) wait(ctx context.Context, upid string) error {
	node, err := taskNode(upid)
	if err != nil {
		return err
	}

	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/status"

	for {
		var t task
		if err := c.get(ctx, path, nil, &t); err != nil {
			return fmt.Errorf("task %s: %w", upid, err)
		}

		if t.Status == "stopped" {
			if t.ExitStatus == "OK" || strings.HasPrefix(t.ExitStatus, "WARNINGS") {
				return nil
			}

			return &taskError{UPID: upid, ExitStatus: t.ExitStatus, sentinels: sentinelsOf(t.ExitStatus)}
		}

		select {
		case <-time.After(c.poll):
		case <-ctx.Done():
			return fmt.Errorf("task %s: %w", upid, ctx.Err())
		}
	}
}

// taskError is a task that ended in failure.
type taskError struct {
	UPID       string
	ExitStatus string

	// sentinels are the errors its exit status stands for; see sentinelsOf.
	sentinels []error
}

func (e *taskError) Error() string {
	return "task " + e.UPID + ": " + e.ExitStatus
}

// Unwrap returns the sentinel errors the exit status stands for.
func (e *taskError) Unwrap() []error { return e.sentinels }

// taskNode returns the node a task runs on, from its UPID:
// UPID:NODE:PID:PSTART:STARTTIME:TYPE:ID:USER:.
func taskNode(upid string) (string, error) {
	parts := strings.Split(upid, ":")
	if len(parts) < 3 || parts[0] != "UPID" || parts[1] == "" {
		return "", fmt.Errorf("%q is not a task ID", upid)
	}

	return parts[1], nil
}

// Proxmox VE's API gives an error no structured code: an answer's status is
// 500 for nearly everything, and what went wrong is said only in English, in
// the status line or a task's exit status. So the errors the provider acts on
// are recognised by phrases in that text here, in this one place, and
// client_test.go pins each phrase to its sentinel. A reworded message in a
// new release fails those tests rather than the provider.
var messagePhrases = []struct {
	phrase   string
	sentinel error
}{
	{"already exists", errVMIDTaken},
	{"local storage", errLocalStorage},
	{"does not exist", errVMNotFound},
	{"no such vm", errVMNotFound},
	{"not running", errNotRunning},
	{"no space left", errOutOfCapacity},
	{"not enough space", errOutOfCapacity},
	{"out of memory", errOutOfCapacity},
	{"cannot allocate memory", errOutOfCapacity},
	{"not enough memory", errOutOfCapacity},
}

// sentinelsOf returns the sentinel errors a message from Proxmox VE stands
// for, matched without regard to case, or nil.
func sentinelsOf(message string) []error {
	text := strings.ToLower(message)

	var sentinels []error
	for _, p := range messagePhrases {
		if strings.Contains(text, p.phrase) && !slices.Contains(sentinels, p.sentinel) {
			sentinels = append(sentinels, p.sentinel)
		}
	}

	return sentinels
}
