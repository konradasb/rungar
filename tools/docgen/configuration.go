// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/konradasb/rungar/internal/config"
)

// module is this module's path.
const module = "github.com/konradasb/rungar"

// Packages declaring the types a configuration is read into. Each provider
// type's are in a package under providerBasePackage named for it.
const (
	configPackage       = module + "/internal/config"
	typesPackage        = module + "/internal/types"
	providerBasePackage = module + "/internal/provider"
)

// providerPackage is the package of the provider type of this name.
func providerPackage(name string) string {
	return providerBasePackage + "/" + name
}

// configurationSection is part of a configuration reference page: an
// introduction, then the keys of one struct under a prefix.
type configurationSection struct {
	intro, from, typ, prefix string
}

// configurationPage is a reference page of configuration keys.
type configurationPage struct {
	path     string
	meta     meta
	sections []configurationSection
}

// providerTypeDoc is what a provider type's configuration page says beyond
// its keys.
type providerTypeDoc struct {
	// runner says what a runner of the type is, and anything its runner
	// blocks must hold: "a Compute Engine instance."
	runner string

	// types maps named types the type's package declares for its keys to how
	// a configuration writes them, as specials does for the rest.
	types map[string]string
}

// providerTypeDocs has an entry for every provider type internal/config
// registers; a type without one fails docgen, so none goes undocumented.
var providerTypeDocs = map[string]providerTypeDoc{
	"aws": {runner: "an EC2 instance."},
	"dicer": {
		runner: "a virtual machine booted from a container image. A name given here -- a kernel, " +
			"a network, a volume -- must exist on every host the block is given to.",
		types: map[string]string{"MountType": "string: volume, file or tmpfs"},
	},
	"gcp":     {runner: "a Compute Engine instance."},
	"proxmox": {runner: "a VM cloned from a template."},
}

// configurationIntro opens the configuration reference.
const configurationIntro = "`rungar` reads its configuration from `/etc/rungar/config.yaml`, or the file " +
	"`--config` names, when it starts: a changed file is taken up by restarting it. Unknown keys are " +
	"rejected, and a file that does not load keeps the daemon from starting.\n\n" +
	"A file says which version of the configuration it is written for, in `version`. A later " +
	"Rungar reads it as that version meant, and warns of anything in it that is deprecated -- in " +
	"its log, and from `rungar validate` -- which `rungar config migrate` rewrites; see " +
	"[Upgrading]({{< relref \"/docs/guides/upgrading-and-uninstalling#migrating-the-configuration\" >}}).\n\n" +
	"A provider's own keys, and the keys of the `runner` blocks, are its type's to read: see " +
	"[Providers]({{< relref \"/docs/providers\" >}}).\n\n" +
	"## General {#general}\n\n" +
	"The daemon's own settings. The sections after them are GitHub, the providers, the scale " +
	"sets, the metrics and the events."

// writeConfiguration writes the configuration reference: the daemon's keys, and
// each provider type's, under dir, the site's content/docs directory. commands
// are every rungar command's path, which the text writes as code.
func writeConfiguration(root, dir string, commands map[string]bool) error {
	pages, err := configurationPages(dir)
	if err != nil {
		return err
	}

	r := &reference{
		root: root, packages: map[string]*goPackage{}, keys: map[string]bool{}, commands: commands,
		specials: maps.Clone(specials),
	}
	for name, doc := range providerTypeDocs {
		for typeName, written := range doc.types {
			r.specials[providerPackage(name)+"."+typeName] = written
		}
	}

	for _, p := range pages {
		for _, sec := range p.sections {
			if err := r.collectKeys(sec.from, sec.typ, sec.prefix); err != nil {
				return fmt.Errorf("%s: %w", filepath.Base(p.path), err)
			}
		}
	}

	for _, p := range pages {
		var body bytes.Buffer

		for _, sec := range p.sections {
			b, err := r.document(sec.from, sec.typ, sec.prefix, sec.intro)
			if err != nil {
				return fmt.Errorf("%s: %w", filepath.Base(p.path), err)
			}
			body.Write(b)
		}

		if err := writePage(p.path, p.meta, body.Bytes()); err != nil {
			return err
		}
	}

	return nil
}

// configurationPages returns the configuration reference's pages under dir:
// the daemon's, then one per provider type, in name order. It fails if a
// provider type has no entry in providerTypeDocs, or an entry names no provider
// type.
func configurationPages(dir string) ([]configurationPage, error) {
	types := config.ProviderTypes()

	for name := range providerTypeDocs {
		if !slices.Contains(types, name) {
			return nil, fmt.Errorf("providerTypeDocs has %q, which is no provider type", name)
		}
	}

	pages := []configurationPage{{
		path: filepath.Join(dir, "reference", "configuration.md"),
		meta: meta{
			title: "Configuration", weight: 1, icon: "cog",
			description: "Every key of rungar's configuration file.",
		},
		sections: []configurationSection{{intro: configurationIntro, from: configPackage, typ: "Config"}},
	}}

	for _, name := range types {
		doc, ok := providerTypeDocs[name]
		if !ok {
			return nil, fmt.Errorf("provider type %q has no entry in providerTypeDocs: add one", name)
		}

		from := providerPackage(name)
		pages = append(pages, configurationPage{
			path: filepath.Join(dir, "providers", name, "configuration.md"),
			meta: meta{
				title: "Configuration", weight: 9, icon: "cog",
				description: fmt.Sprintf("Every key of %s %s provider and its runner blocks.", article(name), name),
			},
			sections: []configurationSection{
				{
					intro: fmt.Sprintf("Beside the keys [every provider has]({{< relref \"/docs/providers\" >}}), "+
						"%s `%s` provider reads these.", article(name), name),
					from: from, typ: "Config",
				},
				{
					intro: "\n## `runner` {#runner}\n\n*mapping*\n\n" +
						"The keys of a `runner` block, the provider's and each scale set's for it: a runner is " +
						doc.runner,
					from: from, typ: "RunnerSpec", prefix: "runner",
				},
			},
		})
	}

	return pages, nil
}

// article returns the indefinite article for a word, by its first letter.
func article(word string) string {
	if word != "" && strings.ContainsRune("aeiou", rune(word[0])) {
		return "an"
	}

	return "a"
}

// reference parses the packages a configuration's types are declared in, and
// writes the reference of their keys.
type reference struct {
	root     string
	fset     token.FileSet
	packages map[string]*goPackage

	// keys are every key of every page, alone and under its prefix, and
	// commands every rungar command: what the text writes as code.
	keys, commands map[string]bool

	// specials maps named types to how a configuration writes them: the
	// package-level specials, and each provider type's from providerTypeDocs.
	specials map[string]string
}

// goPackage is a package's struct types.
type goPackage struct {
	path    string
	structs map[string]*structType
}

// structType is a struct as declared, with the imports its fields' types
// refer to.
type structType struct {
	pkg     *goPackage
	name    string
	fields  []*ast.Field
	imports map[string]string
}

// load parses a package of this module, without its tests.
func (r *reference) load(importPath string) (*goPackage, error) {
	if p, ok := r.packages[importPath]; ok {
		return p, nil
	}

	rel, ok := strings.CutPrefix(importPath, module+"/")
	if !ok {
		return nil, fmt.Errorf("%s is not part of %s", importPath, module)
	}

	dir := filepath.Join(r.root, filepath.FromSlash(rel))

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	p := &goPackage{path: importPath, structs: map[string]*structType{}}

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(&r.fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}

		imports := map[string]string{}
		for _, imp := range file.Imports {
			ipath, _ := strconv.Unquote(imp.Path.Value)
			alias := packageName(ipath)
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			imports[alias] = ipath
		}

		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}

			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if st, ok := ts.Type.(*ast.StructType); ok {
					p.structs[ts.Name.Name] = &structType{
						pkg: p, name: ts.Name.Name, fields: st.Fields.List, imports: imports,
					}
				}
			}
		}
	}

	r.packages[importPath] = p

	return p, nil
}

// structNamed returns the struct of this name in the package at importPath.
func (r *reference) structNamed(importPath, name string) (*structType, error) {
	p, err := r.load(importPath)
	if err != nil {
		return nil, err
	}

	st, ok := p.structs[name]
	if !ok {
		return nil, fmt.Errorf("no struct %s in %s", name, importPath)
	}

	return st, nil
}

// packageName returns the default name of the package at importPath: the last
// element of its path, less a version suffix.
func packageName(importPath string) string {
	name := path.Base(importPath)
	if i := strings.Index(name, ".v"); i > 0 {
		name = name[:i]
	}

	return name
}

// document returns the reference for a struct and every mapping under it, with
// its keys under prefix.
func (r *reference) document(importPath, name, prefix, intro string) ([]byte, error) {
	root, err := r.structNamed(importPath, name)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer

	out.WriteString(intro + "\n")

	if err := r.writeMapping(&out, root, prefix, map[*structType]string{}); err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}

// collectKeys adds a struct's keys, and those of every mapping under it, to
// r.keys: each alone, and under its prefix.
func (r *reference) collectKeys(importPath, name, prefix string) error {
	st, err := r.structNamed(importPath, name)
	if err != nil {
		return err
	}

	return r.collectStruct(st, prefix, map[*structType]bool{})
}

// collectStruct adds st's keys, and those of every mapping under it, to r.keys.
// Like entries, it refuses an embedded field, which would go undocumented.
func (r *reference) collectStruct(st *structType, prefix string, seen map[*structType]bool) error {
	if seen[st] {
		return nil
	}
	seen[st] = true

	keys := yamlKeys(st)
	for _, f := range st.fields {
		if len(f.Names) == 0 {
			return fmt.Errorf("%s embeds a field, which is not documented", st.name)
		}
		key, ok := keys[f.Names[0].Name]
		if !ok {
			continue
		}

		full := key
		if prefix != "" {
			full = prefix + "." + key
		}
		r.keys[key], r.keys[full] = true, true

		_, ref, err := r.describe(st, f.Type)
		if err != nil {
			return err
		}
		if ref != nil {
			if err := r.collectStruct(ref, full, seen); err != nil {
				return err
			}
		}
	}

	return nil
}

// entry is a key of a mapping, ready to write.
type entry struct {
	full, typ, text string

	// mapping is the struct of a key that is a mapping, or a list of them.
	mapping *structType
}

// writeMapping writes a mapping's plain keys, then each of its mappings as a
// section of its own, so that every key is read under its mapping. A struct
// already documented is linked to rather than written again.
func (r *reference) writeMapping(out *bytes.Buffer, st *structType, prefix string, seen map[*structType]string) error {
	entries, err := r.entries(st, prefix)
	if err != nil {
		return err
	}

	seen[st] = prefix

	for _, e := range entries {
		if e.mapping == nil {
			fmt.Fprintf(out, "\n### `%s` {#%s}\n\n*%s*\n\n%s\n", e.full, anchor(e.full), e.typ, e.text)
		}
	}

	for _, e := range entries {
		if e.mapping == nil {
			continue
		}

		fmt.Fprintf(out, "\n## `%s` {#%s}\n\n*%s*\n\n%s\n", e.full, anchor(e.full), e.typ, e.text)

		if other, ok := seen[e.mapping]; ok {
			fmt.Fprintf(out, "\nIts keys are the same as [`%s`](#%s)'s.\n", other, anchor(other))
			continue
		}

		if err := r.writeMapping(out, e.mapping, e.full, seen); err != nil {
			return err
		}
	}

	return nil
}

// entries returns a mapping's keys, in declaration order.
func (r *reference) entries(st *structType, prefix string) ([]entry, error) {
	keys := yamlKeys(st)

	var (
		out      []entry
		previous string
		lastLine int
	)

	for _, f := range st.fields {
		if len(f.Names) == 0 {
			return nil, fmt.Errorf("%s embeds a field, which is not documented", st.name)
		}

		goName := f.Names[0].Name
		key, ok := keys[goName]
		if !ok {
			continue
		}

		full := key
		if prefix != "" {
			full = prefix + "." + key
		}

		typ, ref, err := r.describe(st, f.Type)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", st.name, goName, err)
		}
		if typ == "list of mappings" {
			full += "[]"
		}

		line := r.fset.Position(f.Pos()).Line

		var text string
		switch {
		case f.Doc != nil:
			text = asCode(prose(f.Doc.Text(), keys), r.keys, r.commands)
		case previous != "" && line == lastLine+1:
			// Declared on the line after a documented field, which
			// documents both.
			text = fmt.Sprintf("See `%s`, above.", previous)
		default:
			return nil, fmt.Errorf("%s.%s has no doc comment, so %s would go undocumented", st.name, goName, full)
		}

		out = append(out, entry{full: full, typ: typ, text: text, mapping: ref})
		previous, lastLine = key, r.fset.Position(f.End()).Line
	}

	return out, nil
}

// yamlKeys maps a struct's fields to their YAML keys, leaving out those not
// read from a file.
func yamlKeys(st *structType) map[string]string {
	keys := map[string]string{}

	for _, f := range st.fields {
		if f.Tag == nil || len(f.Names) == 0 || !f.Names[0].IsExported() {
			continue
		}

		tag, _ := strconv.Unquote(f.Tag.Value)
		key, _, _ := strings.Cut(reflect.StructTag(tag).Get("yaml"), ",")
		if key == "" || key == "-" {
			continue
		}

		keys[f.Names[0].Name] = key
	}

	return keys
}

// describe returns a field's type as a configuration writes it, and the struct
// it is a mapping of, if any.
func (r *reference) describe(in *structType, expr ast.Expr) (string, *structType, error) {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return r.describe(in, t.X)

	case *ast.ArrayType:
		elem, ref, err := r.describe(in, t.Elt)
		if err != nil {
			return "", nil, err
		}
		if ref != nil {
			return "list of mappings", ref, nil
		}

		return "list of " + plural(elem), nil, nil

	case *ast.MapType:
		value, _, err := r.describe(in, t.Value)
		if err != nil {
			return "", nil, err
		}

		return "mapping of " + plural(value), nil, nil

	case *ast.Ident:
		if scalar, ok := scalars[t.Name]; ok {
			return scalar, nil, nil
		}

		return r.named(in.pkg.path, t.Name)

	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if !ok {
			break
		}

		importPath, ok := in.imports[pkg.Name]
		if !ok {
			return "", nil, fmt.Errorf("unknown package %s", pkg.Name)
		}

		return r.named(importPath, t.Sel.Name)
	}

	return "", nil, fmt.Errorf("cannot describe a %T", expr)
}

// named describes a named type: a special one, or a struct of this module.
func (r *reference) named(importPath, name string) (string, *structType, error) {
	if special, ok := r.specials[importPath+"."+name]; ok {
		return special, nil, nil
	}

	if !strings.HasPrefix(importPath, module+"/") {
		return "", nil, fmt.Errorf("%s.%s is not a type a configuration can hold", importPath, name)
	}

	st, err := r.structNamed(importPath, name)
	if err != nil {
		return "", nil, err
	}

	return "mapping", st, nil
}

// scalars maps Go's basic types to how a configuration writes them.
var scalars = map[string]string{
	"string":  "string",
	"bool":    "boolean",
	"int":     "integer",
	"int32":   "integer",
	"int64":   "integer",
	"float64": "number",
}

// specials maps named types to how a configuration writes them. A provider
// type's own are in providerTypeDocs.
var specials = map[string]string{
	"time.Duration":                    "duration, such as 30s or 5m",
	typesPackage + ".Size":             "size, such as 512MiB or 4GiB",
	"gopkg.in/yaml.v3.Node":            "mapping, read by the provider",
	typesPackage + ".Placement":        "string: spread or pack",
	typesPackage + ".TimeZone":         "string, an IANA time zone such as Europe/Vilnius",
	typesPackage + ".Weekdays":         "list of days, such as [mon-fri, sun]",
	typesPackage + ".TimeOfDay":        "string, a time of day such as \"08:00\"",
	providerBasePackage + ".Node":      "mapping, read by the provider",
	providerBasePackage + ".OneOrMore": "string, or a list of strings tried in order",
}

// plural returns a type description in the plural.
func plural(s string) string {
	switch {
	case strings.HasSuffix(s, "s"):
		return s
	case strings.Contains(s, ","), strings.Contains(s, ":"):
		return s
	default:
		return s + "s"
	}
}

// prose turns a doc comment into the reference's text, with the struct's Go
// field names written as their keys. The comment's first word is always the
// field's name; elsewhere only compound names such as TokenPath are replaced,
// as a single word such as Token may be meant as the word.
func prose(comment string, keys map[string]string) string {
	if first, rest, ok := strings.Cut(comment, " "); ok {
		if key, ok := keys[first]; ok {
			comment = "`" + key + "` " + rest
		}
	}

	for goName, key := range keys {
		// GitHub looks compound, but is a name, meant as the word wherever
		// a comment uses it, never as the key.
		if !compound.MatchString(goName) || goName == "GitHub" {
			continue
		}
		comment = regexp.MustCompile(`\b`+regexp.QuoteMeta(goName)+`\b`).
			ReplaceAllString(comment, "`"+key+"`")
	}

	var paragraphs []string
	for para := range strings.SplitSeq(strings.TrimSpace(comment), "\n\n") {
		if isCode(para) {
			paragraphs = append(paragraphs, "```yaml\n"+dedent(para)+"\n```")
			continue
		}
		paragraphs = append(paragraphs, strings.Join(strings.Fields(para), " "))
	}

	return strings.Join(paragraphs, "\n\n")
}

// compound matches a name made of several words.
var compound = regexp.MustCompile(`^[A-Z][a-z0-9]+[A-Z]`)

// isCode reports whether a paragraph is an indented example.
func isCode(para string) bool {
	for line := range strings.SplitSeq(para, "\n") {
		if line != "" && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, "  ") {
			return false
		}
	}

	return true
}

// dedent removes an example's indentation.
func dedent(para string) string {
	lines := strings.Split(para, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "\t")
	}

	return strings.Join(lines, "\n")
}

// anchor returns a key's anchor on the page.
func anchor(key string) string {
	return strings.NewReplacer(".", "-", "[]", "", "_", "-").Replace(key)
}

// codeWords matches what the reference writes as code wherever the comments
// it is generated from write it plainly: a command, a flag, an absolute path,
// an environment variable, or a configuration key of more than one word.
var codeWords = regexp.MustCompile(
	`\brungar(?: [a-z][a-z-]*){0,2}\b` +
		`|(?:^|[\s(])--[a-z][a-z-]*` +
		`|(?:^|[\s(])/(?:etc|run|var|usr|home|tmp|opt)/[\w./@:-]*[\w/]` +
		`|\$?\b[A-Z][A-Z0-9]*_[A-Z0-9_]+\b` +
		`|\b[a-z][a-z0-9]*(?:[._][a-z0-9]+)+\b`)

// asCode writes as code the words codeWords matches in text, outside code
// already. A key is written so only if it is one of keys, and a command only
// if it is one of commands, as the patterns match ordinary words too.
func asCode(text string, keys, commands map[string]bool) string {
	parts := strings.Split(text, "`")
	for i := 0; i < len(parts); i += 2 {
		parts[i] = codeWords.ReplaceAllStringFunc(parts[i], func(m string) string {
			lead := ""
			if m[0] == ' ' || m[0] == '\t' || m[0] == '\n' || m[0] == '(' {
				lead, m = m[:1], m[1:]
			}

			switch {
			case strings.HasPrefix(m, "rungar"):
				// The longest command the words name.
				for w := strings.Fields(m); len(w) > 0; w = w[:len(w)-1] {
					if name := strings.Join(w, " "); commands[name] {
						return lead + "`" + name + "`" + strings.TrimPrefix(m, name)
					}
				}
				return lead + m
			case strings.HasPrefix(m, "--"), strings.HasPrefix(m, "/"):
				return lead + "`" + m + "`"
			case strings.ToUpper(m) == m:
				return lead + "`" + m + "`"
			case keys[m]:
				return lead + "`" + m + "`"
			}

			return lead + m
		})
	}

	return strings.Join(parts, "`")
}
