package apiclient

import (
	"bytes"
	"fmt"
	"go/format"
	"strings"
	"testing"
)

// Published decides how the client's own files import each other, and
// every one of them has to make the same choice: a package that
// mixed '.ts' and extensionless imports would compile in one place
// and not the other.
//
// A vendored client is the strict case. It is compiled by a tsconfig
// it does not own, where a '.ts' import is TS5097 unless the
// consuming repo turns on allowImportingTsExtensions - which is not
// ours to ask for.
func TestVendoredClientImportsWithoutExtensions(t *testing.T) {
	p := Params{
		Name: "zoom-users", Version: "2.0.0", ImportPath: "./users",
		Published: false,
	}
	files, err := TypeScript(p)
	if err != nil {
		t.Fatal(err)
	}
	// The test templates import the client the same way and break the
	// same way. Rendering them published, which is all the node harness
	// does, never reaches the unpublished form.
	tests, err := TypeScriptTests(p)
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, tests...)
	if len(files) == 0 {
		t.Fatal("no files rendered")
	}

	for _, f := range files {
		for _, line := range strings.Split(f.Body, "\n") {
			if !strings.Contains(line, "from './") && !strings.Contains(line, "import pkg") {
				continue
			}
			if strings.Contains(line, ".ts'") {
				t.Errorf("%s imports with a .ts extension; TS5097 in the consuming repo:\n  %s",
					f.Path, strings.TrimSpace(line))
			}
		}
	}
}

// The published case is the mirror: its own tsconfig sets
// allowImportingTsExtensions and rewrites them into dist/, so the
// extension has to be there or the emitted JS points at nothing.
func TestPublishedClientImportsWithExtensions(t *testing.T) {
	files, err := TypeScript(Params{
		Name: "pokedex", Version: "0.8.1", ImportPath: "@o/pokedex-client",
		Published: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var sawIndex bool
	for _, f := range files {
		if !strings.HasSuffix(f.Path, "index.ts") {
			continue
		}
		sawIndex = true
		if !strings.Contains(f.Body, "./resilience.ts") {
			t.Errorf("%s does not import ./resilience.ts; tsc would emit a dangling path:\n%s",
				f.Path, f.Body)
		}
	}
	if !sawIndex {
		t.Fatal("no index.ts rendered")
	}
}

// A consumer that cannot build the client - because its call sites
// rely on the generated module-level one - still has to be able to
// get the retries. setConfig takes a fetch, so the transport has to
// be reachable without createClient.
func TestResilientFetchIsReachableWithoutCreateClient(t *testing.T) {
	files, err := TypeScript(Params{
		Name: "zoom-users", Version: "2.0.0", ImportPath: "./users",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Exact paths, not suffixes: "resilience.test.ts" ends in
	// "resilience.ts" too, and a suffix match picked whichever came
	// first in tsFiles.
	var resilience, index string
	for _, f := range files {
		switch f.Path {
		case "resilience.ts":
			resilience = f.Body
		case "index.ts":
			index = f.Body
		}
	}
	// Both named, because an empty one means the loop matched nothing
	// and every assertion below would pass vacuously. That is how a
	// suffix match on "resilience.ts" - which "resilience.test.ts"
	// also satisfies - went unnoticed: it depended on the order of
	// tsFiles, not on the paths being right.
	if resilience == "" {
		t.Fatal("no resilience.ts rendered")
	}
	if index == "" {
		t.Fatal("no index.ts rendered")
	}

	if !strings.Contains(resilience, "export function createResilientFetch") {
		t.Error("the transport is not exported on its own; a consumer can only get it by building a client it will not use")
	}
	if !strings.Contains(index, "createResilientFetch") {
		t.Error("index.ts does not re-export createResilientFetch, so the package entry hides it")
	}
	// It must not demand a baseUrl: the client being configured
	// already has one.
	if !strings.Contains(resilience, "Omit<ClientOptions, 'baseUrl'>") {
		t.Error("createResilientFetch should not require baseUrl - it is not building a client")
	}
}

// Every file in tsFiles/goFiles is rewritten by regen, so every one
// has to say so. The generated/ directory gets this from
// MarkGenerated; these are written from templates and would not,
// which is how resilience.ts came to look hand-editable while being
// overwritten on every run.
func TestEveryRewrittenFileSaysItIsGenerated(t *testing.T) {
	for _, tc := range []struct {
		lang  string
		files func(Params) ([]File, error)
		p     Params
	}{
		{"ts", TypeScript, Params{Name: "x", Version: "1.0.0", ImportPath: "./x"}},
		{"go", Go, Params{Name: "x", Version: "1.0.0", Package: "api", ImportPath: "example.com/x"}},
	} {
		files, err := tc.files(tc.p)
		if err != nil {
			t.Fatalf("%s: %v", tc.lang, err)
		}
		if len(files) == 0 {
			t.Fatalf("%s: no files", tc.lang)
		}
		for _, f := range files {
			// The shared Header, not a loose match on "Do not edit":
			// a template that grows its own copy of the wording
			// drifts from every other file, and matching loosely is
			// what let that happen before.
			if !strings.HasPrefix(f.Body, Header("//")) &&
				!strings.HasPrefix(f.Body, "/* eslint-disable */\n"+Header("//")) {
				t.Errorf("%s/%s does not start with the shared header; it has its own copy of the wording:\n%s",
					tc.lang, f.Path, firstLines(f.Body, 4))
			}
			// TypeScript additionally has to opt out of lint. These
			// files sit ABOVE generated/, so the consuming repo's
			// ignore patterns - which name that directory - do not
			// reach them, and house rules like `curly` fail a build
			// over code nobody there wrote.
			if tc.lang == "ts" && !strings.Contains(f.Body, "/* eslint-disable */") {
				t.Errorf("ts/%s does not disable lint; it is generated but outside generated/", f.Path)
			}
		}
	}
}

// ogen's NewClient takes a SecuritySource before its options exactly
// when the spec declares securitySchemes. Our constructor wraps it, so
// it has to render the matching signature - pokedex declares none and
// Zoom declares one, so both shapes ship.
func TestGoConstructorMatchesOgensSignature(t *testing.T) {
	for _, tc := range []struct {
		name        string
		hasSecurity bool
		wantSig     string
		wantCall    string
	}{
		{
			name:     "no securitySchemes",
			wantSig:  "func New(serverURL, name string, opts ...TransportOption) (*Client, error)",
			wantCall: "return NewClient(serverURL, WithClient(NewHTTPClient(name, opts...)))",
		},
		{
			name:        "with securitySchemes",
			hasSecurity: true,
			wantSig:     "func New(serverURL, name string, sec SecuritySource, opts ...TransportOption) (*Client, error)",
			wantCall:    "return NewClient(serverURL, sec, WithClient(NewHTTPClient(name, opts...)))",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files, err := Go(Params{
				Name: "x", Version: "1.0.0", Package: "api",
				ImportPath: "example.com/x", HasSecurity: tc.hasSecurity,
			})
			if err != nil {
				t.Fatal(err)
			}
			var client string
			for _, f := range files {
				if f.Path == "client.go" {
					client = f.Body
				}
			}
			if client == "" {
				t.Fatal("no client.go")
			}
			if !strings.Contains(client, tc.wantSig) {
				t.Errorf("missing signature:\n  want %s", tc.wantSig)
			}
			if !strings.Contains(client, tc.wantCall) {
				t.Errorf("missing call:\n  want %s", tc.wantCall)
			}
		})
	}
}

// Generated Go has to be gofmt-clean. It lands in consumers' repos
// where a CI step or a pre-commit hook runs gofmt, and a diff there
// points at a file the consumer cannot usefully edit - the fix is
// always back here in the template.
//
// Read from the TEMPLATES, not from Go(): that runs its output through
// gofmt, so a test reading it would be asking whether gofmt's output
// is gofmt-clean. It is, for anything that parses, which makes the
// assertion unfailable: reverting the {{- end }} this gate exists to
// hold left it green, which is how the first version of this fix
// shipped a gate that could not fail.
//
// Both with and without a paged operation, because paging_ops.go is
// written only when something is paged (when: anyPaged) and a template
// can be wrong only on the path the other fixture never renders. That
// is how a blank line at the end of paging_ops.go survived a gate that
// rendered neither.
func TestGeneratedGoIsFormatted(t *testing.T) {
	checked := map[string]bool{}
	for _, paged := range [][]PagedOp{
		{{
			OperationID: "listThings", CursorParam: "after",
			NextPath: "/nextCursor", ItemsPath: "/things", ItemType: "Thing",
		}},
		nil,
	} {
		p := Params{
			Name: "x", Version: "1.0.0", Package: "api",
			ImportPath: "example.com/x", Paged: paged,
		}
		for tmpl, path := range GoTemplates(p) {
			body, err := RenderWith(tmpl, p, Defaults())
			if err != nil {
				t.Fatalf("%s: %v", tmpl, err)
			}
			checked[path] = true
			want, err := format.Source([]byte(body))
			if err != nil {
				t.Errorf("%s does not parse: %v\n%s", path, err, firstLines(body, 5))
				continue
			}
			if string(want) != body {
				t.Errorf("%s is not gofmt-clean; fix %s, not the output.\ngot:\n%s\nwant:\n%s",
					path, tmpl, firstDiff(body, string(want)), firstDiff(string(want), body))
			}
		}
	}
	// Every Go file the generator can write has to have been read,
	// or this gate is green over whatever it happened to render.
	for tmpl, path := range GoTemplates(Params{
		Name: "x", Version: "1.0.0", Package: "api", ImportPath: "example.com/x",
		Paged: []PagedOp{{OperationID: "listThings", CursorParam: "after",
			NextPath: "/nextCursor", ItemsPath: "/things", ItemType: "Thing"}},
	}) {
		if !checked[path] {
			t.Errorf("%s (%s) rendered empty under both fixtures; the gate never saw it", path, tmpl)
		}
	}
}

// firstDiff is the first line on which two renderings differ, with its
// number, so a formatting failure says where rather than only which
// file.
func firstDiff(a, b string) string {
	as, bs := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range as {
		if i >= len(bs) {
			if as[i] == "" {
				return fmt.Sprintf("line %d: a trailing blank line", i+1)
			}
			return fmt.Sprintf("line %d: extra %q", i+1, as[i])
		}
		if as[i] != bs[i] {
			return fmt.Sprintf("line %d: %q", i+1, as[i])
		}
	}
	if len(bs) > len(as) {
		return fmt.Sprintf("line %d: missing %q", len(as)+1, bs[len(as)])
	}
	return "(no line differs)"
}

// firstLines is the start of a rendered file, for an error that has to
// show what a header actually looks like.
func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// No template may spell the header out itself.
//
// Checking the rendered output cannot catch this: a hand-written copy
// renders to exactly the same bytes as the call, right up until
// someone changes one and not the other six. That is how the wording
// came to exist in eight places in two variants, and how a reader of
// hey-api's output was told not to edit it without being told what
// would overwrite them. So read the templates themselves.
func TestNoTemplateWritesItsOwnHeader(t *testing.T) {
	entries, err := templates.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var seen int
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".tmpl") {
			continue
		}
		seen++
		body, err := templates.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(body, []byte(generatedMarker)) {
			t.Errorf("%s writes the header itself; call {{ header `//` }} so the wording has one home",
				e.Name())
		}
		if !bytes.Contains(body, []byte("{{ header ")) {
			t.Errorf("%s has no header call, so what it renders says nothing about being generated",
				e.Name())
		}
	}
	if seen == 0 {
		t.Fatal("no templates found; this test would pass on an empty directory")
	}
}
