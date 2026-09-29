// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"errors"
	"net/http"
	"strings"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

// refusal is what Compute Engine's refusal of an instance says of the zone.
type refusal int

const (
	// refusalOther is Compute Engine failing.
	refusalOther refusal = iota

	// refusalFull is a zone out of stock, or the project out of quota.
	refusalFull

	// refusalInvalid is an instance that can never be made: a machine type
	// or image that does not exist, a malformed field.
	refusalInvalid
)

// operationError is a failed operation's errors.
type operationError struct {
	errors []*compute.OperationErrorErrors
}

func (e *operationError) Error() string {
	parts := make([]string, 0, len(e.errors))
	for _, oe := range e.errors {
		parts = append(parts, oe.Code+": "+oe.Message)
	}

	return strings.Join(parts, "; ")
}

// classify returns what an error making an instance says of the zone. Both a
// failed request and a failed operation are read: a stockout usually comes
// as the latter.
func classify(err error) refusal {
	var opErr *operationError
	if errors.As(err, &opErr) {
		for _, oe := range opErr.errors {
			if r := classifyCode(oe.Code); r != refusalOther {
				return r
			}
		}

		return refusalOther
	}

	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) {
		return refusalOther
	}

	for _, item := range apiErr.Errors {
		if r := classifyCode(item.Reason); r != refusalOther {
			return r
		}
	}

	switch apiErr.Code {
	case http.StatusBadRequest, http.StatusNotFound:
		return refusalInvalid
	default:
		return refusalOther
	}
}

// classifyCode classifies an operation error's code, or a request error's
// reason.
func classifyCode(code string) refusal {
	switch {
	case strings.Contains(code, "RESOURCE_POOL_EXHAUSTED"),
		strings.Contains(code, "STOCKOUT"),
		code == "QUOTA_EXCEEDED",
		code == "quotaExceeded":
		return refusalFull
	case strings.HasPrefix(code, "INVALID_"),
		code == "RESOURCE_NOT_FOUND",
		code == "NOT_FOUND",
		code == "invalid",
		code == "notFound":
		return refusalInvalid
	default:
		return refusalOther
	}
}

// errorMessage returns the message of an error from Compute Engine, without
// the request that failed.
func errorMessage(err error) string {
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) && apiErr.Message != "" {
		return apiErr.Message
	}

	return err.Error()
}
