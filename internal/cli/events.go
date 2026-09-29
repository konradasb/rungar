// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/timestamppb"

	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// Words of an event's kind and action.
const (
	eventRunner   = "Runner"
	eventProvider = "Provider"
	eventScaleSet = "Scale set"

	eventCreated = "Created"
	eventAdopted = "Adopted"
	eventRemoved = "Removed"
	eventLost    = "Lost"
	eventFull    = "Full"
	eventFailing = "Failing"
	eventPaused  = "Paused"
	eventResumed = "Resumed"
)

func newEventsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events",
		Short: "Show what the daemon has done to runners, providers and scale sets, and why",
		Long: "Show what the daemon has done: runners created, adopted, removed and lost, and why, " +
			"providers a scale set found full or failing to make its runner, and scale sets paused and " +
			"resumed. The daemon keeps them in its events file, so they explain what " +
			"happened while nobody was looking, and outlive a restart. Like the journal, it writes what " +
			"is kept and exits; --follow keeps writing new events as they happen. With --json it writes " +
			"one JSON object a line, for scripts.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			req, err := eventsRequest(cmd, time.Now())
			if err != nil {
				return err
			}
			asJSON, _ := cmd.Flags().GetBool("json")

			return withClient(cmd, func(client rungarv1.RungarServiceClient) error {
				return showEvents(cmd, client, req, asJSON)
			})
		},
	}
	cmd.Flags().BoolP("follow", "f", false, "Keep writing new events as they happen")
	cmd.Flags().Int32P("tail", "n", 0, "Only the last this many events (default all kept)")
	cmd.Flags().String("since", "", "Only the events since a time, or for a duration: 2026-09-28, 10:30, 1h")
	cmd.Flags().String("scale-set", "", "Only this scale set's events")
	cmd.Flags().String("provider", "", "Only the events on this provider: of its runners, and of it")
	cmd.Flags().String("runner", "", "Only this runner's events")
	cmd.Flags().Bool("json", false, "Write JSON, one event a line")

	return cmd
}

// eventsRequest returns the request the command's flags ask for, with --since
// relative to now.
func eventsRequest(cmd *cobra.Command, now time.Time) (*rungarv1.ListEventsRequest, error) {
	flags := cmd.Flags()
	follow, _ := flags.GetBool("follow")
	tail, _ := flags.GetInt32("tail")
	since, _ := flags.GetString("since")
	scaleSet, _ := flags.GetString("scale-set")
	provider, _ := flags.GetString("provider")
	runner, _ := flags.GetString("runner")

	if tail < 0 {
		return nil, fmt.Errorf("invalid --tail %d: want 0 or more", tail)
	}

	req := &rungarv1.ListEventsRequest{
		ScaleSet: scaleSet,
		Provider: provider,
		Runner:   runner,
		Limit:    tail,
		Follow:   follow,
	}
	if since != "" {
		t, err := parseSince(since, now)
		if err != nil {
			return nil, err
		}
		req.Since = timestamppb.New(t)
	}

	return req, nil
}

// showEvents writes the events req asks for: the history at once, with columns
// sized to fit it, then each new event as it comes if req follows.
func showEvents(cmd *cobra.Command, client rungarv1.RungarServiceClient, req *rungarv1.ListEventsRequest,
	asJSON bool,
) error {
	ctx := cmd.Context()

	stream, err := client.ListEvents(ctx, req)
	if err != nil {
		return err
	}

	w := &eventWriter{w: cmd.OutOrStdout(), p: paletteFor(cmd.OutOrStdout()), asJSON: asJSON}

	var history []*rungarv1.Event
	caughtUp := false

	for {
		resp, err := stream.Recv()
		switch {
		case errors.Is(err, io.EOF), err != nil && ctx.Err() != nil:
			if caughtUp {
				return nil
			}

			// Show what history arrived before the stream ended.
			return w.write(history)
		case err != nil:
			return err
		}

		if caughtUp {
			if err := w.write(resp.GetEvents()); err != nil {
				return err
			}

			continue
		}

		history = append(history, resp.GetEvents()...)
		if resp.GetCaughtUp() {
			caughtUp = true
			if err := w.write(history); err != nil {
				return err
			}
			history = nil
		}
	}
}

// eventWriter writes events one a line, each column as wide as its widest value
// so far:
//
//	2026-09-28 10:15:02   Runner     rungar-c2-m4-x7k2p   Created   Runner created on compute1: 2 vCPU, 4 GiB
//	2026-09-28 10:16:40   Provider   compute2            Full      Full for runner rungar-c2-m4-q9d1: ...
//
// or as JSON, one object a line.
type eventWriter struct {
	w      io.Writer
	p      palette
	asJSON bool

	kind, name, action int
}

// write writes events, first widening the columns to fit them.
func (ew *eventWriter) write(events []*rungarv1.Event) error {
	if ew.asJSON {
		for _, e := range events {
			b, err := jsonOptions.Marshal(e)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(ew.w, "%s\n", b); err != nil {
				return err
			}
		}

		return nil
	}

	for _, e := range events {
		ew.kind = max(ew.kind, visibleWidth(eventKindWord(e.GetKind())))
		ew.name = max(ew.name, visibleWidth(e.GetName()))
		ew.action = max(ew.action, visibleWidth(eventActionWord(e.GetAction())))
	}

	sep := strings.Repeat(" ", gutter)

	var b strings.Builder
	for _, e := range events {
		b.WriteString(e.GetTime().AsTime().Local().Format(time.DateTime) + sep)
		b.WriteString(pad(eventKindWord(e.GetKind()), ew.kind) + sep)
		b.WriteString(pad(e.GetName(), ew.name) + sep)

		action := ew.p.status(eventActionWord(e.GetAction()))
		if e.GetMessage() == "" {
			b.WriteString(action + "\n")
			continue
		}
		b.WriteString(pad(action, ew.action) + sep + e.GetMessage() + "\n")
	}

	_, err := io.WriteString(ew.w, b.String())

	return err
}

// eventKindWord returns what an event is about: "Runner".
func eventKindWord(k rungarv1.EventKind) string {
	switch k {
	case rungarv1.EventKind_EVENT_KIND_RUNNER:
		return eventRunner
	case rungarv1.EventKind_EVENT_KIND_PROVIDER:
		return eventProvider
	case rungarv1.EventKind_EVENT_KIND_SCALE_SET:
		return eventScaleSet
	default:
		return "-"
	}
}

// eventActionWord returns what happened: "Failing".
func eventActionWord(a rungarv1.EventAction) string {
	switch a {
	case rungarv1.EventAction_EVENT_ACTION_CREATED:
		return eventCreated
	case rungarv1.EventAction_EVENT_ACTION_ADOPTED:
		return eventAdopted
	case rungarv1.EventAction_EVENT_ACTION_REMOVED:
		return eventRemoved
	case rungarv1.EventAction_EVENT_ACTION_LOST:
		return eventLost
	case rungarv1.EventAction_EVENT_ACTION_FULL:
		return eventFull
	case rungarv1.EventAction_EVENT_ACTION_FAILING:
		return eventFailing
	case rungarv1.EventAction_EVENT_ACTION_PAUSED:
		return eventPaused
	case rungarv1.EventAction_EVENT_ACTION_RESUMED:
		return eventResumed
	default:
		return "-"
	}
}

// parseSince parses --since: a duration before now (90m), a date
// (2026-09-28), a time today (10:30, 10:30:15), or both (2026-09-28 10:30).
func parseSince(s string, now time.Time) (time.Time, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d), nil
	}

	for _, layout := range []string{time.DateTime, "2006-01-02 15:04", time.DateOnly, time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			return t, nil
		}
	}

	for _, layout := range []string{time.TimeOnly, "15:04"} {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			y, m, d := now.Date()
			return time.Date(y, m, d, t.Hour(), t.Minute(), t.Second(), 0, now.Location()), nil
		}
	}

	return time.Time{}, fmt.Errorf("invalid --since %q: want a duration (1h), a date (2026-09-28) or a time (10:30)", s)
}
