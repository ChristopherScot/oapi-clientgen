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
	"fmt"
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
	t, err := template.New(name).Parse(string(b))
	if err != nil {
		return "", fmt.Errorf("apiclient: parsing %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, p); err != nil {
		return "", fmt.Errorf("apiclient: rendering %s: %w", name, err)
	}
	return buf.String(), nil
}

// tsFiles and goFiles are the one inventory of what each client is
// made of. A consumer decides WHERE they go; what they ARE is decided
// here, so adding a file does not mean remembering a second list.
var tsFiles = []struct{ tmpl, path string }{
	{"ts_resilience.ts.tmpl", "resilience.ts"},
	{"ts_index.ts.tmpl", "index.ts"},
}

var goFiles = []struct{ tmpl, path string }{
	{"go_client.go.tmpl", "client.go"},
	{"go_paging.go.tmpl", "paging.go"},
	{"go_client_test.go.tmpl", "client_test.go"},
	{"go_paging_test.go.tmpl", "paging_test.go"},
}

// TypeScript returns the files that sit beside a generated TypeScript
// client.
func TypeScript(p Params) ([]File, error) { return files(tsFiles, p) }

// Go returns the files that sit beside a generated Go client.
func Go(p Params) ([]File, error) { return files(goFiles, p) }

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
