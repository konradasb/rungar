// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"
)

// TestValidateWarnsOfDeprecations checks validate passes a file with something
// deprecated in it, and says so on stderr, with what rewrites it.
func TestValidateWarnsOfDeprecations(t *testing.T) {
	path := writeConfigFile(t, unversioned, 0o600)

	stdout, stderr, err := runLocal(t, "validate", "-f", path)
	if err != nil {
		t.Fatalf("rungar validate = %v", err)
	}

	if !strings.Contains(stdout, "is valid") {
		t.Errorf("stdout = %q, want the file valid", stdout)
	}
	for _, want := range []string{"Warning: " + path + ": version is not set", "rungar config migrate -f " + path} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}
