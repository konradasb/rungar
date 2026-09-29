// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	ghscaleset "github.com/actions/scaleset"

	"github.com/konradasb/rungar/internal/types"
	"github.com/konradasb/rungar/internal/version"
)

const (
	// sessionRetryFirst and sessionRetryMax bound the wait between attempts
	// to open a session while another holds the scale set.
	sessionRetryFirst = 10 * time.Second
	sessionRetryMax   = time.Minute

	// sessionOpenTimeout bounds one attempt to open a session, and
	// sessionCloseTimeout closing one.
	sessionOpenTimeout  = time.Minute
	sessionCloseTimeout = 30 * time.Second
)

// openSession opens the scale set's message session. GitHub allows one per
// scale set, so while another is held, by another daemon or by one that ended
// without closing it, it waits and retries.
//
// The request itself is not cancelled by ctx: GitHub may create the session
// even if the client stops waiting, and a session the daemon does not know it
// holds is never closed. One opened after ctx is done is closed at once.
func (s *scaleSet) openSession(ctx context.Context, scaleSetID int) (*ghscaleset.MessageSessionClient, error) {
	scaleSet := s.spec.Name
	wait := sessionRetryFirst

	for {
		openCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionOpenTimeout)
		session, err := s.client.MessageSessionClient(openCtx, scaleSetID, sessionOwner(scaleSet))
		cancel()

		if err == nil {
			if ctx.Err() != nil {
				s.closeSession(ctx, session)
				return nil, ctx.Err()
			}

			return session, nil
		}

		if !isSessionConflict(err) {
			return nil, fmt.Errorf("scale set %q: open message session: %w", scaleSet, err)
		}

		s.setPhase(types.ScaleSetWaitingForSession)
		s.logger.Warn("another message session holds the scale set; waiting for it to end",
			slog.Duration("retry_in", wait), slog.Any("error", err))

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}

		wait = min(wait*2, sessionRetryMax)
	}
}

// isSessionConflict reports whether GitHub refused a message session because
// another is held. The scale set client has no error value for it, so GitHub's
// exception name is matched.
func isSessionConflict(err error) bool {
	return strings.Contains(err.Error(), "SessionConflictException")
}

// closeSession ends a message session, so that GitHub stops assigning it jobs
// at once. The scale set stays on GitHub.
func (s *scaleSet) closeSession(ctx context.Context, session *ghscaleset.MessageSessionClient) {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionCloseTimeout)
	defer cancel()

	if err := session.Close(closeCtx); err != nil {
		s.logger.Warn("closing message session", slog.Any("error", err))
	}
}

// sessionOwner returns the owner GitHub shows for a session: the host and the
// scale set.
func sessionOwner(scaleSet string) string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}

	return fmt.Sprintf("rungar-%s-%s", host, scaleSet)
}

// systemInfo returns what the scale set client puts in its user agent,
// including the scale set's ID, which GitHub support asks for.
func systemInfo(scaleSetID int) ghscaleset.SystemInfo {
	return ghscaleset.SystemInfo{
		System:     "rungar",
		Subsystem:  "daemon",
		Version:    version.Version,
		CommitSHA:  version.Commit,
		ScaleSetID: scaleSetID,
	}
}
