// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package scaleset

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	ghscaleset "github.com/actions/scaleset"
	"github.com/actions/scaleset/listener"

	"github.com/konradasb/rungar/internal/types"
	"github.com/konradasb/rungar/internal/version"
)

const (
	// sessionRetryFirst and sessionRetryMax bound the wait between attempts
	// to open a session while another holds the scale set, and
	// sessionRetryFirst is also the wait after losing one.
	sessionRetryFirst = 10 * time.Second
	sessionRetryMax   = time.Minute

	// sessionOpenTimeout bounds one attempt to open a session, and
	// sessionCloseTimeout closing one.
	sessionOpenTimeout  = time.Minute
	sessionCloseTimeout = 30 * time.Second
)

// errSessionLost is the scale set's message session failing while held: GitHub
// ended it, or stopped answering over it.
var errSessionLost = errors.New("message session lost")

// messageSession is a scale set's message session: what the listener polls
// over, and Close to end it.
type messageSession interface {
	listener.Client
	Close(ctx context.Context) error
}

// sessionOpener opens a scale set's message session, in owner's name.
type sessionOpener func(ctx context.Context, scaleSetID int, owner string) (messageSession, error)

// sessionOpenerOf returns the sessionOpener of a scale set client.
func sessionOpenerOf(client *ghscaleset.Client) sessionOpener {
	return func(ctx context.Context, scaleSetID int, owner string) (messageSession, error) {
		session, err := client.MessageSessionClient(ctx, scaleSetID, owner)
		if err != nil {
			return nil, err
		}

		return session, nil
	}
}

// openSession opens the scale set's message session. GitHub allows one per
// scale set, so while another is held, by another daemon or by one that ended
// without closing it, it waits and retries: the scale set stands by. It
// retries too while GitHub does not answer, and fails on any other refusal.
//
// The request itself is not cancelled by ctx: GitHub may create the session
// even if the client stops waiting, and a session the daemon does not know it
// holds is never closed. One opened after ctx is done is closed at once.
func (s *scaleSet) openSession(ctx context.Context, scaleSetID int) (messageSession, error) {
	scaleSet := s.spec.Name
	wait := s.sessionRetry

	for {
		openCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionOpenTimeout)
		session, err := s.openMessageSession(openCtx, scaleSetID, sessionOwner(scaleSet))
		cancel()

		if err == nil {
			if ctx.Err() != nil {
				s.closeSession(ctx, session)
				return nil, ctx.Err()
			}

			return session, nil
		}

		switch {
		case isSessionConflict(err):
			// A standby is told once; the retries say nothing new.
			level := slog.LevelDebug
			if s.currentPhase() != types.ScaleSetWaitingForSession {
				level = slog.LevelInfo
			}
			s.setPhase(types.ScaleSetWaitingForSession)
			s.logger.Log(ctx, level, "another message session holds the scale set; standing by until it ends",
				slog.Duration("retry_in", wait), slog.Any("error", err))
		case isUnanswered(err):
			s.setPhase(types.ScaleSetGitHubUnreachable)
			s.logger.Warn("GitHub did not answer a request for the message session; trying again",
				slog.Duration("retry_in", wait), slog.Any("error", err))
		default:
			return nil, fmt.Errorf("scale set %q: open message session: %w", scaleSet, err)
		}

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

// isUnanswered reports whether a request to GitHub failed for want of an
// answer: it could not be sent, or timed out, or had only answers worth
// retrying until its retries ran out. The scale set client returns an answer
// it does not want without a *url.Error.
func isUnanswered(err error) bool {
	var urlErr *url.Error
	return errors.As(err, &urlErr)
}

// closeSession ends a message session, so that GitHub stops assigning it jobs
// at once. The scale set stays on GitHub.
func (s *scaleSet) closeSession(ctx context.Context, session messageSession) {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionCloseTimeout)
	defer cancel()

	if err := session.Close(closeCtx); err != nil {
		s.logger.Warn("cannot close the message session", slog.Any("error", err))
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
