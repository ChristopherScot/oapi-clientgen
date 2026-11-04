package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherScot/oapi-clientgen/apiclient"
)

// spec writes a document and returns its path. Written per test rather
// than shared: these are about what one document says, and a shared
// fixture would have to say everything at once.
func spec(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openapi.yml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const oneArray = `
openapi: 3.0.0
info: {title: probe, version: 1.0.0}
paths:
  /things:
    get:
      operationId: listThings
      parameters:
        - {name: after, in: query, schema: {type: string}}
      responses:
        '200':
          content:
            application/json:
              schema: {$ref: '#/components/schemas/ThingList'}
components:
  schemas:
    Thing: {type: object}
    ThingList:
      type: object
      properties:
        things: {type: array, items: {$ref: '#/components/schemas/Thing'}}
        nextCursor: {type: string}
`

// An operation saying nothing at all still pages, because it declares
// the parameter the convention names.
func TestTheConventionNeedsNoExtension(t *testing.T) {
	ops, err := pagedOperations(spec(t, oneArray))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("found %d paged operations, want 1", len(ops))
	}
	got := ops[0]
	if got.OperationID != "listThings" || got.CursorParam != "after" || got.CursorIn != "query" {
		t.Errorf("cursor = %s %s on %s", got.CursorParam, got.CursorIn, got.OperationID)
	}
	if got.NextPath != "/nextCursor" || got.ItemsPath != "/things" || got.ItemType != "Thing" {
		t.Errorf("next=%s items=%s type=%s", got.NextPath, got.ItemsPath, got.ItemType)
	}
}

// Most operations do not page and must produce nothing, not an error.
func TestAnOperationWithoutTheParameterIsNotPaged(t *testing.T) {
	ops, err := pagedOperations(spec(t, strings.Replace(oneArray,
		"- {name: after, in: query, schema: {type: string}}",
		"- {name: limit, in: query, schema: {type: integer}}", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("found %d paged operations for a spec that pages none: %+v", len(ops), ops)
	}
}

// Two arrays and no items: refuse rather than page the wrong one. A
// guess here compiles and reads the wrong collection.
func TestTwoArraysWithoutItemsIsRefused(t *testing.T) {
	_, err := pagedOperations(spec(t, strings.Replace(oneArray,
		"        nextCursor: {type: string}",
		"        learnedBy: {type: array, items: {$ref: '#/components/schemas/Thing'}}\n        nextCursor: {type: string}", 1)))
	if err == nil {
		t.Fatal("two arrays and no items was accepted")
	}
	for _, want := range []string{"learnedBy", "things", "items"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// Named, the ambiguity is gone.
func TestItemsResolvesTheAmbiguity(t *testing.T) {
	ops, err := pagedOperations(spec(t, strings.NewReplacer(
		"        nextCursor: {type: string}",
		"        learnedBy: {type: array, items: {$ref: '#/components/schemas/Thing'}}\n        nextCursor: {type: string}",
		"      operationId: listThings",
		"      operationId: listThings\n      x-oapi-clientgen-pagination: {items: /things}",
	).Replace(oneArray)))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].ItemsPath != "/things" {
		t.Fatalf("items = %+v, want /things", ops)
	}
}

// A cursor the operation does not declare is caught here rather than
// in the consumer's build.
func TestACursorThatIsNotAParameterIsRefused(t *testing.T) {
	_, err := pagedOperations(spec(t, strings.Replace(oneArray,
		"      operationId: listThings",
		"      operationId: listThings\n      x-oapi-clientgen-pagination: {cursor: {name: page_token, in: query}}", 1)))
	if err == nil {
		t.Fatal("a cursor naming no declared parameter was accepted")
	}
	if !strings.Contains(err.Error(), "page_token") || !strings.Contains(err.Error(), "after") {
		t.Errorf("error names neither the missing cursor nor what is declared: %v", err)
	}
}

// A vendor's names, stated once, with no convention anywhere.
func TestAnOverrideReplacesTheConvention(t *testing.T) {
	ops, err := pagedOperations(spec(t, strings.NewReplacer(
		"- {name: after, in: query, schema: {type: string}}",
		"- {name: page_token, in: query, schema: {type: string}}",
		"        nextCursor: {type: string}",
		"        next_page_token: {type: string}",
		"      operationId: listThings",
		"      operationId: listThings\n      x-oapi-clientgen-pagination:\n        cursor: {name: page_token, in: query}\n        next: /next_page_token",
	).Replace(oneArray)))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("found %d, want 1", len(ops))
	}
	if ops[0].CursorParam != "page_token" || ops[0].NextPath != "/next_page_token" {
		t.Errorf("got cursor=%s next=%s", ops[0].CursorParam, ops[0].NextPath)
	}
}

// after on its own is a plausible date filter. Taking it without
// answering with a cursor is not paging, and generating a wrapper for
// it would read a field that is not there.
func TestAfterWithoutACursorFieldIsNotPaged(t *testing.T) {
	ops, err := pagedOperations(spec(t, strings.Replace(oneArray,
		"        nextCursor: {type: string}", "        updatedAt: {type: string}", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("found %d paged operations for a date filter: %+v", len(ops), ops)
	}
}

// An override says it pages, so a next naming nothing is an error
// rather than a silent skip - the author meant it and got it wrong.
func TestAnOverrideNamingNoFieldIsRefused(t *testing.T) {
	_, err := pagedOperations(spec(t, strings.NewReplacer(
		"        nextCursor: {type: string}", "        updatedAt: {type: string}",
		"      operationId: listThings",
		"      operationId: listThings\n      x-oapi-clientgen-pagination: {next: /nextCursor}",
	).Replace(oneArray)))
	if err == nil {
		t.Fatal("a next naming no response field was accepted")
	}
	if !strings.Contains(err.Error(), "nextCursor") || !strings.Contains(err.Error(), "updatedAt") {
		t.Errorf("error does not name what was wanted and what is there: %v", err)
	}
}

// The wiring: what the reader finds has to reach the templates. The
// render-and-compile harnesses in apiclient cover what the templates
// then do with it, but nothing else checks that Params carries it.
func TestWhatTheReaderFindsReachesTheTemplates(t *testing.T) {
	ops, err := pagedOperations(spec(t, oneArray))
	if err != nil {
		t.Fatal(err)
	}

	files, err := apiclient.Go(apiclient.Params{
		Name: "probe", Version: "1.0.0", Package: "api", Paged: ops,
	})
	if err != nil {
		t.Fatal(err)
	}

	var body string
	for _, f := range files {
		if f.Path == "paging_ops.go" {
			body = f.Body
		}
	}
	if body == "" {
		t.Fatal("no paging_ops.go rendered for a spec that pages")
	}
	for _, want := range []string{"ListThingsIter", "Page[Thing]", "res.Things", "res.NextCursor"} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered client does not mention %q:\n%s", want, body)
		}
	}
}

// A spec where nothing pages gets no file, rather than an empty one.
func TestNoPagedOperationsMeansNoFile(t *testing.T) {
	files, err := apiclient.Go(apiclient.Params{Name: "probe", Version: "1.0.0", Package: "api"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path == "paging_ops.go" {
			t.Errorf("paging_ops.go shipped for a spec that pages nothing:\n%s", f.Body)
		}
	}
}
