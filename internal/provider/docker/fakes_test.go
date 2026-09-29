// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package docker

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/types"
)

// request is a request the fake engine was sent.
type request struct {
	method, path string
	query        url.Values
	body         string
}

// engine is a fake Docker Engine API. Each handler, keyed by "METHOD /path"
// without the version, answers with a status and a body; unset ones answer
// 204 with nothing.
type engine struct {
	t      *testing.T
	server *httptest.Server

	mu       sync.Mutex
	answers  map[string]answer
	requests []request
}

// answer is what the fake engine answers a request with.
type answer struct {
	status int
	body   string
	wait   time.Duration
}

func newEngine(t *testing.T) *engine {
	t.Helper()

	e := &engine{t: t, answers: map[string]answer{}}
	e.server = httptest.NewServer(http.HandlerFunc(e.serve))
	t.Cleanup(e.server.Close)

	return e
}

// on sets the answer to method and path.
func (e *engine) on(method, path string, status int, body string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.answers[method+" "+path] = answer{status: status, body: body}
}

// hang has the engine take d to answer method and path.
func (e *engine) hang(method, path string, d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.answers[method+" "+path] = answer{status: http.StatusOK, body: "[]", wait: d}
}

func (e *engine) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	path := strings.TrimPrefix(r.URL.Path, "/"+apiVersion)

	e.mu.Lock()
	e.requests = append(e.requests, request{method: r.Method, path: path, query: r.URL.Query(), body: string(body)})
	a, ok := e.answers[r.Method+" "+path]
	e.mu.Unlock()

	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if a.wait > 0 {
		select {
		case <-time.After(a.wait):
		case <-r.Context().Done():
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(a.status)
	_, _ = io.WriteString(w, a.body)
}

// sent returns the requests made to method and path.
func (e *engine) sent(method, path string) []request {
	e.mu.Lock()
	defer e.mu.Unlock()

	var out []request
	for _, r := range e.requests {
		if r.method == method && r.path == path {
			out = append(out, r)
		}
	}

	return out
}

// provider returns a provider of the fake engine, configured from yaml.
func (e *engine) provider(extra string) *Provider {
	e.t.Helper()

	c := configure(e.t, "address: tcp://"+strings.TrimPrefix(e.server.URL, "http://")+"\n"+extra)

	return open(e.t, c)
}

// open opens the provider of c.
func open(t *testing.T, c *Config) *Provider {
	t.Helper()

	p, err := c.Open(nil)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	docker, ok := p.(*Provider)
	if !ok {
		t.Fatalf("Open() = %T, want *Provider", p)
	}

	return docker
}

// configure returns the provider configured from yaml.
func configure(t *testing.T, doc string) *Config {
	t.Helper()

	c, err := Type{}.Configure("build1", node(t, doc))
	if err != nil {
		t.Fatalf("Configure() = %v", err)
	}

	cfg, ok := c.(*Config)
	if !ok {
		t.Fatalf("Configure() = %T, want *Config", c)
	}

	return cfg
}

// node parses doc into a YAML node, as a provider's entry or runner block is
// handed to its type.
func node(t *testing.T, doc string) *yaml.Node {
	t.Helper()

	var n yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &n); err != nil {
		t.Fatalf("parse %q: %v", doc, err)
	}
	if len(n.Content) == 0 {
		return nil
	}

	return n.Content[0]
}

// runner parses a runner block with the provider's defaults.
func runner(t *testing.T, doc string) RunnerSpec {
	t.Helper()

	spec, err := (&Config{}).ParseRunner(node(t, doc))
	if err != nil {
		t.Fatalf("ParseRunner(%q) = %v", doc, err)
	}

	r, ok := spec.(RunnerSpec)
	if !ok {
		t.Fatalf("ParseRunner() = %T, want RunnerSpec", spec)
	}

	return r
}

// machine returns the machine spec of a runner named name.
func machine(name string, spec types.RunnerSpec) types.MachineSpec {
	return types.MachineSpec{
		Name:      name,
		Labels:    map[string]string{"rungar.sh/managed": "true", "rungar.sh/runner": name},
		JITConfig: "jit-" + name,
		Runner:    spec,
	}
}

// decode unmarshals a request body.
func decode(t *testing.T, body string, v any) {
	t.Helper()

	if err := json.Unmarshal([]byte(body), v); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
}
