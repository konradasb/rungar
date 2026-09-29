// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package config

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/konradasb/rungar/internal/errdefs"
)

// Version is the configuration version this Rungar writes, and the newest it
// reads. It changes when a key is removed or a value changes meaning, never
// for a key added.
const Version = 1

// oldestVersion is the oldest configuration version this Rungar reads. It is
// a variable so tests can raise it.
var oldestVersion = 1

// A Deprecation is something in a configuration file that this Rungar still
// reads, but a later one may not. rungar config migrate rewrites it.
type Deprecation struct {
	// Line is the line of the file it is on, or 0 for the file as a whole.
	Line int

	// Message says what is deprecated, and what replaces it.
	Message string
}

// String returns the deprecation as printed, after the file's name.
func (d Deprecation) String() string {
	if d.Line == 0 {
		return d.Message
	}

	return fmt.Sprintf("line %d: %s", d.Line, d.Message)
}

// A migration rewrites a deprecated form of the configuration -- a key
// renamed, a value whose meaning changed -- into what replaces it.
type migration struct {
	// until is the last version the old form is read in. A file declaring a
	// later one is not rewritten, and the old form in it is refused.
	until int

	// apply rewrites the file's top-level mapping in place, and returns
	// what it changed, on the lines the file had.
	apply func(root *yaml.Node) []Deprecation
}

// migrations are the deprecated forms this Rungar still reads, oldest first. A
// key renamed or removed, or a value whose meaning changed, adds one here; one
// whose until is older than oldestVersion is deleted with it.
var migrations []migration

// migrate rewrites a parsed configuration file for Version, in place: every
// deprecated form replaced by what replaces it, and its version set. It returns
// what is deprecated in the file, and whether anything but the version was
// rewritten. A file with no version is version 1. A file that is not a
// mapping is left for decoding to say what is wrong with it.
func migrate(doc *yaml.Node) ([]Deprecation, bool, error) {
	root := rootOf(doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return nil, false, nil
	}

	value := versionNode(root)

	version := 1
	if value != nil {
		v, err := strconv.Atoi(value.Value)
		if err != nil || value.ShortTag() != "!!int" {
			return nil, false, errdefs.InvalidArgument("line %d: version %q: want a whole number, such as %d",
				value.Line, value.Value, Version)
		}
		version = v
	}

	var deprecations []Deprecation

	switch {
	case version > Version:
		return nil, false, errdefs.InvalidArgument("line %d: version %d is newer than this Rungar reads: "+
			"upgrade Rungar, or write the file for version %d", value.Line, version, Version)
	case version < oldestVersion && value == nil:
		return nil, false, errdefs.InvalidArgument("version is not set, and is taken as 1, which is older than "+
			"this Rungar reads, %d to %d: rewrite it with rungar config migrate of a release that reads both",
			oldestVersion, Version)
	case version < oldestVersion:
		return nil, false, errdefs.InvalidArgument("line %d: version %d is older than this Rungar reads, "+
			"%d to %d: rewrite it with rungar config migrate of a release that reads both",
			value.Line, version, oldestVersion, Version)
	case value == nil:
		deprecations = append(deprecations, Deprecation{
			Message: fmt.Sprintf("version is not set, and is taken as 1; set it to %d", Version),
		})
	case version < Version:
		deprecations = append(deprecations, Deprecation{
			Line:    value.Line,
			Message: fmt.Sprintf("version %d is deprecated; this Rungar writes version %d", version, Version),
		})
	}

	rewritten := false
	for _, m := range migrations {
		if version > m.until {
			continue
		}
		changes := m.apply(root)
		deprecations = append(deprecations, changes...)
		rewritten = rewritten || len(changes) > 0
	}

	setVersion(root)

	return deprecations, rewritten, nil
}

// Migrate returns the configuration file at path rewritten for this Rungar:
// its version set to Version and every deprecated form in it replaced, so that
// it means what it did and loads without a warning. It returns what it
// changed, and the file as it is when nothing needs to. It fails if the file,
// rewritten, would not load.
//
// Comments are kept. Only the version's line changes, unless a deprecated form
// had to be rewritten, which lays the whole file out afresh.
func Migrate(path string) ([]byte, []Deprecation, error) {
	b, err := readFile(path)
	if err != nil {
		return nil, nil, err
	}

	f, err := parseFile(b, path)
	if err != nil {
		return nil, nil, err
	}
	if _, err := f.decode(); err != nil {
		return nil, nil, err
	}
	if len(f.deprecations) == 0 {
		return b, nil, nil
	}

	out, err := rewrite(b, &f.doc, f.rewritten)
	if err != nil {
		return nil, nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	if _, err := load(out, path); err != nil {
		return nil, nil, fmt.Errorf("the migrated configuration would not load, so nothing was changed: %w", err)
	}

	return out, f.deprecations, nil
}

// rewrite returns the file src, parsed and migrated as doc, as text: src with
// only its version changed where nothing else was rewritten and the layout
// allows, and doc laid out afresh otherwise.
func rewrite(src []byte, doc *yaml.Node, rewritten bool) ([]byte, error) {
	if !rewritten {
		if out, ok := withVersion(src); ok {
			return out, nil
		}
	}

	return encodeYAML(doc)
}

// withVersion returns src with its version set to Version and nothing else
// changed: the value replaced where src sets one, or a line added above the
// first key and the comment on it. It reports false for a file with nowhere
// to put one, such as a mapping written in flow style, and for one the edit
// would change more than the version of, such as one whose first key is
// written after "?".
func withVersion(src []byte) ([]byte, bool) {
	out, ok := editVersion(src)
	if !ok || !sameApartFromVersion(src, out) {
		return nil, false
	}

	return out, true
}

// editVersion sets src's version by editing its text, as withVersion
// describes, without checking the result. It reports false for a file with a
// line ended by a carriage return alone, which YAML counts as a line and
// editVersion would not.
func editVersion(src []byte) ([]byte, bool) {
	if bytes.Contains(bytes.ReplaceAll(src, []byte("\r\n"), nil), []byte("\r")) {
		return nil, false
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, false
	}

	root := rootOf(&doc)
	if root == nil || root.Kind != yaml.MappingNode || root.Style&yaml.FlowStyle != 0 || len(root.Content) == 0 {
		return nil, false
	}

	lines := strings.SplitAfter(string(src), "\n")
	version := strconv.Itoa(Version)

	if value := versionNode(root); value != nil {
		line := lines[value.Line-1]

		start := len(string([]rune(line)[:value.Column-1]))
		end := strings.IndexFunc(line[start:], unicode.IsSpace)
		if end < 0 {
			end = len(line) - start
		}
		lines[value.Line-1] = line[:start] + version + line[start+end:]

		return []byte(strings.Join(lines, "")), true
	}

	newline := "\n"
	if bytes.Contains(src, []byte("\r\n")) {
		newline = "\r\n"
	}

	// Above the comment on the first key, as it is about that key.
	first := root.Content[0]
	at := first.Line - 1
	for at > 0 && strings.HasPrefix(strings.TrimSpace(lines[at-1]), "#") {
		at--
	}

	indent := strings.Repeat(" ", first.Column-1)
	lines = slices.Insert(lines, at, indent+"version: "+version+newline+newline)

	return []byte(strings.Join(lines, "")), true
}

// sameApartFromVersion reports whether edited reads as src does, but for a
// version of Version.
func sameApartFromVersion(src, edited []byte) bool {
	var before, after map[string]any
	if yaml.Unmarshal(src, &before) != nil || yaml.Unmarshal(edited, &after) != nil {
		return false
	}
	if after["version"] != Version {
		return false
	}

	delete(before, "version")
	delete(after, "version")

	return reflect.DeepEqual(before, after)
}

// rootOf returns a parsed file's top-level node, or nil for an empty file.
func rootOf(doc *yaml.Node) *yaml.Node {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}

	return doc.Content[0]
}

// versionNode returns the value of a top-level mapping's version, or nil when
// it has none.
func versionNode(root *yaml.Node) *yaml.Node {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "version" {
			return root.Content[i+1]
		}
	}

	return nil
}

// setVersion sets a top-level mapping's version to Version, adding it first
// where the mapping has none.
func setVersion(root *yaml.Node) {
	value := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(Version)}

	if old := versionNode(root); old != nil {
		value.LineComment = old.LineComment
		*old = *value

		return
	}

	key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "version"}
	root.Content = append([]*yaml.Node{key, value}, root.Content...)
}
