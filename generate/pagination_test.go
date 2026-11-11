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

// The search and replace that turn the fixture's operation into a
// paged one with nothing overridden, which is what most of these tests
// want. Constants rather than baked into oneArray so a test can mark
// it differently - or leave it unmarked, which is its own case now.
const (
	unmarked = "      operationId: listThings"
	withMark = "      operationId: listThings\n      x-oapi-clientgen-pagination: {}"
)

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

// A marker with no fields is the whole declaration for an API using
// the default names, so adopting it is one line rather than three.
func TestAnEmptyMarkerTakesTheConventionalNames(t *testing.T) {
	ops, err := pagedOperations(spec(t, variant(t, oneArray, unmarked, withMark)))
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

// Nothing pages unless it says so.
//
// The spec here follows the convention exactly - an `after` query
// parameter and a `nextCursor` in the response - and that used to be
// enough on its own. Inferring it meant an operation taking `after` as
// a date filter got an iterator nobody asked for, and an operation
// that really paged under other names got silence. Both were quiet.
func TestAnUnmarkedOperationDoesNotPage(t *testing.T) {
	ops, err := pagedOperations(spec(t, oneArray))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Errorf("an unmarked operation was read as paging: %+v", ops)
	}
}

// Naming a cursor that is not there and leaving a non-conforming
// operation on the default are the same failure with different
// remedies, so the message cannot offer one piece of advice.
//
// An author who named it needs a name the operation declares; one who
// named nothing needs to start naming. Advice for the wrong half reads
// as a suggestion to change what is already correct.
func TestTheAdviceMatchesWhichHalfTheAuthorWrote(t *testing.T) {
	for _, c := range []struct {
		name, marker, want, notWant string
	}{
		{
			name:    "named a cursor the operation does not declare",
			marker:  "      x-oapi-clientgen-pagination: {cursor: {name: page_token}}",
			want:    "point x-oapi-clientgen-pagination.cursor at a parameter",
			notWant: "set x-oapi-clientgen-pagination.cursor",
		},
		{
			name:    "left the cursor to the default, which does not fit",
			marker:  "      x-oapi-clientgen-pagination: {}",
			want:    "set x-oapi-clientgen-pagination.cursor",
			notWant: "point x-oapi-clientgen-pagination.cursor at a parameter",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := pagedOperations(spec(t, variant(t, oneArray,
				"      operationId: listThings",
				"      operationId: listThings\n"+c.marker,
				"- {name: after, in: query, schema: {type: string}}",
				"- {name: limit, in: query, schema: {type: integer}}")))
			if err == nil {
				t.Fatal("a cursor the operation does not declare was accepted")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("advice is not %q: %v", c.want, err)
			}
			if strings.Contains(err.Error(), c.notWant) {
				t.Errorf("advice is for the other half (%q): %v", c.notWant, err)
			}
		})
	}
}

// A key with nothing after the colon is YAML null, which decodes to
// the same nil as no key at all - and means the opposite. An author
// writes it believing they marked the operation, so reading it as
// unmarked is the silent miss this marker exists to remove.
func TestAMarkerWithNoValueIsRefused(t *testing.T) {
	_, err := pagedOperations(spec(t, variant(t, oneArray,
		unmarked, "      operationId: listThings\n      x-oapi-clientgen-pagination:")))
	if err == nil {
		t.Fatal("a marker with no value was read as unmarked, silently")
	}
	for _, want := range []string{"no value", "{}", "remove the key"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

func TestMarkedWithoutTheCursorParameterIsRefused(t *testing.T) {
	_, err := pagedOperations(spec(t, variant(t, oneArray,
		unmarked, withMark,
		"- {name: after, in: query, schema: {type: string}}",
		"- {name: limit, in: query, schema: {type: integer}}")))
	if err == nil {
		t.Fatal("an operation marked as paging, declaring no cursor, was accepted")
	}
	// What it wanted, what is actually there, and both ways out: the
	// author either named the wrong parameter or marked the wrong
	// operation, and the message cannot tell which.
	for _, want := range []string{"after", "limit", "cursor", "drop the marker"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// Two arrays and no items: refuse rather than page the wrong one. A
// guess here compiles and reads the wrong collection.
func TestTwoArraysWithoutItemsIsRefused(t *testing.T) {
	_, err := pagedOperations(spec(t, variant(t, oneArray,
		unmarked, withMark,
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

// A marker is a claim about the response as well as the request. An
// operation that says it pages but answers with no cursor would
// generate a wrapper reading a field that is not there, so it is
// refused here rather than in the consumer's build.
func TestMarkedWithoutACursorFieldIsRefused(t *testing.T) {
	_, err := pagedOperations(spec(t, variant(t, oneArray,
		unmarked, withMark,
		"        nextCursor: {type: string}", "        updatedAt: {type: string}")))
	if err == nil {
		t.Fatal("an operation marked as paging, whose response carries no cursor, was accepted")
	}
	// The field it looked for, the fields that are actually there, and
	// both ways out. Without the second a reader cannot tell whether
	// they misnamed it or marked the wrong operation.
	for _, want := range []string{"nextCursor", "updatedAt", "drop the marker"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
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
	ops, err := pagedOperations(spec(t, variant(t, oneArray, unmarked, withMark)))
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

// A parameter is a name AND a location, so after in a header is not
// the cursor the default names mean. Matching on the name alone would
// generate a query parameter the operation cannot read.
func TestACursorOutsideTheQueryIsRefused(t *testing.T) {
	_, err := pagedOperations(spec(t, variant(t, oneArray,
		unmarked, withMark,
		"- {name: after, in: query, schema: {type: string}}",
		"- {name: after, in: header, schema: {type: string}}")))
	if err == nil {
		t.Fatal("a header named after was taken as the cursor")
	}
	// The location is half the identity of a parameter, so the error
	// has to show both - "after" alone reads as though the operation
	// does not declare it at all, and it does.
	for _, want := range []string{"after", "query", "header"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
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
		"      parameters:", "      x-oapi-clientgen-pagination: {}\n      parameters:",
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
		unmarked, withMark,
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

// A required cursor field is refused, because it cannot mean what a
// cursor means: its absence is how a response says the collection
// ended.
func TestARequiredCursorIsRefused(t *testing.T) {
	_, err := pagedOperations(spec(t, variant(t, oneArray,
		unmarked, withMark,
		"      type: object\n      properties:",
		"      type: object\n      required: [nextCursor]\n      properties:")))
	if err == nil {
		t.Fatal("a required nextCursor was accepted; the generated client would not compile")
	}
	for _, want := range []string{"listThings", "nextCursor", "optional"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// A field required somewhere OTHER than the cursor is none of this
// check's business: items is required in plenty of real specs.
func TestARequiredItemsArrayStillPages(t *testing.T) {
	ops, err := pagedOperations(spec(t, variant(t, oneArray,
		unmarked, withMark,
		"      type: object\n      properties:",
		"      type: object\n      required: [things]\n      properties:")))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops = %+v, want the operation to page", ops)
	}
}

// And the same for the cursor PARAMETER, where the contradiction
// lands one request earlier: a required cursor leaves no way to ask
// for the first page.
func TestARequiredCursorParameterIsRefused(t *testing.T) {
	_, err := pagedOperations(spec(t, variant(t, oneArray,
		unmarked, withMark,
		"{name: after, in: query, schema: {type: string}}",
		"{name: after, in: query, required: true, schema: {type: string}}")))
	if err == nil {
		t.Fatal("a required after was accepted; the generated client would not compile")
	}
	for _, want := range []string{"listThings", "after", "optional"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// A parameter required alongside an optional cursor is none of this
// check's business.
func TestAnotherRequiredParameterStillPages(t *testing.T) {
	ops, err := pagedOperations(spec(t, variant(t, oneArray,
		unmarked, withMark,
		"        - {name: after, in: query, schema: {type: string}}",
		"        - {name: after, in: query, schema: {type: string}}\n        - {name: team, in: query, required: true, schema: {type: string}}")))
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("ops = %+v, want the operation to page", ops)
	}
}
