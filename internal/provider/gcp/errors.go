// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"errors"
	"net/http"
	"strings"

	"cloud.google.com/go/auth"
	"golang.org/x/oauth2"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

// refusal is what Compute Engine's refusal of an instance says of the zone.
type refusal int

const (
	// refusalOther is Compute Engine refusing for a reason that says
	// nothing of the zone, which another zone may not share. It is the zero
	// value: what is not known to be one of the others.
	refusalOther refusal = iota

	// refusalFull is a zone out of stock: another zone may have the
	// machine.
	refusalFull

	// refusalQuota is the project out of quota. Compute Engine's quotas are a
	// region's or the whole project's, so every other zone refuses the same.
	refusalQuota

	// refusalInvalid is an instance that can never be created: a machine type
	// or image that does not exist, a malformed field.
	refusalInvalid

	// refusalUncertain is Compute Engine not answering, failing as it
	// answered, or not saying how an operation it took ended: the outcome is
	// unknown, and the instance may have been created.
	refusalUncertain
)

// waitError is a failure to learn how an operation ended, which it may yet
// do well.
type waitError struct {
	err error
}

func (e *waitError) Error() string { return "wait for the operation: " + e.err.Error() }

func (e *waitError) Unwrap() error { return e.err }

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

// refusalOf returns what an error creating an instance says of the zone. Both a
// failed request and a failed operation are read: a stockout or an exceeded
// quota comes as the latter. A request Compute Engine did not answer, or
// answered with a server error, and an operation whose end was not learnt,
// are refusalUncertain; a request never sent, for want of a token, is
// refusalOther.
func refusalOf(err error) refusal {
	var waitErr *waitError
	if errors.As(err, &waitErr) {
		return refusalUncertain
	}

	if isCredentialError(err) {
		return refusalOther
	}

	var operationErr *operationError
	if errors.As(err, &operationErr) {
		for _, oe := range operationErr.errors {
			if r := refusalOfCode(oe.Code); r != refusalOther {
				return r
			}
		}

		return refusalOther
	}

	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) || apiErr.Code >= http.StatusInternalServerError {
		return refusalUncertain
	}

	for _, item := range apiErr.Errors {
		if r := refusalOfCode(item.Reason); r != refusalOther {
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

// refusalOfCode returns the refusal an operation error's code, or a request
// error's reason, is.
func refusalOfCode(code string) refusal {
	switch code {
	case "ZONE_RESOURCE_POOL_EXHAUSTED",
		"ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS",
		"REGION_RESOURCE_POOL_EXHAUSTED",
		"RESOURCE_POOL_EXHAUSTED":
		return refusalFull
	case "QUOTA_EXCEEDED",
		"quotaExceeded":
		return refusalQuota
	case "RESOURCE_NOT_FOUND",
		"NOT_FOUND",
		"invalid",
		"notFound":
		return refusalInvalid
	}

	switch {
	// A stockout of a particular resource, such as a GPU, has a code of its
	// own ending STOCKOUT, and Compute Engine documents no full list of them.
	case strings.HasSuffix(code, "STOCKOUT"):
		return refusalFull
	// A malformed request's code names what was wrong with it --
	// INVALID_FIELD_VALUE, INVALID_USAGE and more -- from an open-ended set.
	case strings.HasPrefix(code, "INVALID_"):
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

// isCredentialError reports whether err is a failure to get the token a
// request is sent with, from either of the libraries the client authenticates
// with. Such a request never reached Compute Engine.
func isCredentialError(err error) bool {
	var authErr *auth.Error
	var retrieveErr *oauth2.RetrieveError

	return errors.As(err, &authErr) || errors.As(err, &retrieveErr)
}

// isTransient reports whether a call that failed may succeed if made again:
// Compute Engine did not answer, answered with a server error, or asked to be
// called later. A call it refused otherwise would be refused again.
func isTransient(err error) bool {
	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) {
		return true
	}

	return apiErr.Code >= http.StatusInternalServerError ||
		apiErr.Code == http.StatusTooManyRequests || apiErr.Code == http.StatusRequestTimeout
}

// isNotFound reports whether err is Compute Engine saying a resource is not
// there: as the request, or as its operation, when the resource went while
// the operation ran. A wait for an operation answered 404 is not: it is the
// operation Compute Engine does not know, and the resource may be there yet.
func isNotFound(err error) bool {
	var waitErr *waitError
	if errors.As(err, &waitErr) {
		return false
	}

	var operationErr *operationError
	if errors.As(err, &operationErr) {
		for _, oe := range operationErr.errors {
			if oe.Code == "RESOURCE_NOT_FOUND" {
				return true
			}
		}

		return false
	}

	var apiErr *googleapi.Error
	return errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound
}
