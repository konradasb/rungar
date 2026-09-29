// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package docker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// apiVersion is the Engine API version the client speaks: Docker 20.10 and
// later.
const apiVersion = "v1.41"

// client is a small Docker Engine API client: the calls the provider makes,
// and nothing else.
type client struct {
	http *http.Client
	base string
}

// newSocketClient returns a client of the daemon listening on the socket at
// path.
func newSocketClient(path string) *client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}

	// The host is ignored: every request goes to the socket.
	return &client{http: &http.Client{Transport: transport}, base: "http://docker/" + apiVersion}
}

// newTCPClient returns a client of the daemon at hostPort, over TLS if cfg is
// not nil. It goes through the proxy the environment names, as every
// provider's network connections do.
func newTCPClient(hostPort string, cfg *tls.Config) *client {
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSClientConfig:     cfg,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     90 * time.Second,
	}

	scheme := "http"
	if cfg != nil {
		scheme = "https"
	}

	return &client{http: &http.Client{Transport: transport}, base: scheme + "://" + hostPort + "/" + apiVersion}
}

// apiError is an error the daemon answered with.
type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string {
	return e.message
}

// statusOf returns the HTTP status err was answered with, or 0 if it was not
// an answer.
func statusOf(err error) int {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		return apiErr.status
	}

	return 0
}

// do sends a request with in, if not nil, as its JSON body, and decodes the
// JSON answer into out, if not nil.
func (c *client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	resp, err := c.send(ctx, method, path, query, in)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s %s: decode the answer: %w", method, path, err)
	}

	return nil
}

// stream sends a request whose answer is a stream of JSON messages, as a
// pull's is, and returns the error the stream ends with, if any.
func (c *client) stream(ctx context.Context, method, path string, query url.Values) error {
	resp, err := c.send(ctx, method, path, query, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)

	for scanner.Scan() {
		var msg struct {
			Error       string `json:"error"`
			ErrorDetail struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		switch {
		case msg.ErrorDetail.Message != "":
			return &apiError{status: resp.StatusCode, message: msg.ErrorDetail.Message}
		case msg.Error != "":
			return &apiError{status: resp.StatusCode, message: msg.Error}
		}
	}

	return scanner.Err()
}

// send sends a request, returning an *apiError for an answer that is not a
// success.
func (c *client) send(ctx context.Context, method, path string, query url.Values, in any) (*http.Response, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}

	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= http.StatusBadRequest {
		defer func() { _ = resp.Body.Close() }()
		return nil, &apiError{status: resp.StatusCode, message: errorMessage(resp)}
	}

	return resp, nil
}

// errorMessage returns the message of an error answer: the JSON body's
// message, or the body itself.
func errorMessage(resp *http.Response) string {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	var msg struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &msg) == nil && msg.Message != "" {
		return msg.Message
	}

	if s := strings.TrimSpace(string(b)); s != "" {
		return s
	}

	return resp.Status
}

// close releases the client's idle connections.
func (c *client) close() {
	c.http.CloseIdleConnections()
}
