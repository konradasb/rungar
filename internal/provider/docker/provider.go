// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package docker implements the docker provider type: runners as containers
// on a Docker host, talking to its daemon's Engine API. A daemon runs on one
// machine, so each Docker host is a provider of its own.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/provider"
	"github.com/konradasb/rungar/internal/types"
)

// Provider is a connected Docker daemon.
type Provider struct {
	client  *client
	timeout time.Duration
	logger  *slog.Logger
}

var _ provider.Provider = (*Provider)(nil)

// List returns the containers carrying the selector's labels, running or not.
func (p *Provider) List(ctx context.Context, selector map[string]string) ([]types.Machine, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	query := url.Values{"all": {"1"}}
	if len(selector) > 0 {
		filters, err := labelFilters(selector)
		if err != nil {
			return nil, err
		}
		query.Set("filters", filters)
	}

	var containers []containerSummary
	if err := p.client.do(ctx, http.MethodGet, "/containers/json", query, nil, &containers); err != nil {
		return nil, err
	}

	machines := make([]types.Machine, 0, len(containers))
	for _, c := range containers {
		machines = append(machines, machineOf(c))
	}

	return machines, nil
}

// labelFilters returns the Engine API's filter for containers carrying every
// label in selector.
func labelFilters(selector map[string]string) (string, error) {
	labels := make([]string, 0, len(selector))
	for _, key := range slices.Sorted(maps.Keys(selector)) {
		labels = append(labels, key+"="+selector[key])
	}

	b, err := json.Marshal(map[string][]string{"label": labels})
	if err != nil {
		return "", err
	}

	return string(b), nil
}

// Create pulls the runner's image if it is to be, and creates and starts its
// container. It is bounded by its context alone, since a pull takes as long as
// it takes.
func (p *Provider) Create(ctx context.Context, spec types.MachineSpec) error {
	runner, ok := spec.Runner.(RunnerSpec)
	if !ok {
		return errdefs.InvalidArgument("runner %q: not a docker runner (%T)", spec.Name, spec.Runner)
	}

	if err := p.pull(ctx, runner); err != nil {
		return fmt.Errorf("pull %s: %w", runner.ImageRef, pullError(err))
	}

	query := url.Values{"name": {spec.Name}}
	if err := p.client.do(ctx, http.MethodPost, "/containers/create", query, containerFor(spec, runner), nil); err != nil {
		return fmt.Errorf("create the container: %w", createError(err))
	}

	if err := p.client.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(spec.Name)+"/start", nil, nil, nil); err != nil {
		return fmt.Errorf("start the container: %w", startError(err))
	}

	return nil
}

// pull pulls the runner's image, unless the host has it and the runner does
// not ask for it every time.
func (p *Provider) pull(ctx context.Context, runner RunnerSpec) error {
	if runner.Pull == PullMissing {
		err := p.client.do(ctx, http.MethodGet, "/images/"+runner.ImageRef+"/json", nil, nil, nil)
		if err == nil {
			return nil
		}
		if statusOf(err) != http.StatusNotFound {
			return err
		}
	}

	repo, tag := splitImageRef(runner.ImageRef)

	return p.client.stream(ctx, http.MethodPost, "/images/create", url.Values{"fromImage": {repo}, "tag": {tag}})
}

// Delete removes a container, stopping it first. One not there, or already
// being removed, is not an error.
func (p *Provider) Delete(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	query := url.Values{"force": {"1"}, "v": {"1"}}

	err := p.client.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(name), query, nil, nil)
	switch {
	case err == nil, statusOf(err) == http.StatusNotFound:
		return nil
	case statusOf(err) == http.StatusConflict && strings.Contains(err.Error(), "already in progress"):
		// A container removing itself as it stops.
		return nil
	default:
		return err
	}
}

// Close releases the client's connections.
func (p *Provider) Close() error {
	p.client.close()
	return nil
}

// pullError puts an error pulling an image in the class that says what Rungar
// should do next (see provider.Provider.Create), as createError and
// startError do for the steps after it: a host out of disk is full, and an
// image, network, volume or path the runner names that the host lacks is the
// runner's fault, which no retry mends.
func pullError(err error) error {
	switch msg := strings.ToLower(err.Error()); {
	case isFull(msg):
		return errdefs.NoCapacity("%s", err)
	case statusOf(err) == http.StatusNotFound,
		strings.Contains(msg, "manifest unknown"),
		strings.Contains(msg, "not found"),
		strings.Contains(msg, "pull access denied"),
		strings.Contains(msg, "invalid reference format"):
		return errdefs.InvalidArgument("%s", err)
	default:
		return err
	}
}

// createError classifies an error creating a container. The daemon answers a
// missing image or network with 404, and a configuration it cannot run with
// 400.
func createError(err error) error {
	switch status := statusOf(err); {
	case isFull(strings.ToLower(err.Error())):
		return errdefs.NoCapacity("%s", err)
	case status == http.StatusNotFound, status == http.StatusBadRequest:
		return errdefs.InvalidArgument("%s", err)
	default:
		return err
	}
}

// startError classifies an error starting a container.
func startError(err error) error {
	switch msg := strings.ToLower(err.Error()); {
	case isFull(msg):
		return errdefs.NoCapacity("%s", err)
	case strings.Contains(msg, "error while mounting volume"),
		strings.Contains(msg, "invalid mount config"),
		strings.Contains(msg, "bind source path does not exist"),
		strings.Contains(msg, "not a directory"),
		strings.Contains(msg, "network") && strings.Contains(msg, "not found"):
		return errdefs.InvalidArgument("%s", err)
	default:
		return err
	}
}

// isFull reports whether an error message says the host is out of disk.
func isFull(msg string) bool {
	return strings.Contains(msg, "no space left on device")
}
