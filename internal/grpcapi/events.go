// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package grpcapi

import (
	"errors"

	"google.golang.org/grpc"

	"github.com/konradasb/rungar/internal/errdefs"
	"github.com/konradasb/rungar/internal/events"
	rungarv1 "github.com/konradasb/rungar/proto/rungar/v1"
)

// maxEventBatch is the most history events sent in one response.
const maxEventBatch = 500

// ListEvents streams the matching history and, if the request follows, new
// events as they are recorded.
func (s *Server) ListEvents(
	req *rungarv1.ListEventsRequest, stream grpc.ServerStreamingServer[rungarv1.ListEventsResponse],
) error {
	if req.GetLimit() < 0 {
		return errdefs.InvalidArgument("limit cannot be negative")
	}

	filter := events.Filter{
		ScaleSet: req.GetScaleSet(),
		Provider: req.GetProvider(),
		Runner:   req.GetRunner(),
	}
	if since := req.GetSince(); since != nil {
		filter.Since = since.AsTime()
	}
	limit := int(req.GetLimit())

	if !req.GetFollow() {
		return sendHistory(stream, s.events.List(filter, limit))
	}

	history, sub := s.events.Subscribe(filter, limit)
	defer sub.Close()

	if err := sendHistory(stream, history); err != nil {
		return err
	}

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case e, ok := <-sub.Events():
			if !ok {
				if err := sub.Err(); errors.Is(err, events.ErrFellBehind) {
					return errdefs.Unavailable("%w", err)
				}

				return nil
			}

			resp := &rungarv1.ListEventsResponse{Events: []*rungarv1.Event{eventToProto(e)}}
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
	}
}

// sendHistory sends list in batches, the last marked caught up. An empty list
// is sent as one empty batch.
func sendHistory(stream grpc.ServerStreamingServer[rungarv1.ListEventsResponse], list []events.Event) error {
	for {
		n := min(len(list), maxEventBatch)
		resp := &rungarv1.ListEventsResponse{
			Events:   make([]*rungarv1.Event, 0, n),
			CaughtUp: n == len(list),
		}
		for _, e := range list[:n] {
			resp.Events = append(resp.Events, eventToProto(e))
		}

		if err := stream.Send(resp); err != nil {
			return err
		}
		if resp.GetCaughtUp() {
			return nil
		}

		list = list[n:]
	}
}

// eventToProto converts an event.
func eventToProto(e events.Event) *rungarv1.Event {
	return &rungarv1.Event{
		Time:       timestamp(e.Time),
		Kind:       eventKindToProto(e.Kind),
		Name:       e.Name,
		Action:     eventActionToProto(e.Action),
		ScaleSet:   e.ScaleSet,
		Provider:   e.Provider,
		Message:    e.Message,
		Attributes: e.Attributes,
	}
}

// eventKindToProto converts what an event is about.
func eventKindToProto(k events.Kind) rungarv1.EventKind {
	switch k {
	case events.KindRunner:
		return rungarv1.EventKind_EVENT_KIND_RUNNER
	case events.KindProvider:
		return rungarv1.EventKind_EVENT_KIND_PROVIDER
	case events.KindScaleSet:
		return rungarv1.EventKind_EVENT_KIND_SCALE_SET
	default:
		return rungarv1.EventKind_EVENT_KIND_UNSPECIFIED
	}
}

// eventActionToProto converts what happened in an event.
func eventActionToProto(a events.Action) rungarv1.EventAction {
	switch a {
	case events.ActionCreated:
		return rungarv1.EventAction_EVENT_ACTION_CREATED
	case events.ActionAdopted:
		return rungarv1.EventAction_EVENT_ACTION_ADOPTED
	case events.ActionRemoved:
		return rungarv1.EventAction_EVENT_ACTION_REMOVED
	case events.ActionLost:
		return rungarv1.EventAction_EVENT_ACTION_LOST
	case events.ActionConnected:
		return rungarv1.EventAction_EVENT_ACTION_CONNECTED
	case events.ActionJobStarted:
		return rungarv1.EventAction_EVENT_ACTION_JOB_STARTED
	case events.ActionFull:
		return rungarv1.EventAction_EVENT_ACTION_FULL
	case events.ActionFailing:
		return rungarv1.EventAction_EVENT_ACTION_FAILING
	case events.ActionPaused:
		return rungarv1.EventAction_EVENT_ACTION_PAUSED
	case events.ActionResumed:
		return rungarv1.EventAction_EVENT_ACTION_RESUMED
	case events.ActionMinRunnersChanged:
		return rungarv1.EventAction_EVENT_ACTION_MIN_RUNNERS_CHANGED
	default:
		return rungarv1.EventAction_EVENT_ACTION_UNSPECIFIED
	}
}
