// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// APIError is an unexpected response from GitHub.
type APIError struct {
	Method     string
	Path       string
	StatusCode int

	// Message is GitHub's message, or the start of the body if it is not
	// JSON.
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github: %s %s: %d %s: %s",
		e.Method, e.Path, e.StatusCode, http.StatusText(e.StatusCode), e.Message)
}

// newAPIError reads an unexpected response into an APIError.
func newAPIError(resp *http.Response) *APIError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))

	var parsed struct {
		Message string `json:"message"`
	}
	msg := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &parsed) == nil && parsed.Message != "" {
		msg = parsed.Message
	}

	return &APIError{
		Method:     resp.Request.Method,
		Path:       resp.Request.URL.Path,
		StatusCode: resp.StatusCode,
		Message:    msg,
	}
}
