// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package events keeps the log of events the daemon records, and delivers new
// ones to subscribers.
package events

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/konradasb/rungar/internal/types"
)

// DefaultMaxCount is how many events a Log keeps unless told otherwise.
const DefaultMaxCount = 10000

// fileMode lets the daemon's user read and write the events file, and nobody
// else.
const fileMode = 0o600

// Config configures a Log.
type Config struct {
	// Path is the file the events are kept in, one JSON document a line.
	// Its directory is made if it is not there.
	Path string

	// MaxCount is how many events are kept: the most recent. Defaults to
	// DefaultMaxCount.
	MaxCount int

	// MaxAge is how long an event is kept. Zero means no limit.
	MaxAge time.Duration

	// Logger is where the Log logs. Optional: defaults to discarding.
	Logger *slog.Logger
}

// Log keeps events in memory and in an append-only file, and passes each new
// one to its subscribers. The file is compacted when opened and when it grows
// a quarter past its limit.
type Log struct {
	path     string
	maxCount int
	maxAge   time.Duration
	logger   *slog.Logger

	// now stamps and ages events; tests replace it.
	now func() time.Time

	mu      sync.Mutex
	file    *os.File
	events  []types.Event // oldest first
	written int           // events in the file, kept or not
	subs    map[*Subscription]struct{}
}

// Open opens the log at cfg.Path, creating it if it does not exist.
func Open(cfg Config) (*Log, error) {
	if cfg.MaxCount <= 0 {
		cfg.MaxCount = DefaultMaxCount
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}

	l := &Log{
		path:     cfg.Path,
		maxCount: cfg.MaxCount,
		maxAge:   cfg.MaxAge,
		logger:   cfg.Logger,
		now:      time.Now,
		subs:     make(map[*Subscription]struct{}),
	}

	if err := os.MkdirAll(filepath.Dir(l.path), 0o750); err != nil {
		return nil, fmt.Errorf("create the events file's directory: %w", err)
	}
	if err := l.load(); err != nil {
		return nil, err
	}
	if err := l.compact(); err != nil {
		return nil, err
	}

	return l, nil
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

		var e types.Event
		if err := json.Unmarshal(line, &e); err != nil {
			l.logger.Warn("skipping an unreadable event", slog.Any("error", err))
			continue
		}
		l.events = append(l.events, e)
	}

	return nil
}

// Record timestamps e if needed, keeps it, and passes it to every matching
// subscriber. Write errors are logged.
func (l *Log) Record(e types.Event) {
	if e.Time.IsZero() {
		e.Time = l.now()
	}
	e = clone(e)

	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.append(e); err != nil {
		l.logger.Warn("cannot write an event",
			slog.String("kind", string(e.Kind)),
			slog.String("name", e.Name),
			slog.String("action", string(e.Action)),
			slog.Any("error", err))
	}
	l.events = append(l.events, e)
	l.trim()

	if l.written > l.maxCount+l.maxCount/4 {
		if err := l.compact(); err != nil {
			l.logger.Warn("cannot compact the events", slog.Any("error", err))
		}
	}

	for sub := range l.subs {
		if sub.filter.Matches(e) {
			sub.send(clone(e))
		}
	}
}

// append writes e to the end of the file. The caller must hold l.mu.
func (l *Log) append(e types.Event) error {
	if l.file == nil {
		return errors.New("the events file is not open")
	}

	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := l.file.Write(append(line, '\n')); err != nil {
		return err
	}
	l.written++

	return nil
}

// trim drops the events past the log's limits, oldest first. The caller
// must hold l.mu.
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

// compact rewrites the file with only the events kept, and reopens it for
// appending. The caller must hold l.mu, or be Open.
func (l *Log) compact() error {
	l.trim()

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, e := range l.events {
		if err := enc.Encode(e); err != nil {
			return fmt.Errorf("encode event: %w", err)
		}
	}
	if err := writeFile(l.path, buf.Bytes()); err != nil {
		return fmt.Errorf("write events: %w", err)
	}

	if l.file != nil {
		_ = l.file.Close()
	}
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND, fileMode)
	if err != nil {
		l.file = nil
		return fmt.Errorf("open events: %w", err)
	}
	l.file = f
	l.written = len(l.events)

	return nil
}

// writeFile replaces the file at path with data atomically, by writing beside
// it and renaming over it.
func writeFile(path string, data []byte) error {
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
func (l *Log) List(f types.EventFilter, limit int) []types.Event {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.list(f, limit)
}

// list is List. The caller must hold l.mu.
func (l *Log) list(f types.EventFilter, limit int) []types.Event {
	var out []types.Event
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

// Close stops the log: its subscriptions end, and nothing more is written.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	for sub := range l.subs {
		sub.end(nil)
	}
	if l.file == nil {
		return nil
	}

	err := l.file.Close()
	l.file = nil

	return err
}

// clone returns e with its own copy of the attributes.
func clone(e types.Event) types.Event {
	e.Attributes = maps.Clone(e.Attributes)

	return e
}
