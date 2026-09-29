// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// versioned is valid, declaring the current version.
var versioned = fmt.Sprintf("version: %d\n", Version) + valid

// renameLog is a migration for tests: log, deprecated, renamed log_level.
func renameLog(until int) migration {
	return migration{
		until: until,
		apply: func(root *yaml.Node) []Deprecation {
			for i := 0; i+1 < len(root.Content); i += 2 {
				if key := root.Content[i]; key.Value == "log" {
					key.Value = "log_level"
					return []Deprecation{{Line: key.Line, Message: "log is deprecated: renamed log_level"}}
				}
			}

			return nil
		},
	}
}

// withMigrations has the configuration read with steps in place of this
// Rungar's migrations, for the rest of the test.
func withMigrations(t *testing.T, steps ...migration) {
	t.Helper()

	saved := migrations
	migrations = steps
	t.Cleanup(func() { migrations = saved })
}

// TestUnversionedFileIsReadAsVersion1 checks a file from before versions were
// declared loads as version 1, with a warning to declare it.
func TestUnversionedFileIsReadAsVersion1(t *testing.T) {
	cfg, err := Load(writeConfig(t, valid))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}

	if cfg.Version != 1 {
		t.Errorf("version = %d, want 1", cfg.Version)
	}
	got := cfg.Deprecations()
	if len(got) != 1 || !strings.Contains(got[0].Message, "version is not set") {
		t.Errorf("Deprecations() = %v, want one saying the version is not set", got)
	}
}

// TestCurrentVersionHasNothingDeprecated checks a file at the current version
// loads without a deprecation.
func TestCurrentVersionHasNothingDeprecated(t *testing.T) {
	cfg, err := Load(writeConfig(t, versioned))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}

	if cfg.Version != Version || len(cfg.Deprecations()) != 0 {
		t.Errorf("version %d, deprecations %v; want %d, and none", cfg.Version, cfg.Deprecations(), Version)
	}
}

// TestUnreadableVersionIsRefused checks a version this Rungar cannot read, or
// that is not a whole number, is refused on its line, saying what to do.
func TestUnreadableVersionIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name, version, want string
	}{
		{"newer", strconv.Itoa(Version + 1), "upgrade Rungar"},
		{"older", strconv.Itoa(oldestVersion - 1), "older than this Rungar reads"},
		{"a word", "one", "want a whole number"},
		{"quoted", `"1"`, "want a whole number"},
		{"fractional", "1.5", "want a whole number"},
		{"a mapping", "{major: 1}", "want a whole number"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, "version: "+tt.version+"\n"+valid))
			if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "line 1") {
				t.Errorf("Load() = %v, want it to say %q, on line 1", err, tt.want)
			}
		})
	}
}

// TestUnversionedFileOlderThanReadIsRefused checks a file with no version,
// taken as version 1, is refused once version 1 is no longer read, rather
// than the missing version's line being looked up.
func TestUnversionedFileOlderThanReadIsRefused(t *testing.T) {
	saved := oldestVersion
	oldestVersion = 2
	t.Cleanup(func() { oldestVersion = saved })

	_, err := Load(writeConfig(t, valid))
	if err == nil || !strings.Contains(err.Error(), "version is not set") ||
		!strings.Contains(err.Error(), "older than this Rungar reads") {
		t.Errorf("Load() = %v, want it refused as unset and too old", err)
	}
}

// TestDeprecatedFormIsReadWithAWarning checks a deprecated key loads as what
// replaces it, and is warned of on the line the file has it.
func TestDeprecatedFormIsReadWithAWarning(t *testing.T) {
	withMigrations(t, renameLog(Version))

	cfg, err := Load(writeConfig(t, versioned+"log: debug\n"))
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}

	if cfg.LogLevel != "debug" {
		t.Errorf("log level = %q, want debug, from the deprecated key", cfg.LogLevel)
	}
	want := Deprecation{Line: strings.Count(versioned, "\n") + 1, Message: "log is deprecated: renamed log_level"}
	if got := cfg.Deprecations(); !slices.Equal(got, []Deprecation{want}) {
		t.Errorf("Deprecations() = %v, want %v", got, []Deprecation{want})
	}
}

// TestDeprecatedFormIsRefusedAfterItsLastVersion checks a migration leaves a
// file declaring a later version alone, so the old form is an unknown key.
func TestDeprecatedFormIsRefusedAfterItsLastVersion(t *testing.T) {
	withMigrations(t, renameLog(Version-1))

	_, err := Load(writeConfig(t, versioned+"log: debug\n"))
	if err == nil || !strings.Contains(err.Error(), "field log not found") {
		t.Errorf("Load() = %v, want log refused as unknown", err)
	}
}

// TestMigrateSetsTheVersion checks a file with no version gains one and is
// otherwise as it was: comments, blank lines and layout.
func TestMigrateSetsTheVersion(t *testing.T) {
	body := strings.TrimPrefix(valid, "\n")
	version := fmt.Sprintf("version: %d\n\n", Version)

	for _, tt := range []struct {
		name, in, want string
	}{
		{
			name: "no comments",
			in:   body,
			want: version + body,
		},
		{
			name: "a comment on the first key",
			in:   "# GitHub, and the credential.\n" + body,
			want: version + "# GitHub, and the credential.\n" + body,
		},
		{
			name: "a comment on the file",
			in:   "# Rungar's configuration.\n\n" + body,
			want: "# Rungar's configuration.\n\n" + version + body,
		},
		{
			name: "a document marker",
			in:   "---\n" + body,
			want: "---\n" + version + body,
		},
		{
			name: "CRLF line endings",
			in:   strings.ReplaceAll(body, "\n", "\r\n"),
			want: strings.ReplaceAll(version+body, "\n", "\r\n"),
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, changes, err := Migrate(writeConfig(t, tt.in))
			if err != nil {
				t.Fatalf("Migrate() = %v", err)
			}

			if string(got) != tt.want {
				t.Errorf("Migrate() =\n%s\nwant\n%s", got, tt.want)
			}
			if len(changes) != 1 {
				t.Errorf("changes = %v, want the version set", changes)
			}
		})
	}
}

// TestWithVersionReplacesTheValueAlone checks an older version is replaced
// where it is written, keeping the comment beside it.
func TestWithVersionReplacesTheValueAlone(t *testing.T) {
	in := "# Rungar.\nversion:  0   # the first\ngithub: {}\n"

	got, ok := withVersion([]byte(in))
	if want := fmt.Sprintf("# Rungar.\nversion:  %d   # the first\ngithub: {}\n", Version); !ok || string(got) != want {
		t.Errorf("withVersion() = %q, %v; want %q", got, ok, want)
	}
}

// TestMigrateLeavesACurrentFileAlone checks a file with nothing to migrate is
// returned byte for byte, with no changes.
func TestMigrateLeavesACurrentFileAlone(t *testing.T) {
	got, changes, err := Migrate(writeConfig(t, versioned))
	if err != nil {
		t.Fatalf("Migrate() = %v", err)
	}

	if string(got) != versioned || len(changes) != 0 {
		t.Errorf("Migrate() = %v changes, and\n%s\nwant none, and the file as it is", changes, got)
	}
}

// TestMigrateRewritesDeprecatedForms checks a deprecated key is replaced, the
// comments kept, and that the result loads without a warning.
func TestMigrateRewritesDeprecatedForms(t *testing.T) {
	withMigrations(t, renameLog(Version))

	got, changes, err := Migrate(writeConfig(t, valid+"# How much to say.\nlog: debug\n"))
	if err != nil {
		t.Fatalf("Migrate() = %v", err)
	}

	if len(changes) != 2 {
		t.Errorf("changes = %v, want the version set and log renamed", changes)
	}
	for _, want := range []string{fmt.Sprintf("version: %d\n", Version), "# How much to say.\nlog_level: debug"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("Migrate() lacks %q:\n%s", want, got)
		}
	}

	cfg, err := Load(writeConfig(t, string(got)))
	if err != nil {
		t.Fatalf("Load() of the migrated file = %v\n%s", err, got)
	}
	if cfg.LogLevel != "debug" || len(cfg.Deprecations()) != 0 {
		t.Errorf("migrated: log level %q, deprecations %v; want debug, and none", cfg.LogLevel, cfg.Deprecations())
	}
}

// TestMigrateRefusesAFileThatDoesNotLoad checks Migrate says what keeps a file
// from loading and leaves the file unchanged.
func TestMigrateRefusesAFileThatDoesNotLoad(t *testing.T) {
	path := writeConfig(t, valid+"reconcile_interval: 1s\n")

	if _, _, err := Migrate(path); err == nil || !strings.Contains(err.Error(), "reconcile_interval") {
		t.Errorf("Migrate() = %v, want it to say what keeps the file from loading", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != valid+"reconcile_interval: 1s\n" {
		t.Errorf("the file was changed:\n%s", b)
	}
}

// TestWithVersionRefusesAnEditThatChangesMore checks a file the edit would
// change more than the version of, here by splitting an explicit key from its
// "?", is left to the encoder.
func TestWithVersionRefusesAnEditThatChangesMore(t *testing.T) {
	if got, ok := withVersion([]byte("?\n  log_level\n: info\n")); ok {
		t.Errorf("withVersion() = %q, true; want false", got)
	}
}

// TestWithVersionLeavesCarriageReturnLinesToTheEncoder checks a file with
// lines ended by a carriage return alone is not edited by hand, as its lines
// are not where YAML puts its keys.
func TestWithVersionLeavesCarriageReturnLinesToTheEncoder(t *testing.T) {
	if got, ok := withVersion([]byte("\r\rlog_level: info")); ok {
		t.Errorf("withVersion() = %q, true; want false", got)
	}
}

// FuzzEditVersion checks that editing a file's text for its version keeps
// every other line as it was, comments and all: it replaces the version's
// line, or adds one above the first key with a blank line after it.
func FuzzEditVersion(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, src []byte) {
		out, ok := editVersion(src)
		if !ok {
			return
		}

		before := strings.SplitAfter(string(src), "\n")
		after := strings.SplitAfter(string(out), "\n")

		at := 0
		for at < len(before) && at < len(after) && before[at] == after[at] {
			at++
		}

		want := "version: " + strconv.Itoa(Version)
		switch len(after) - len(before) {
		case 0:
			if at < len(after) && !strings.Contains(after[at], strconv.Itoa(Version)) {
				t.Errorf("line %d changed to %q, which has no version\n%q\nfrom\n%q",
					at+1, after[at], out, src)
			}
			at++
		case 2:
			if strings.TrimSpace(after[at]) != want || strings.TrimSpace(after[at+1]) != "" {
				t.Errorf("added %q, want %q and a blank line\n%q\nfrom\n%q",
					after[at:at+2], want, out, src)
			}
			after = append(after[:at:at], after[at+2:]...)
		default:
			t.Fatalf("the edit took %d lines to %d\n%q\nfrom\n%q", len(before), len(after), out, src)
		}

		if !slices.Equal(before[min(at, len(before)):], after[min(at, len(after)):]) {
			t.Errorf("the edit changed more than the version's line\n%q\nfrom\n%q", out, src)
		}
	})
}
