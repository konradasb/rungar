// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

// Package errdefs defines the classes of error that cross package boundaries,
// matched with errors.Is.
package errdefs

import (
	"errors"
	"fmt"
)

// The error classes. The constructor of the same name puts an error in one
// without adding the class to its message: NotFound("no host %q", name) reads
// `no host "compute1"` and matches ErrNotFound.
var (
	// ErrInvalidArgument is a malformed configuration or request, or one that
	// can never succeed, such as a runner its provider can never make.
	ErrInvalidArgument = errors.New("invalid argument")

	// ErrNotFound is a provider, scale set or runner that does not exist.
	ErrNotFound = errors.New("not found")

	// ErrNoCapacity is a provider, or every provider, being full. It is not a
	// fault, and may pass once a job finishes.
	ErrNoCapacity = errors.New("no capacity")

	// ErrBusy is a runner that cannot be removed because it is running a job.
	ErrBusy = errors.New("busy")

	// ErrUnavailable is something needed that may be available later, such
	// as a scale set while it starts.
	ErrUnavailable = errors.New("unavailable")
)

// InvalidArgument returns an error in the ErrInvalidArgument class, formatted
// as by fmt.Errorf.
func InvalidArgument(format string, args ...any) error {
	return classify(ErrInvalidArgument, format, args...)
}

// NotFound returns an error in the ErrNotFound class.
func NotFound(format string, args ...any) error {
	return classify(ErrNotFound, format, args...)
}

// NoCapacity returns an error in the ErrNoCapacity class.
func NoCapacity(format string, args ...any) error {
	return classify(ErrNoCapacity, format, args...)
}

// Busy returns an error in the ErrBusy class.
func Busy(format string, args ...any) error {
	return classify(ErrBusy, format, args...)
}

// Unavailable returns an error in the ErrUnavailable class.
func Unavailable(format string, args ...any) error {
	return classify(ErrUnavailable, format, args...)
}

// classified is an error in a class; its message leaves the class out.
type classified struct {
	class error
	err   error
}

func classify(class error, format string, args ...any) error {
	return &classified{class: class, err: fmt.Errorf(format, args...)}
}

func (e *classified) Error() string { return e.err.Error() }

// Unwrap returns the class, and whatever the message wrapped with %w.
func (e *classified) Unwrap() []error { return []error{e.err, e.class} }
