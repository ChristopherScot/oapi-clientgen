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

// variant edits the fixture and fails if an edit matched nothing: a
// strings.Replace that misses silently tests the unmodified spec, and
// passes for the wrong reason.
func variant(t *testing.T, base string, pairs ...string) string {
	t.Helper()
	for i := 0; i < len(pairs); i += 2 {
		if !strings.Contains(base, pairs[i]) {
			t.Fatalf("fixture does not contain %q", pairs[i])
		}
		base = strings.Replace(base, pairs[i], pairs[i+1], 1)
	}
	return base
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
	if got.OperationID != "listThings" || got.CursorParam != "after" {
		t.Errorf("cursor = %s on %s", got.CursorParam, got.OperationID)
	}
	if got.NextPath != "/nextCursor" || got.ItemsPath != "/things" || got.ItemType != "Thing" {
		t.Errorf("next=%s items=%s type=%s", got.NextPath, got.ItemsPath, got.ItemType)
	}
}

// Most operations do not page and must produce nothing, not an error.
func TestAnOperationWithoutTheParameterIsNotPaged(t *testing.T) {
	ops, err := pagedOperations(spec(t, variant(t, oneArray,
		"- {name: after, in: query, schema: {type: string}}",
		"- {name: limit, in: query, schema: {type: integer}}")))
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
	_, err := pagedOperations(spec(t, variant(t, oneArray,
		"        nextCursor: {type: string}",
		"        learnedBy: {type: array, items: {$ref: '#/components/schemas/Thing'}}\n        nextCursor: {type: string}")))
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
	ops, err := pagedOperations(spec(t, variant(t, oneArray,
		"        nextCursor: {type: string}",
		"        learnedBy: {type: array, items: {$ref: '#/components/schemas/Thing'}}\n        nextCursor: {type: string}",
		"      operationId: listThings",
		"      operationId: listThings\n      x-oapi-clientgen-pagination: {items: /things}",
	)))
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
	_, err := pagedOperations(spec(t, variant(t, oneArray,
		"      operationId: listThings",
		"      operationId: listThings\n      x-oapi-clientgen-pagination: {cursor: {name: page_token, in: query}}")))
	if err == nil {
		t.Fatal("a cursor naming no declared parameter was accepted")
	}
	if !strings.Contains(err.Error(), "page_token") || !strings.Contains(err.Error(), "after") {
		t.Errorf("error names neither the missing cursor nor what is declared: %v", err)
	}
}

// A vendor's names, stated once, with no convention anywhere.
func TestAnOverrideReplacesTheConvention(t *testing.T) {
	ops, err := pagedOperations(spec(t, variant(t, oneArray,
		"- {name: after, in: query, schema: {type: string}}",
		"- {name: page_token, in: query, schema: {type: string}}",
		"        nextCursor: {type: string}",
		"        next_page_token: {type: string}",
		"      operationId: listThings",
		"      operationId: listThings\n      x-oapi-clientgen-pagination:\n        cursor: {name: page_token, in: query}\n        next: /next_page_token",
	)))
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
	ops, err := pagedOperations(spec(t, variant(t, oneArray,
		"        nextCursor: {type: string}", "        updatedAt: {type: string}")))
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
	_, err := pagedOperations(spec(t, variant(t, oneArray,
		"        nextCursor: {type: string}", "        updatedAt: {type: string}",
		"      operationId: listThings",
		"      operationId: listThings\n      x-oapi-clientgen-pagination: {next: /nextCursor}",
	)))
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

// after in a header is not the cursor. The convention is a name AND a
// location: the name alone would match a date filter passed as a
// header and generate a query parameter the operation cannot read.
func TestAfterSomewhereOtherThanTheQueryIsNotTheCursor(t *testing.T) {
	ops, err := pagedOperations(spec(t, variant(t, oneArray,
		"- {name: after, in: query, schema: {type: string}}",
		"- {name: after, in: header, schema: {type: string}}")))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("found %d paged operations for a header named after: %+v", len(ops), ops)
	}
}

// The override matches on location too, so naming a parameter that
// exists somewhere else is still wrong.
func TestAnOverrideMatchesTheLocationAsWell(t *testing.T) {
	_, err := pagedOperations(spec(t, variant(t, oneArray,
		"- {name: after, in: query, schema: {type: string}}",
		"- {name: page_token, in: header, schema: {type: string}}",
		"      operationId: listThings",
		"      operationId: listThings\n      x-oapi-clientgen-pagination:\n        cursor: {name: page_token, in: query}\n        next: /nextCursor",
	)))
	if err == nil {
		t.Fatal("a cursor naming a header as though it were a query parameter was accepted")
	}
	if !strings.Contains(err.Error(), "page_token") {
		t.Errorf("error does not name the cursor: %v", err)
	}
}

// in defaults to query, which is the whole point of letting it be
// omitted.
func TestAnOverrideWithoutALocationMeansQuery(t *testing.T) {
	ops, err := pagedOperations(spec(t, variant(t, oneArray,
		"- {name: after, in: query, schema: {type: string}}",
		"- {name: page_token, in: query, schema: {type: string}}",
		"      operationId: listThings",
		"      operationId: listThings\n      x-oapi-clientgen-pagination:\n        cursor: {name: page_token}\n        next: /nextCursor",
	)))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].CursorParam != "page_token" {
		t.Fatalf("got %+v, want one operation with cursor page_token", ops)
	}
}

// A paged operation with no operationId would name a method Iter.
func TestAPagedOperationNeedsAnOperationID(t *testing.T) {
	_, err := pagedOperations(spec(t, variant(t, oneArray,
		"      operationId: listThings\n", "")))
	if err == nil {
		t.Fatal("a paged operation with no operationId was accepted")
	}
	if !strings.Contains(err.Error(), "operationId") {
		t.Errorf("error does not say what is missing: %v", err)
	}
}

// An array declared without items renders Page[] in Go, which does
// not compile.
func TestAnArrayWithoutItemsIsNotAPage(t *testing.T) {
	_, err := pagedOperations(spec(t, variant(t, oneArray,
		"        things: {type: array, items: {$ref: '#/components/schemas/Thing'}}",
		"        things: {type: array}")))
	if err == nil {
		t.Fatal("an array with no items was accepted")
	}
	if !strings.Contains(err.Error(), "no array") {
		t.Errorf("error does not say there is nothing to page over: %v", err)
	}
}

// items has to name an array exactly; a prefix is a different field.
func TestItemsDoesNotMatchOnAPrefix(t *testing.T) {
	_, err := pagedOperations(spec(t, variant(t, oneArray,
		"      operationId: listThings",
		"      operationId: listThings\n      x-oapi-clientgen-pagination: {items: /thing}")))
	if err == nil {
		t.Fatal("items naming a prefix of an array was accepted")
	}
	if !strings.Contains(err.Error(), "thing") {
		t.Errorf("error does not name what was asked for: %v", err)
	}
}

// next and items are pointers, not names, because an API answering
// {meta: {nextCursor}} is ordinary. goPath and tsPath have always
// rendered the nested access; nothing checked the reader accepted it.
func TestANestedCursorResolves(t *testing.T) {
	ops, err := pagedOperations(spec(t, variant(t, oneArray,
		"        nextCursor: {type: string}",
		"        meta: {type: object, properties: {nextCursor: {type: string}}}",
		"      operationId: listThings",
		"      operationId: listThings\n      x-oapi-clientgen-pagination: {next: /meta/nextCursor}",
	)))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].NextPath != "/meta/nextCursor" {
		t.Fatalf("got %+v, want one operation with next /meta/nextCursor", ops)
	}
}

// The walk has to reach the end of the pointer: a next naming
// /meta/nextCursor against a meta that holds something else is wrong,
// and checking only the first segment would accept it.
func TestANestedCursorMustExistAllTheWayDown(t *testing.T) {
	_, err := pagedOperations(spec(t, variant(t, oneArray,
		"        nextCursor: {type: string}",
		"        meta: {type: object, properties: {total: {type: integer}}}",
		"      operationId: listThings",
		"      operationId: listThings\n      x-oapi-clientgen-pagination: {next: /meta/nextCursor}",
	)))
	if err == nil {
		t.Fatal("a next naming a field nested under the wrong parent was accepted")
	}
	if !strings.Contains(err.Error(), "meta/nextCursor") {
		t.Errorf("error does not name the pointer: %v", err)
	}
}
