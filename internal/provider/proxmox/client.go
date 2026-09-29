// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package proxmox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// client calls the Proxmox VE API.
type client struct {
	// base is the API's root: https://HOST:PORT/api2/json.
	base string

	// auth is the Authorization header: PVEAPIToken=ID=SECRET.
	auth string

	http    *http.Client
	timeout time.Duration

	// closed is cancelled by Close, cancelling every call in flight.
	closed context.Context
}

// apiError is an error the API answered with. Proxmox VE puts its message in
// the status line, and any errors of the request's parameters in the body.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("proxmox: %s (%d)", e.Message, e.Status)
}

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

// do makes one call, bounded by the client's timeout. out may be nil.
func (c *client) do(ctx context.Context, method, path string, query, form url.Values, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	// Close cancels the call; ctx is still the caller's.
	stop := context.AfterFunc(c.closed, cancel) //nolint:contextcheck // only cancels ctx
	defer stop()

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
		return err
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("proxmox: read the answer to %s %s: %w", method, path, err)
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
		return fmt.Errorf("proxmox: decode the answer to %s %s: %w", method, path, err)
	}
	if len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("proxmox: decode the answer to %s %s: %w", method, path, err)
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
	if json.Unmarshal(raw, &body) != nil {
		return &apiError{Status: resp.StatusCode, Message: message}
	}

	var b strings.Builder
	b.WriteString(message)
	if m := strings.TrimSpace(body.Message); m != "" && !strings.Contains(message, m) {
		b.WriteString(": " + m)
	}
	for _, param := range slices.Sorted(maps.Keys(body.Errors)) {
		b.WriteString("; " + param + ": " + strings.TrimSpace(body.Errors[param]))
	}

	return &apiError{Status: resp.StatusCode, Message: b.String()}
}

// task is a task's status.
type task struct {
	Status     string `json:"status"`
	ExitStatus string `json:"exitstatus"`
}

// wait waits for the task upid to end, asking every poll, and returns an error
// carrying its exit status if it failed.
func (c *client) wait(ctx context.Context, upid string, poll time.Duration) error {
	node, err := taskNode(upid)
	if err != nil {
		return err
	}

	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/status"

	for {
		var t task
		if err := c.get(ctx, path, nil, &t); err != nil {
			return fmt.Errorf("proxmox: task %s: %w", upid, err)
		}

		if t.Status == "stopped" {
			if t.ExitStatus == "OK" || strings.HasPrefix(t.ExitStatus, "WARNINGS") {
				return nil
			}

			return &taskError{UPID: upid, ExitStatus: t.ExitStatus}
		}

		select {
		case <-time.After(poll):
		case <-ctx.Done():
			return fmt.Errorf("proxmox: task %s: %w", upid, ctx.Err())
		}
	}
}

// taskError is a task that ended in failure.
type taskError struct {
	UPID       string
	ExitStatus string
}

func (e *taskError) Error() string {
	return "proxmox: " + e.ExitStatus
}

// taskNode returns the node a task runs on, from its UPID:
// UPID:NODE:PID:PSTART:STARTTIME:TYPE:ID:USER:.
func taskNode(upid string) (string, error) {
	parts := strings.Split(upid, ":")
	if len(parts) < 3 || parts[0] != "UPID" || parts[1] == "" {
		return "", fmt.Errorf("proxmox: %q is not a task ID", upid)
	}

	return parts[1], nil
}

// errorMessage returns what err says, in lower case, for matching Proxmox
// VE's messages.
func errorMessage(err error) string {
	var api *apiError
	if errors.As(err, &api) {
		return strings.ToLower(api.Message)
	}

	var t *taskError
	if errors.As(err, &t) {
		return strings.ToLower(t.ExitStatus)
	}

	return strings.ToLower(err.Error())
}
