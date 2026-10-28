// Package apiclient renders the half of a generated client that the
// generator does not write: retries that skip non-idempotent methods,
// a circuit breaker, Retry-After honoured over local backoff, a
// per-attempt timeout, and the identifying headers.
//
// ogen and hey-api write the typed operations; these templates make
// them safe to call, the same way in both languages.
package apiclient

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
)

//go:embed *.tmpl
var templates embed.FS

// File is one file written beside the generated code.
type File struct {
	Path string
	Body string
}

// Params is everything the client templates need, and nothing else.
type Params struct {
	// Name is the API this client talks to, for its doc comments.
	Name string

	// Version is what the client reports in Client-Version. For a
	// service we own it is its spec's info.version; for a third-party
	// client it is the version of THEIR API we generated against.
	Version string

	// Package is the Go package name. Empty for TypeScript.
	Package string

	// ImportPath is how a consumer imports this client, as it would
	// appear in their source: an npm package name for a published
	// client, a relative path for one that ships inside the repo that
	// generated it.
	ImportPath string

	// HasSecurity says the spec declares securitySchemes, which makes
	// ogen's NewClient take a SecuritySource before its options. The
	// generated constructor has to match that signature.
	HasSecurity bool

	// Published says this client ships as an npm package owning its
	// own tsconfig, which decides two things.
	//
	// Client-Version comes from package.json when published, so it
	// cannot disagree with the version a consumer installed, and from
	// the spec's info.version when not.
	//
	// Imports carry '.ts' when published, because that tsconfig
	// rewrites them into dist/. Unpublished clients are compiled by a
	// tsconfig they do not own, where '.ts' is TS5097, so they are
	// extensionless.
	Published bool
}

func render(name string, p Params) (string, error) {
	b, err := templates.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("apiclient: %w", err)
	}
	t, err := template.New(name).Funcs(template.FuncMap{
		"header":      Header,
		"parityCases": func() []string { return parityCases },
	}).Parse(string(b))
	if err != nil {
		return "", fmt.Errorf("apiclient: parsing %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, p); err != nil {
		return "", fmt.Errorf("apiclient: rendering %s: %w", name, err)
	}
	return buf.String(), nil
}

// templateFile is a template and where its output goes.
type templateFile struct{ tmpl, path string }

// These lists are the one inventory of what each client is made of.
// A consumer decides WHERE they go; what they ARE is decided here, so
// adding a file does not mean remembering a second list.
var tsFiles = []templateFile{
	{"ts_resilience.ts.tmpl", "resilience.ts"},
	{"ts_index.ts.tmpl", "index.ts"},
}

var goFiles = []templateFile{
	{"go_client.go.tmpl", "client.go"},
	{"go_paging.go.tmpl", "paging.go"},
}

// tsTestFiles and goTestFiles are rendered by THIS repo's tests and
// not shipped.
//
// They test this template, not the consumer's service. The Go ones
// used to ship, on the reasoning that a consumer could run them -
// which answered whether they could, never whether they should. They
// are transport-level to the last line: the client template's only
// substitution is the package clause, so every consumer was handed
// a byte-identical copy of tests that never touch their API, and paid
// 14s of -race on every CI run to re-prove what one render here proves
// once. A failure landed in a file that correctly says "do not edit",
// where the person reading it could do nothing about it.
//
// The TypeScript ones could not ship even on that reasoning: they are
// written for `node --test`, and the repo consuming this client runs
// vitest, whose default include is every *.test.ts - so shipping them
// would hand vitest a suite it cannot run, importing node:test from a
// project with no @types/node. Rewriting them for vitest would tie
// this generator to one consumer's choice of runner, and the next
// consumer's choice would be a second copy.
var tsTestFiles = []templateFile{
	{"ts_resilience_test.ts.tmpl", "resilience.test.ts"},
}

var goTestFiles = []templateFile{
	{"go_client_test.go.tmpl", "client_test.go"},
	{"go_paging_test.go.tmpl", "paging_test.go"},
}

// TypeScript returns the files that sit beside a generated TypeScript
// client.
func TypeScript(p Params) ([]File, error) { return files(tsFiles, p) }

// TypeScriptTests returns the TypeScript client's own tests; see
// tsTestFiles for why they do not ship.
func TypeScriptTests(p Params) ([]File, error) { return files(tsTestFiles, p) }

// Go returns the files that sit beside a generated Go client.
func Go(p Params) ([]File, error) { return files(goFiles, p) }

// GoTests returns the Go client's own tests; see goTestFiles for why
// they do not ship.
func GoTests(p Params) ([]File, error) { return files(goTestFiles, p) }

func files(set []templateFile, p Params) ([]File, error) {
	out := make([]File, 0, len(set))
	for _, f := range set {
		body, err := render(f.tmpl, p)
		if err != nil {
			return nil, err
		}
		out = append(out, File{Path: f.path, Body: body})
	}
	return out, nil
}

// parityCases are the Retry-After headers every generated client must
// answer the same way. Rendered into each language's test suite from
// here, so the list is one thing rather than one per language.
//
// No case carries an expected answer. Each suite reports what its own
// client said and the comparison looks for disagreement, so two
// clients that differ fail even on an input nobody predicted - which a
// table of expected answers cannot do. Agreement is all this shows:
// whether an answer is correct is each language's own suite's question.
//
// The list is every header that has actually diverged, plus the
// neighbours of each.
var parityCases = []string{
	"2", "30", "31", "3600", "0",
	"+5", "-0", "-5", "-1",
	"1.5", "0.5", "0x10", "1e3", "5s", " 5 .5", "soon-ish",
	"9223372037", "9223372036854775", "9223372036854775807",
	"9223372036854775808", "+9223372036854775808",
	"18446744073709551616", "99999999999999999999999999",
	"Wed, 21 Oct 2115 07:28:00 GMT",
	"Wednesday, 21-Oct-15 07:28:00 GMT",
	"Wed Oct 21 07:28:00 2115",
	"Wed Oct  1 07:28:00 2115",
	"Xyz, 21 Oct 2115 07:28:00 GMT",
	"Wed, 21 Zzz 2115 07:28:00 GMT",
	"Wed, 21 Oct 2115 24:00:00 GMT",
	"Funday, 06-Nov-26 08:49:37 GMT",
}

// ParityCases is a copy of that list, for a comparison that needs to
// know a client answered every case rather than merely as many as its
// peers.
func ParityCases() []string { return slices.Clone(parityCases) }

// retired are files this package used to write and no longer does.
// Dropping one from the lists above is not enough: the copy on disk
// stays, owned by nobody, still telling its reader not to edit it.
//
// An entry leaves this list once no supported version still writes
// the file. A file no consumer ever received does not need one.
var retired = []string{"client_test.go", "paging_test.go"}

// Retired is a copy of that list. A copy, and unexported underneath,
// because an importer reassigning it would change which files
// RemoveRetired deletes from somebody else's repo.
func Retired() []string { return slices.Clone(retired) }

// WroteIt reports whether this package wrote body, by looking for any
// of knownMarkers on the first line, or the second behind an eslint
// pragma.
//
// Position, not just presence: a hand-written replacement may well
// mention the file it replaced, and searching the whole body would
// read that as ownership and delete someone's work.
func WroteIt(body []byte) bool {
	for i, line := range bytes.SplitN(body, []byte("\n"), 3) {
		if i >= 2 {
			break
		}
		for _, m := range knownMarkers {
			if bytes.Contains(line, []byte(m)) {
				return true
			}
		}
	}
	return false
}

// RemoveRetired deletes the Retired files under dir that WroteIt
// claims, and returns their names so a caller can report them. A file
// that was never there is not an error.
func RemoveRetired(dir string) ([]string, error) {
	var removed []string
	for _, name := range retired {
		path := filepath.Join(dir, name)
		body, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return removed, fmt.Errorf("reading %s: %w", path, err)
		}
		if !WroteIt(body) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return removed, fmt.Errorf("removing %s: %w", path, err)
		}
		removed = append(removed, name)
	}
	return removed, nil
}

// generatedMarker is the phrase this tool writes into every file it
// generates. It names the tool that actually did it, so a reader who
// goes looking finds the right repo.
const generatedMarker = "Generated by oapi-clientgen"

// knownMarkers are every phrase this tool has ever written, newest
// first. WroteIt matches any of them.
//
// Writing and recognising are different jobs. What we write has to be
// true today; what we recognise has to cover every file still on
// disk, including ones generated under an old name. Tying those
// together would mean either lying in new files or orphaning old
// ones - a file WroteIt stops claiming can never be retired, in a
// repo we do not control.
//
// An entry leaves this list when no file carrying it can still be in
// anyone's tree. "Generated by homelabctl" is from before this tool
// was extracted from homelabctl; roughly 40 such files are committed
// in pokemon and app today.
var knownMarkers = []string{
	generatedMarker,
	"Generated by homelabctl",
}

// generatedNotice is the header minus the comment syntax, one line
// per line of output. A const rather than a slice, because a
// package-level slice would be mutable shared state.
const generatedNotice = generatedMarker + ". Do not edit:\n" +
	"`oapi-clientgen generate` rewrites this file, so edits are lost\n" +
	"silently. Change the template in oapi-clientgen instead."

// Header returns the notice as line comments, each prefixed with
// comment and terminated by a newline. No blank line after: a
// template that wants one writes it.
//
// Templates call this as {{ "{{ header `//` }}" }} so the wording has
// one home.
func Header(comment string) string {
	var b strings.Builder
	for _, line := range strings.Split(generatedNotice, "\n") {
		fmt.Fprintf(&b, "%s %s\n", comment, line)
	}
	return b.String()
}

// GeneratedHeader is what MarkGenerated prepends to hey-api's own
// output - the same notice as Header, behind an eslint pragma.
//
// Lint has to be told to skip generated code, and the repos consuming
// these clients tell it inline rather than through .eslintignore -
// because naming an ignored file on an eslint command line makes it
// warn "File ignored", and a CI step that lints CHANGED files does
// exactly that the first time generated code changes. An ignore entry
// turns a passing build red; a header in the file does not.
func GeneratedHeader() string { return "/* eslint-disable */\n" + Header("//") }

// MarkGenerated prepends GeneratedHeader to every file under dir that
// does not already carry it.
func MarkGenerated(dir string) error {
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch filepath.Ext(path) {
		case ".ts", ".tsx", ".js", ".mjs":
		default:
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.HasPrefix(body, []byte("/* eslint-disable */")) {
			return nil
		}
		return os.WriteFile(path, append([]byte(GeneratedHeader()), body...), 0o644)
	})
}
