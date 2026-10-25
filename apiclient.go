// Package apiclient owns what a generated API client is made of,
// beyond the code the generator itself emits.
//
// A generated client is two halves. The generator - ogen for Go,
// hey-api for TypeScript - writes the typed operations from the spec.
// Everything that makes those operations safe to call is written here:
// retries that skip non-idempotent methods, a circuit breaker, the
// server's own Retry-After honoured over local backoff, a per-attempt
// timeout, and the headers that let a server see which client is
// calling it.
//
// It lives apart from internal/runtime because it is not a property of
// a language runtime. The templates were under templates/go-service
// only because the first thing to emit a TypeScript client happened to
// be a Go service - and a client generated for a THIRD-PARTY spec, the
// second consumer, has no runtime at all. Reaching into go-service for
// them shipped a client whose own usage example named an npm package
// that did not exist.
package apiclient

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
//
// Deliberately not runtime.Params: that struct carries ports, images
// and deploy settings a client knows nothing about, and passing it
// here is what let Owner go unset and render an empty npm scope into
// every third-party client.
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
	// ogen's NewClient take a SecuritySource before its options. Our
	// own constructor has to match that signature or it will not
	// compile for such a spec - and pokedex has none while Zoom has
	// one, so both shapes are real.
	HasSecurity bool

	// Published says this client ships as an npm package of its own,
	// with its own tsconfig.
	//
	// Owning that tsconfig is what decides both things this field
	// controls, which is why one field is enough for two decisions.
	//
	// Client-Version: a published client reads it from package.json,
	// so the header cannot disagree with the version a consumer
	// installed. An unpublished one has no package.json and writes in
	// the spec's info.version - the version of THEIR API, which is
	// what a server reading the header wants to know.
	//
	// Import extensions: a published client writes '.ts', because its
	// own tsconfig sets allowImportingTsExtensions and
	// rewriteRelativeImportExtensions, so tsc rewrites them to '.js'
	// on the way into dist/. An unpublished client is compiled by a
	// tsconfig it does not own - it lives as source inside the
	// consuming repo - and '.ts' there is TS5097 unless that repo
	// also enables allowImportingTsExtensions and then either noEmit
	// or the rewrite. Extensionless asks nothing of the consumer and
	// typechecks under Bundler, node and nodenext alike.
	//
	// '.js' is not the answer for the unpublished case, though it is
	// the usual ESM advice: the file on disk is .ts, so bare Node
	// fails with ERR_MODULE_NOT_FOUND either way. Running these
	// sources directly needs compiled output, which is what being
	// published is for.
	//
	// That is also why a consumer running `node server.ts` rather
	// than bundling is already served: it depends on the PUBLISHED
	// client, resolves main to dist/, and gets JavaScript. Only a
	// client generated into the consuming repo is unpublished, and
	// that repo compiles it as part of its own build.
	Published bool
}

func render(name string, p Params) (string, error) {
	b, err := templates.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("apiclient: %w", err)
	}
	t, err := template.New(name).Funcs(template.FuncMap{"header": Header}).Parse(string(b))
	if err != nil {
		return "", fmt.Errorf("apiclient: parsing %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, p); err != nil {
		return "", fmt.Errorf("apiclient: rendering %s: %w", name, err)
	}
	return buf.String(), nil
}

// These lists are the one inventory of what each client is made of. A consumer decides WHERE they go; what they ARE is decided
// here, so adding a file does not mean remembering a second list.
var tsFiles = []struct{ tmpl, path string }{
	{"ts_resilience.ts.tmpl", "resilience.ts"},
	{"ts_index.ts.tmpl", "index.ts"},
}

var goFiles = []struct{ tmpl, path string }{
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
var tsTestFiles = []struct{ tmpl, path string }{
	{"ts_resilience_test.ts.tmpl", "resilience.test.ts"},
}

var goTestFiles = []struct{ tmpl, path string }{
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

func files(set []struct{ tmpl, path string }, p Params) ([]File, error) {
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

// Retired are files this package used to write and no longer does,
// relative to the directory a client's support files live in.
//
// Dropping a file from the lists above is not enough on its own. The
// copy already on disk stays there, in every repo that ever ran this,
// now owned by nobody - drifting from a template that keeps moving
// while still telling its reader not to edit it. Whoever writes these
// files has to remove those too, which is what RemoveRetired is for.
//
// Only ever paths this package itself wrote, matched exactly.
//
// resilience.test.ts is deliberately absent. It was added to tsFiles
// and taken back out before either was released, so no consumer ever
// received one - an entry for it would be a deletion that can never
// fire. An entry leaves this list the same way: once no supported
// version of this tool still writes the file.
var Retired = []string{"client_test.go", "paging_test.go"}

// WroteIt reports whether this package wrote body, by looking for the
// marker on the FIRST line, or the second behind an eslint pragma.
// Every template puts it in one of those two places.
//
// Position matters, not just presence. A hand-written replacement for
// a generated test is very likely to MENTION the thing it replaced -
// "replaces the one generated by homelabctl, which was wrong" - and
// searching anywhere else in the file reads that as a claim of
// ownership and deletes someone's work. A byte window does not help:
// in a short file the prose is inside it.
func WroteIt(body []byte) bool {
	for i, line := range bytes.SplitN(body, []byte("\n"), 3) {
		if i >= 2 {
			break
		}
		if bytes.Contains(line, []byte(generatedMarker)) {
			return true
		}
	}
	return false
}

// RemoveRetired deletes the Retired files under dir and returns the
// ones it removed, so a caller can say so. Files that were never there
// are not an error: a service scaffolded after they were dropped never
// had them.
//
// It removes a file only if WroteIt claims it.
func RemoveRetired(dir string) ([]string, error) {
	var removed []string
	for _, name := range Retired {
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

// GeneratedHeader is prepended to every file a generator writes.
//
// Lint has to be told to skip generated code, and the repos consuming
// these clients tell it inline rather than through .eslintignore -
// because naming an ignored file on an eslint command line makes it
// warn "File ignored", and a CI step that lints CHANGED files does
// exactly that the first time generated code changes. An ignore entry
// turns a passing build red; a header in the file does not.
// generatedMarker is the phrase that says a file came from here.
//
// Deliberately just the phrase, not the sentence that follows it. The
// files WroteIt has to claim are the ones ALREADY on disk in consumer
// repos, written by older versions of this tool - and the rest of the
// notice is free to be reworded, which it has been. Matching only the
// current wording would silently stop recognising every file written
// before the change.
//
// Everything that writes a header writes this, through Header below.
// It used to be eight hand-maintained copies, one per template plus
// GeneratedHeader, in two wordings - which is how RemoveRetired came
// to match a sentence ending in "." while every template ended it in
// ":" and removed nothing at all.
const generatedMarker = "Generated by homelabctl"

// generatedNotice is the whole header, minus the comment syntax, with
// one line per line of output.
//
// A const rather than a []string: a package-level slice is mutable
// shared state - any code here could rewrite an element and change
// what every generated file claims - and this is a fixed fact, not a
// configurable one.
//
// It names BOTH commands. A reader who finds one of these files has
// to know what will overwrite their edit, and `client generate` and
// `homelabctl regen` rewrite different subsets of them.
const generatedNotice = generatedMarker + ". Do not edit: `homelabctl regen` and\n" +
	"`client generate` rewrite this file, so edits are lost silently.\n" +
	"Change the template in homelabctl instead."

// Header returns the generated-file header as line comments, each
// prefixed with lead, ending in a blank line.
//
// Templates call this as {{ "{{ header `//` }}" }} rather than
// spelling the notice out, so the wording has one home. lead is the
// language's line-comment token, because that is the only part that
// differs between a .go and a .ts file.
func Header(lead string) string {
	var b strings.Builder
	for _, line := range strings.Split(generatedNotice, "\n") {
		b.WriteString(lead)
		b.WriteString(" ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

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
