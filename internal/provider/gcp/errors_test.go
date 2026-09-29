// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package gcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"

	"cloud.google.com/go/auth"
	"golang.org/x/oauth2"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

// TestCodesAreReadAsRefusals pins what each of Compute Engine's codes, and
// reasons, says of the zone.
func TestCodesAreReadAsRefusals(t *testing.T) {
	tests := []struct {
		code string
		want refusal
	}{
		{"ZONE_RESOURCE_POOL_EXHAUSTED", refusalFull},
		{"ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS", refusalFull},
		{"REGION_RESOURCE_POOL_EXHAUSTED", refusalFull},
		{"RESOURCE_POOL_EXHAUSTED", refusalFull},
		{"GPU_STOCKOUT", refusalFull},
		{"QUOTA_EXCEEDED", refusalQuota},
		{"quotaExceeded", refusalQuota},
		{"INVALID_FIELD_VALUE", refusalInvalid},
		{"INVALID_USAGE", refusalInvalid},
		{"RESOURCE_NOT_FOUND", refusalInvalid},
		{"NOT_FOUND", refusalInvalid},
		{"invalid", refusalInvalid},
		{"notFound", refusalInvalid},
		{"INTERNAL_ERROR", refusalOther},
		{"backendError", refusalOther},
		{"", refusalOther},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			if got := refusalOfCode(tt.code); got != tt.want {
				t.Errorf("refusalOfCode(%q) = %d, want %d", tt.code, got, tt.want)
			}
		})
	}
}

// TestErrorsAreReadAsRefusals pins what each kind of error creating an
// instance says: a request Compute Engine did not answer, or answered with a
// server error, and an operation whose end was not learnt, leave unknown
// whether the instance was created.
func TestErrorsAreReadAsRefusals(t *testing.T) {
	apiError := func(status int, reason string) error {
		return &googleapi.Error{Code: status, Errors: []googleapi.ErrorItem{{Reason: reason}}}
	}

	tests := []struct {
		name string
		err  error
		want refusal
	}{
		{"transport error", &url.Error{Op: "Post", URL: "https://compute", Err: io.ErrUnexpectedEOF}, refusalUncertain},
		{"timeout", context.DeadlineExceeded, refusalUncertain},
		{"internal server error", apiError(http.StatusInternalServerError, "backendError"), refusalUncertain},
		{"unavailable", apiError(http.StatusServiceUnavailable, "backendError"), refusalUncertain},
		{"wait failed", &waitError{err: apiError(http.StatusForbidden, "forbidden")}, refusalUncertain},
		{"forbidden", apiError(http.StatusForbidden, "forbidden"), refusalOther},
		{"rate limited", apiError(http.StatusTooManyRequests, "rateLimitExceeded"), refusalOther},
		{"quota", apiError(http.StatusForbidden, "quotaExceeded"), refusalQuota},
		{"bad request", apiError(http.StatusBadRequest, "invalid"), refusalInvalid},
		{"operation failed", &operationError{errors: []*compute.OperationErrorErrors{{Code: "INTERNAL_ERROR"}}}, refusalOther},
		{"operation out of stock", &operationError{errors: []*compute.OperationErrorErrors{{Code: "ZONE_RESOURCE_POOL_EXHAUSTED"}}}, refusalFull},
		// A request never sent, for want of a token, created nothing.
		{"no token", &url.Error{Op: "Post", URL: "https://compute", Err: &oauth2.RetrieveError{
			Response: &http.Response{StatusCode: http.StatusBadRequest},
		}}, refusalOther},
		{"no token, from the auth library", &url.Error{Op: "Post", URL: "https://compute", Err: &auth.Error{
			Response: &http.Response{StatusCode: http.StatusServiceUnavailable},
		}}, refusalOther},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := refusalOf(fmt.Errorf("insert: %w", tt.err)); got != tt.want {
				t.Errorf("refusalOf(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

// TestNotFoundErrors pins which errors say a resource is not there: not a
// wait answered 404, which is Compute Engine not knowing the operation.
func TestNotFoundErrors(t *testing.T) {
	notFound := &googleapi.Error{Code: http.StatusNotFound}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"request", notFound, true},
		{"operation", &operationError{errors: []*compute.OperationErrorErrors{{Code: "RESOURCE_NOT_FOUND"}}}, true},
		{"wait", &waitError{err: notFound}, false},
		{"other", &googleapi.Error{Code: http.StatusForbidden}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNotFound(fmt.Errorf("delete: %w", tt.err)); got != tt.want {
				t.Errorf("isNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
