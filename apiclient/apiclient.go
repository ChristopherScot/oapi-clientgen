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
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
	"unicode"
)

//go:embed *.tmpl
var templates embed.FS

// File is one file written beside the generated code.
type File struct {
	// Path is a bare filename, resolved against the output
	// directory by whoever writes it. RemoveRetired compares it
	// against retired names the same way, so a path with a
	// directory in it would stop matching and a file being
	// written could be retired out from under the caller.
	Path string
	Body string
}

// PagedOp is one operation the spec says pages, resolved against the
// document that declared it.
//
// The names arrive resolved rather than assumed. after/nextCursor is
// the convention and most operations say nothing, but an API that
// chose page_token/next_page_token is ordinary and the generated
// wrapper has to use its words, not ours.
type PagedOp struct {
	// OperationID is what the spec calls it, which decides what the
	// generated wrapper is called.
	OperationID string

	// CursorParam is the query parameter carrying the cursor. Both
	// templates put it in the query string; a spec may say
	// in: requestBody, and this generator does not render that.
	CursorParam string

	// NextPath locates the next cursor in the response and ItemsPath
	// the array holding the page, both as paths rather than names: an
	// API answering {meta: {nextCursor}} is ordinary, and a name
	// could not say so.
	NextPath  string
	ItemsPath string

	// ItemType is what one element is called in the generated code,
	// resolved from the array's schema.
	ItemType string
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

	// Paged are the operations the spec says page, in operationId
	// order. Empty for a spec where none do, which is most of them.
	//
	// The only per-OPERATION data here: everything else describes the
	// client as a whole, which is why the templates could ignore the
	// document until now.
	Paged []PagedOp

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
	return renderWith(name, p, defaults)
}

// renderWith is render with the shared numbers supplied, so a test can
// vary them and see whether the templates actually read them.
func renderWith(name string, p Params, d SharedDefaults) (string, error) {
	b, err := templates.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("apiclient: %w", err)
	}
	t, err := template.New(name).Funcs(template.FuncMap{
		"header":      Header,
		"parityCases": func() []string { return parityCases },
		"defaults":    func() SharedDefaults { return d },
		"verdicts":    func() VerdictWords { return verdictVocabulary },
		"goName":      goName,
		"goPath":      goPath,
		"tsPath":      tsPath,
		"goDuration":  goDuration,
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

// goName is an operationId as ogen exports it: listPokemon becomes
// ListPokemon. ogen only upper-cases the first rune, so neither does
// this - a divergence would name a method that is not there.
func goName(operationID string) string {
	if operationID == "" {
		return ""
	}
	r := []rune(operationID)
	return string(unicode.ToUpper(r[0])) + string(r[1:])
}

// goDuration writes a count of milliseconds as the time.Duration
// expression a Go author would have written: 30000 becomes
// 30 * time.Second, 100 stays 100 * time.Millisecond.
//
// The shared defaults are in milliseconds because that is the unit
// TypeScript has. Rendering that number straight into Go multiplied by
// time.Millisecond is arithmetically right and reads as a mistake -
// 10000 * time.Millisecond gives no sense of ten seconds.
func goDuration(ms int) string {
	for _, u := range []struct {
		name string
		per  int
	}{
		{"time.Hour", 3600000},
		{"time.Minute", 60000},
		{"time.Second", 1000},
	} {
		if ms >= u.per && ms%u.per == 0 {
			return fmt.Sprintf("%d * %s", ms/u.per, u.name)
		}
	}
	return fmt.Sprintf("%d * time.Millisecond", ms)
}

// goPath turns a JSON Pointer into the Go field accesses that reach
// the same value: /meta/nextCursor becomes .Meta.NextCursor, because
// ogen exports every field.
func goPath(pointer string) string { return fieldPath(pointer, goName) }

// tsPath is the same for TypeScript, where hey-api keeps the spec's
// own spelling: /meta/nextCursor stays .meta.nextCursor.
func tsPath(pointer string) string {
	return fieldPath(pointer, func(s string) string { return s })
}

func fieldPath(pointer string, name func(string) string) string {
	var b strings.Builder
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		if part == "" {
			continue
		}
		b.WriteString("." + name(part))
	}
	return b.String()
}

// templateFile is a template and where its output goes.
type templateFile struct {
	tmpl, path string

	// when decides whether the file is written at all. Nil means
	// always, which is every file but the paging wrappers: those
	// exist only for the operations a spec says page, and a spec
	// where none do should get no file rather than an empty one
	// nobody can account for.
	when func(Params) bool
}

// anyPaged reports whether the spec marked anything.
func anyPaged(p Params) bool { return len(p.Paged) > 0 }

// These lists are the one inventory of what each client is made of.
// A consumer decides WHERE they go; what they ARE is decided here, so
// adding a file does not mean remembering a second list.
var tsFiles = []templateFile{
	{tmpl: "ts_resilience.ts.tmpl", path: "resilience.ts"},
	{tmpl: "ts_paging.ts.tmpl", path: "paging.ts"},
	{tmpl: "ts_paging_ops.ts.tmpl", path: "paging_ops.ts", when: anyPaged},
	{tmpl: "ts_index.ts.tmpl", path: "index.ts"},
}

var goFiles = []templateFile{
	{tmpl: "go_client.go.tmpl", path: "client.go"},
	{tmpl: "go_paging.go.tmpl", path: "paging.go"},
	{tmpl: "go_paging_ops.go.tmpl", path: "paging_ops.go", when: anyPaged},
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
	{tmpl: "ts_resilience_test.ts.tmpl", path: "resilience.test.ts"},
	{tmpl: "ts_paging_test.ts.tmpl", path: "paging.test.ts"},
}

var goTestFiles = []templateFile{
	{tmpl: "go_client_test.go.tmpl", path: "client_test.go"},
	{tmpl: "go_paging_test.go.tmpl", path: "paging_test.go"},
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
		if f.when != nil && !f.when(p) {
			continue
		}
		body, err := render(f.tmpl, p)
		if err != nil {
			return nil, err
		}
		// Go output goes through gofmt. The templates are kept
		// gofmt-clean as written and a test holds them that way, so
		// this changes nothing today - it is here because one
		// template cannot stay that way by hand.
		//
		// gofmt spaces a binary expression by how deeply it is
		// nested: 30 * time.Second stays spaced on its own and is
		// compressed to 30*time.Second inside a call that has other
		// arguments. goDuration feeds seven call sites and cannot
		// pick a spelling right at all of them, so the eighth one
		// lands here rather than in a consumer's repo, where the file
		// says "do not edit" and the fix is back in this repo anyway.
		//
		// Formatting only: format.Source does not add or remove
		// imports, and accepts a fragment with no package clause
		// without complaint. generated_test.go compiles the result,
		// which is what catches those.
		//
		// TypeScript has no equivalent pass - there is no formatter
		// in the standard library to call - so its templates must be
		// formatter-clean as written. See
		// TestNoTypeScriptParameterListEndsInAComma.
		if strings.HasSuffix(f.path, ".go") {
			formatted, err := format.Source([]byte(body))
			if err != nil {
				return nil, fmt.Errorf("apiclient: rendering %s: output is not valid Go: %w", f.tmpl, err)
			}
			body = string(formatted)
		}
		out = append(out, File{Path: f.path, Body: body})
	}
	return out, nil
}

// verdicts are the words a recorder prints for what its client made of
// a Retry-After header, and the comparison reads back.
//
// A wire format: the recorders print these and the comparison reads
// them back, so a difference in spelling reads as every case of that
// verdict diverging rather than as the typo it is.
type VerdictWords struct {
	Stop, Fallback string
	// GoWait and TSWait are the same sentence in each language's own
	// formatting: one Printf verb, one template literal. Kept beside
	// each other so a change to the shape has to touch both.
	GoWait, TSWait string
}

var verdictVocabulary = VerdictWords{
	Stop:     "STOP",
	Fallback: "FALLBACK",
	GoWait:   "WAIT %dms",
	TSWait:   "WAIT ${ms}ms",
}

// defaults are the numbers both languages are specified to share.
//
// Rendered into each template from here, so a change lands in both or
// in neither. Nothing compares 5s against 5000, so a timeout that
// moved in one language only would be a difference no test could see.
//
// Durations are milliseconds because that is the unit TypeScript has;
// goDuration renders them back into the time.Duration expression a Go
// author would have written.
type SharedDefaults struct {
	TimeoutMs       int
	SingleBackoffMs int
	ExpSeedMs       int
	ExpAttempts     int
	// ExpJitterPct is how far either side of a backoff the jitter may
	// move it, so a fleet does not retry in lockstep. A percentage
	// rather than a fraction because the templates render it into two
	// languages and 25 survives both.
	ExpJitterPct      int
	MaxRetryAfterMs   int
	BreakerWindowMs   int
	BreakerMinReqs    int
	BreakerFailPct    int
	BreakerCooldownMs int
}

var defaults = SharedDefaults{
	TimeoutMs:         5000,
	SingleBackoffMs:   1000,
	ExpSeedMs:         100,
	ExpAttempts:       5,
	ExpJitterPct:      25,
	MaxRetryAfterMs:   30000,
	BreakerWindowMs:   10000,
	BreakerMinReqs:    20,
	BreakerFailPct:    50,
	BreakerCooldownMs: 30000,
}

// parityCases are the Retry-After headers every generated client must
// answer the same way. Rendered into each language's test suite from
// here, so the list is one thing rather than one per language.
//
// No case carries an expected answer. Each suite reports what its own
// client said and the comparison looks for disagreement, so two
// clients that differ fail even on an input nobody predicted - which a
// table of expected answers cannot do.
//
// Agreement is all this shows. Where a case is also asserted in a
// language's own suite that suite says what the right answer is; the
// date cases here are not, so a date both clients read the same wrong
// way passes. Each suite asserts date handling on a timestamp it
// generates, which is the behaviour but not these headers.
//
// The list is every header that has actually diverged, plus the
// neighbours of each.
//
// A date case belongs in the past or far enough ahead to exceed the
// cap, never inside it: a wait is deadline-minus-now, so a date a few
// seconds out gives each client its own number and they disagree over
// nothing. Past dates clamp to 0ms in every language, and distant
// ones stop instead of waiting, which is what lets the answers be
// compared exactly. TestNoDateCaseFallsInsideTheCap enforces it.
//
// What this does not do is remove the duplication. Each language
// parses Retry-After with what its own runtime offers, so the rule
// stays implemented twice and a change to it means editing both
// templates; one list of inputs catches the two drifting apart rather
// than keeping them together.
//
// Retry-After is the only rule compared this way. Each template
// writes out its own retryable and unwell boundaries, and no test
// compares those across languages; 501 and 505 are the exception,
// asserted in both suites.
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

// RemoveRetired deletes the retired files under dir that the caller
// is not writing this run, and returns their names so it can report
// them. A file that was never there is not an error.
//
// writing is what the caller just rendered. A name in retired that is
// also being written is not retired at all - go_client_test.go.tmpl
// still exists and this repo's own tests render it, so the name alone
// cannot decide.
//
// Keyed on that rather than on which phrases this tool has ever
// written. Recognising its own output meant a list of every past
// marker, whose entries could only leave once no file carrying one
// was in anyone else's repo - a rule you had to reason about two
// repos away to edit a const.
//
// A file is still only deleted if it carries the header, because the
// alternative is deleting a consumer's own client_test.go. That check
// is a prefix on the shared Header rather than a search for a marker
// anywhere in the body: a hand-written replacement that MENTIONS what
// it replaced is the file a consumer is most likely to write, and
// searching the whole body reads that as a claim of ownership.
func RemoveRetired(dir string, writing []File) ([]string, error) {
	current := make(map[string]bool, len(writing))
	for _, f := range writing {
		current[f.Path] = true
	}

	var removed []string
	for _, name := range retired {
		if current[name] {
			continue
		}
		path := filepath.Join(dir, name)
		body, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return removed, fmt.Errorf("reading %s: %w", path, err)
		}
		if !ours(body) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return removed, fmt.Errorf("removing %s: %w", path, err)
		}
		removed = append(removed, name)
	}
	return removed, nil
}

// ours reports whether body opens with a header this tool writes
// today.
//
// A prefix, not a search: the position is what separates "this file
// is ours" from "this file talks about us". Both comment syntaxes,
// and the eslint pragma TypeScript carries ahead of it.
//
// Headers earlier versions wrote are not claimed, so a file still
// carrying one is never retired. Regenerating rewrites the header and
// makes the file claimable again, which is what every consumer has
// already done.
func ours(body []byte) bool {
	for _, prefix := range []string{
		Header("//"),
		"/* eslint-disable */\n" + Header("//"),
	} {
		if bytes.HasPrefix(body, []byte(prefix)) {
			return true
		}
	}
	return false
}

// generatedMarker is the phrase this tool writes into every file it
// generates. It names the tool that actually did it, so a reader who
// goes looking finds the right repo.
//
// Rewording it changes what ours claims: the rendered Header opens
// with this phrase, and ours matches that whole Header as a prefix.
// Files already on disk carry the old wording and stop being
// retirable until they are regenerated.
const generatedMarker = "Generated by oapi-gen"

// generatedNotice is the header minus the comment syntax, one line
// per line of output. A const rather than a slice, because a
// package-level slice would be mutable shared state.
const generatedNotice = generatedMarker + ". Do not edit:\n" +
	"`oapi-gen generate` rewrites this file, so edits are lost\n" +
	"silently. Change the template in oapi-gen instead."

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
