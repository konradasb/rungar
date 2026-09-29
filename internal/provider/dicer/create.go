// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/types"
)

// Create pulls a runner's image onto the host if it does not hold it, then
// defines the runner's instance and starts it. The pull is bounded by ctx,
// which is the runner's time to start, since a pull onto a cold host can
// take minutes; the connection's keepalive gives up on a daemon that becomes
// unreachable. Creating the instance, with its image held, is bounded by the
// provider's timeout too, so that an unreachable daemon is given up on while
// the runner still has time to be tried elsewhere.
func (p *Provider) Create(ctx context.Context, spec types.MachineSpec) error {
	runner, ok := spec.Runner.(RunnerSpec)
	if !ok {
		return errdefs.InvalidArgument("runner %q: not a dicer runner (%T)", spec.Name, spec.Runner)
	}

	if err := p.ensureImage(ctx, runner.Image); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	if _, err := p.client.CreateInstance(ctx, createInstanceRequest(spec, runner)); err != nil {
		return createError(err)
	}

	return nil
}

// ensureImage pulls an image the host does not hold.
func (p *Provider) ensureImage(ctx context.Context, ref string) error {
	getCtx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	_, err := p.client.GetImage(getCtx, &dicerdv1.GetImageRequest{Ref: ref})
	switch {
	case err == nil:
		return nil
	case status.Code(err) != codes.NotFound:
		return fmt.Errorf("look up image %s: %w", ref, createError(err))
	}

	p.logger.Info("pulling image", slog.String("image", ref))
	started := time.Now()

	if err := p.pull(ctx, ref); err != nil {
		return fmt.Errorf("pull %s: %w", ref, createError(err))
	}

	p.logger.Info("pulled image", slog.String("image", ref),
		slog.Duration("pull_duration", time.Since(started)))

	return nil
}

// pull pulls an image, waiting for the daemon to say it is done.
func (p *Provider) pull(ctx context.Context, ref string) error {
	stream, err := p.client.PullImage(ctx, &dicerdv1.PullImageRequest{Ref: ref})
	if err != nil {
		return err
	}

	for {
		if _, err := stream.Recv(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
