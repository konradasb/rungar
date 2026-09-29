// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package events defines the events the daemon records, keeps their log, and
// delivers new ones to subscribers. Recording never waits on the disk: the
// events file is written behind, in the order the events were recorded.
package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// DefaultMaxCount is how many events a Log keeps unless told otherwise.
const DefaultMaxCount = 10000

// writeBuffer is how many events may wait to be written to the file. Past
// it, an event is kept in memory and given to subscribers, but not written
// until the file is next compacted: once the writer has caught up, or on
// Close.
const writeBuffer = 1024

// fileMode lets the daemon's user read and write the events file, and nobody
// else.
const fileMode = 0o600

// compactionRetryDelay is how long the writer waits after a failed
// compaction before trying another. No timer runs: the next try comes with
// the first event the writer takes after the delay, or on Close, which
// always tries.
const compactionRetryDelay = 30 * time.Second

// Config configures a Log.
type Config struct {
	// File is where the events are kept, one JSON document a line.
	// Its directory is made if it is not there.
	File string

	// MaxCount is how many events are kept: the most recent. Defaults to
	// DefaultMaxCount.
	MaxCount int

	// MaxAge is how long an event is kept. Zero means no limit.
	MaxAge time.Duration

	// Logger is where the Log logs. Optional: defaults to discarding.
	Logger *slog.Logger
}

// Log keeps events in memory and in an append-only file, and passes each new
// one to its subscribers. One goroutine writes the file, so that Record never
// waits on it; the file is compacted when opened, when it grows a quarter past
// its limit, after a write to it fails, once the writer has caught up after
// dropping events, and on Close if events were left unwritten. It is safe for
// concurrent use.
type Log struct {
	path     string
	maxCount int
	maxAge   time.Duration
	logger   *slog.Logger

	// now stamps and ages events; tests replace it.
	now func() time.Time

	// writes carries recorded events to the writer goroutine, in the order
	// they were recorded. Closed by Close.
	writes chan pendingEvent
	// stopped is closed when the writer goroutine has flushed and closed
	// the file; closeErr is then why compacting or closing it at the end
	// failed, or nil.
	stopped  chan struct{}
	closeErr error

	mu       sync.Mutex
	events   []Event // oldest first
	recorded uint64  // events recorded so far, numbering each
	dropped  uint64  // events not queued for writing: writes was full
	// droppedThrough is the number of the last event dropped. The file
	// lacks it until compacted with it.
	droppedThrough uint64
	// dropping is whether the last event recorded was dropped, so that a
	// run of them is warned of once.
	dropping bool
	closed   bool
	// recordedAfterClose is whether an event was recorded after Close,
	// which is warned of once.
	recordedAfterClose bool
	subscriptions      map[*Subscription]struct{}
}

// pendingEvent is an event waiting to be written, numbered in the order it
// was recorded.
type pendingEvent struct {
	seq   uint64
	event Event
}

// eventsFile is the events file as the writer goroutine sees it: something
// to append to. Only the writer goroutine uses it, once open has made it.
type eventsFile struct {
	path string

	// open opens the file for appending, replace replaces it whole, and now
	// times retries; tests replace them.
	open    func(path string) (io.WriteCloser, error)
	replace func(path string, data []byte) error
	now     func() time.Time

	// file is nil after a write to it failed, since that may have left part
	// of a line, or when it could not be opened: nothing is appended until
	// it is compacted, which writes it whole.
	file    io.WriteCloser
	written int // events in the file, kept or not
	// compactedThrough is the last event the file was compacted with:
	// the writer skips pending events up to it, which the file already has.
	compactedThrough uint64
	// retryAt is when the writer may try again to compact the file, after a
	// compaction failed.
	retryAt time.Time
}

// Open opens the log at cfg.File, creating it if it does not exist.
func Open(cfg Config) (*Log, error) {
	f := &eventsFile{open: openAppend, replace: replaceFile, now: time.Now}

	return open(cfg, f, writeBuffer)
}

// open is Open, with the file written through f and room for buffer events
// waiting to be written.
func open(cfg Config, f *eventsFile, buffer int) (*Log, error) {
	if cfg.MaxCount <= 0 {
		cfg.MaxCount = DefaultMaxCount
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	l := &Log{
		path:          cfg.File,
		maxCount:      cfg.MaxCount,
		maxAge:        cfg.MaxAge,
		logger:        cfg.Logger,
		now:           time.Now,
		writes:        make(chan pendingEvent, buffer),
		stopped:       make(chan struct{}),
		subscriptions: make(map[*Subscription]struct{}),
	}

	if err := os.MkdirAll(filepath.Dir(l.path), 0o750); err != nil {
		return nil, fmt.Errorf("create the events file's directory: %w", err)
	}
	if err := l.load(); err != nil {
		return nil, err
	}
	l.trim()

	f.path = l.path
	if err := l.compact(f); err != nil {
		return nil, err
	}
	go l.write(f)

	return l, nil
}

// openAppend opens the file at path for appending.
func openAppend(path string) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND, fileMode)
}

// load reads the events kept in the file, skipping a line that cannot be
// read, such as the tail of a write the daemon died during.
func (l *Log) load() error {
	data, err := os.ReadFile(l.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read events: %w", err)
	}

	for line := range bytes.Lines(data) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}

		var e Event
		if err := json.Unmarshal(line, &e); err != nil {
			l.logger.Warn("skipping an unreadable event", slog.Any("error", err))
			continue
		}
		l.events = append(l.events, e)
	}

	return nil
}

// Record timestamps e if needed, keeps it, passes it to every matching
// subscriber, and queues it to be written. It does not wait for the write:
// if too many events are waiting already, e is left out of the file until it
// is next compacted, and a run of such events is warned of once. An event
// recorded after Close is kept in memory but may not be written, which is
// warned of once.
func (l *Log) Record(e Event) {
	if e.Time.IsZero() {
		e.Time = l.now()
	}
	e = clone(e)

	l.mu.Lock()
	l.events = append(l.events, e)
	l.trim()
	l.recorded++
	warning := l.queue(pendingEvent{seq: l.recorded, event: e})
	dropped := l.dropped
	for sub := range l.subscriptions {
		if sub.filter.Matches(e) {
			sub.send(clone(e))
		}
	}
	l.mu.Unlock()

	switch warning {
	case warnClosed:
		l.logger.Warn("events recorded after the event log closed may not be written",
			slog.String("kind", string(e.Kind)), slog.String("name", e.Name),
			slog.String("action", string(e.Action)))
	case warnDropping:
		l.logger.Warn("events are recorded faster than they are written; "+
			"the file will have them when next compacted",
			slog.Uint64("dropped", dropped))
	}
}

// queueWarning is what queue found worth warning of.
type queueWarning int

const (
	noWarning queueWarning = iota
	// warnDropping is the first event dropped since the queue last had
	// room.
	warnDropping
	// warnClosed is the first event recorded after Close.
	warnClosed
)

// queue passes p to the writer goroutine without waiting, dropping it if the
// queue is full, and returns what is worth warning of. The caller must hold
// l.mu.
func (l *Log) queue(p pendingEvent) queueWarning {
	if l.closed {
		if l.recordedAfterClose {
			return noWarning
		}
		l.recordedAfterClose = true

		return warnClosed
	}

	select {
	case l.writes <- p:
		l.dropping = false

		return noWarning
	default:
		l.dropped++
		l.droppedThrough = p.seq
		if l.dropping {
			return noWarning
		}
		l.dropping = true

		return warnDropping
	}
}

// droppedSince reports whether an event after seq was dropped.
func (l *Log) droppedSince(seq uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.droppedThrough > seq
}

// write is the writer goroutine: it appends each queued event to f, and
// compacts f as it grows, once a write to it fails, and once it has caught up
// after events were dropped, so that a crash loses as few as it can. Once
// l.writes is closed and drained, it compacts f if f lacks any event, closes
// it, and keeps why either failed for Close to return.
func (l *Log) write(f *eventsFile) {
	defer close(l.stopped)

	for p := range l.writes {
		// An event a compaction already wrote is skipped, but the checks
		// below still run: it may be the last one queued, with an event
		// dropped during that compaction still to be written.
		if p.seq > f.compactedThrough && f.file != nil {
			if err := f.writeEvent(p.event); err != nil {
				l.logger.Warn("cannot write an event; the events file will be rewritten",
					slog.String("kind", string(p.event.Kind)), slog.String("name", p.event.Name),
					slog.String("action", string(p.event.Action)), slog.Any("error", err))
				f.discard()
			}
		}
		// An event is dropped only when the queue is full, so the writer
		// always takes another after a drop, written or skipped, and finds
		// it here once the queue is empty.
		caughtUp := len(l.writes) == 0 && l.droppedSince(f.compactedThrough)
		if f.file == nil || f.written > l.maxCount+l.maxCount/4 || caughtUp {
			l.tryCompact(f)
		}
	}

	var errs []error
	if f.file == nil || l.droppedSince(f.compactedThrough) {
		if err := l.compact(f); err != nil {
			errs = append(errs, err)
		}
	}
	if f.file != nil {
		if err := f.file.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close events: %w", err))
		}
		f.file = nil
	}
	l.closeErr = errors.Join(errs...)
}

// tryCompact compacts f unless a compaction failed too recently, and puts
// the next attempt off if this one fails.
func (l *Log) tryCompact(f *eventsFile) {
	now := f.now()
	if now.Before(f.retryAt) {
		return
	}

	if err := l.compact(f); err != nil {
		f.retryAt = now.Add(compactionRetryDelay)
		l.logger.Warn("cannot compact the events; trying again later",
			slog.Any("error", err), slog.Duration("retry_after", compactionRetryDelay))
	}
}

// writeEvent appends e to the file, which must be open.
func (f *eventsFile) writeEvent(e Event) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := f.file.Write(append(line, '\n')); err != nil {
		return err
	}
	f.written++

	return nil
}

// discard closes the file after a write to it failed, which may have left
// part of a line: nothing more is appended until it is compacted.
func (f *eventsFile) discard() {
	_ = f.file.Close()
	f.file = nil
}

// trim drops the events past the log's limits, oldest first. The caller
// must hold l.mu, unless no other goroutine has the log yet, as in open.
func (l *Log) trim() {
	drop := max(len(l.events)-l.maxCount, 0)
	if l.maxAge > 0 {
		oldest := l.now().Add(-l.maxAge)
		for drop < len(l.events) && l.events[drop].Time.Before(oldest) {
			drop++
		}
	}
	if drop > 0 {
		l.events = append(l.events[:0:0], l.events[drop:]...)
	}
}

// snapshot returns the events kept, and the number of the last one recorded:
// every event up to it is either among them or no longer kept.
func (l *Log) snapshot() ([]Event, uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.events), l.recorded
}

// compact rewrites the file with only the events kept, and reopens it for
// appending. Only once the file is rewritten does it count the events up to
// the snapshot as written. It is called by the writer goroutine, or by open
// before that starts, and never with l.mu held: it takes it only to snapshot
// the events.
func (l *Log) compact(f *eventsFile) error {
	events, seq := l.snapshot()

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			return fmt.Errorf("encode event: %w", err)
		}
	}
	if err := f.replace(f.path, buf.Bytes()); err != nil {
		return fmt.Errorf("write events: %w", err)
	}
	f.compactedThrough = seq

	if f.file != nil {
		_ = f.file.Close()
	}
	file, err := f.open(f.path)
	if err != nil {
		f.file = nil
		return fmt.Errorf("open events: %w", err)
	}
	f.file = file
	f.written = len(events)

	return nil
}

// replaceFile replaces the file at path with data atomically, by writing
// beside it and renaming over it.
func replaceFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(fileMode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), path)
}

// List returns the events f picks, oldest first: the last limit of them, or
// all if limit is zero.
func (l *Log) List(f Filter, limit int) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.list(f, limit)
}

// list is List. The caller must hold l.mu.
func (l *Log) list(f Filter, limit int) []Event {
	var out []Event
	for _, e := range l.events {
		if f.Matches(e) {
			out = append(out, clone(e))
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}

	return out
}

// Close stops the log: its subscriptions end, the events waiting to be
// written are written, and the file is compacted if it lacks any. It returns
// why that last compaction or closing the file failed. Events recorded after
// Close are kept in memory but may not be written, and subscriptions made
// after it end at once. Calling Close again waits for the first to finish and
// returns its error.
func (l *Log) Close() error {
	l.mu.Lock()
	if !l.closed {
		l.closed = true
		for sub := range l.subscriptions {
			sub.end(nil)
		}
		close(l.writes)
	}
	l.mu.Unlock()

	<-l.stopped

	return l.closeErr
}

// clone returns e with its own copy of the attributes.
func clone(e Event) Event {
	e.Attributes = maps.Clone(e.Attributes)

	return e
}
