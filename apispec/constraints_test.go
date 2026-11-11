package apispec_test

import (
	"strings"
	"testing"

	"github.com/ChristopherScot/oapi-gen/apispec"
	"gopkg.in/yaml.v3"
)

func decode(t *testing.T, body string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	return &doc
}

// at walks a decoded document by key, for a test reading one value
// back out.
func at(t *testing.T, n *yaml.Node, keys ...string) *yaml.Node {
	t.Helper()
	if n.Kind == yaml.DocumentNode {
		n = n.Content[0]
	}
	for _, key := range keys {
		found := false
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				n, found = n.Content[i+1], true
				break
			}
		}
		if !found {
			t.Fatalf("no %q in the document", key)
		}
	}
	return n
}

// The constraints a caller cannot otherwise see reach the description
// a generator renders.
func TestConstraintsReachTheDescription(t *testing.T) {
	doc := decode(t, `
paths:
  /things:
    get:
      parameters:
        - name: limit
          in: query
          description: How many to return.
          schema: {type: integer, minimum: 1, maximum: 50}
`)
	was := paramDescription(t, doc)
	if n := apispec.AnnotateNode(doc); n != 1 {
		t.Fatalf("annotated %d descriptions, want 1", n)
	}
	got := paramDescription(t, doc)
	for _, want := range []string{"minimum: 1", "maximum: 50"} {
		if !strings.Contains(got, want) {
			t.Errorf("description %q does not contain %q", got, want)
		}
	}
	// Whatever the author wrote is still there, unaltered. What it
	// SAYS is theirs; this only holds that appending did not take it.
	if !strings.HasPrefix(got, was) {
		t.Errorf("description %q no longer opens with the author's text %q", got, was)
	}
}

// A schema declaring nothing is left exactly as it was: most
// descriptions in a spec are untouched by this.
func TestAnUnconstrainedSchemaIsUntouched(t *testing.T) {
	doc := decode(t, `
paths:
  /things:
    get:
      parameters:
        - name: type
          in: query
          description: Filter by type.
          schema: {type: string}
`)
	was := paramDescription(t, doc)
	if n := apispec.AnnotateNode(doc); n != 0 {
		t.Fatalf("annotated %d descriptions, want 0", n)
	}
	// Compared against what the fixture put there rather than a
	// literal, so rewording the fixture cannot fail this.
	if got := paramDescription(t, doc); got != was {
		t.Errorf("description = %q, want it unchanged from %q", got, was)
	}
}

// Running twice says the same thing once: regeneration is not a
// pipeline that keeps appending.
func TestAnnotatingTwiceIsNotCumulative(t *testing.T) {
	const body = `
paths:
  /things:
    get:
      parameters:
        - name: limit
          in: query
          description: How many.
          schema: {type: integer, maximum: 50}
`
	doc := decode(t, body)
	apispec.AnnotateNode(doc)
	first := paramDescription(t, doc)
	if n := apispec.AnnotateNode(doc); n != 0 {
		t.Errorf("a second pass annotated %d descriptions, want 0", n)
	}
	second := paramDescription(t, doc)
	if second != first {
		t.Errorf("second pass changed the description:\n%q\n%q", first, second)
	}
	if strings.Count(second, "maximum: 50") != 1 {
		t.Errorf("maximum appears more than once: %q", second)
	}
}

// A pattern that would end a TypeScript block comment is escaped,
// into something that is still the same regex.
func TestAPatternCannotEndABlockComment(t *testing.T) {
	doc := decode(t, `
paths:
  /things:
    get:
      parameters:
        - name: slug
          in: query
          description: The slug.
          schema: {type: string, pattern: "^[a-z]*/$"}
`)
	apispec.AnnotateNode(doc)
	got := paramDescription(t, doc)
	if strings.Contains(got, "*/") {
		t.Errorf("description still ends a block comment: %q", got)
	}
	if !strings.Contains(got, `*\/`) {
		t.Errorf("description %q does not carry the escaped pattern", got)
	}
}

// A property deep in components is reached too, not only a parameter.
func TestAPropertyIsAnnotated(t *testing.T) {
	doc := decode(t, `
components:
  schemas:
    Thing:
      type: object
      properties:
        slug:
          type: string
          description: URL-safe id.
          maxLength: 30
`)
	if n := apispec.AnnotateNode(doc); n != 1 {
		t.Fatalf("annotated %d, want 1", n)
	}
	got := at(t, doc, "components", "schemas", "Thing", "properties", "slug", "description").Value
	if !strings.Contains(got, "maxLength: 30") {
		t.Errorf("description %q lost the constraint", got)
	}
}

func paramDescription(t *testing.T, doc *yaml.Node) string {
	t.Helper()
	params := at(t, doc, "paths", "/things", "get", "parameters")
	for i := 0; i+1 < len(params.Content[0].Content); i += 2 {
		if params.Content[0].Content[i].Value == "description" {
			return params.Content[0].Content[i+1].Value
		}
	}
	return ""
}

// Only schemas are documented.
//
// A walk over every map in the document annotates whatever happens to
// hold a constraint keyword: a vendor extension, a security scheme, an
// example payload. Each one gets a description written into a part of
// the spec that is not a schema and renders no doc comment, and the
// example stops being the value it was meant to show.
func TestOnlySchemasAreAnnotated(t *testing.T) {
	doc := decode(t, `
x-ratelimit: {maximum: 50}
components:
  securitySchemes:
    apiKey: {type: apiKey, name: X-Key, in: header, pattern: "^k-"}
  schemas:
    Thing:
      type: object
      example: {slug: abc, maxLength: 9}
      properties:
        slug: {type: string, maxLength: 30}
`)
	if n := apispec.AnnotateNode(doc); n != 1 {
		t.Fatalf("annotated %d nodes, want only the property", n)
	}
	for _, path := range [][]string{
		{"components", "securitySchemes", "apiKey"},
		{"x-ratelimit"},
		{"components", "schemas", "Thing", "example"},
	} {
		node := at(t, doc, path...)
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == "description" {
				t.Errorf("%v was documented: %q", path, node.Content[i+1].Value)
			}
		}
	}
}

// The document keeps the order its author wrote.
//
// Re-encoded through a map it would not: a Go map sorts its keys, so
// the generators would see every field alphabetically and reorder
// every struct they emit. That is a diff the size of the generated
// client, on a change that adds two lines.
func TestAnnotatingKeepsTheDocumentsOrder(t *testing.T) {
	doc := decode(t, `
components:
  schemas:
    Thing:
      type: object
      properties:
        zebra: {type: string}
        alpha: {type: string, maxLength: 4}
        middle: {type: string}
`)
	apispec.AnnotateNode(doc)
	props := at(t, doc, "components", "schemas", "Thing", "properties")

	var order []string
	for i := 0; i+1 < len(props.Content); i += 2 {
		order = append(order, props.Content[i].Value)
	}
	want := []string{"zebra", "alpha", "middle"}
	if len(order) != len(want) {
		t.Fatalf("properties = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("properties = %v, want %v", order, want)
		}
	}
}

// A schema is documented wherever a spec puts it, not only inline.
//
// components holds what a spec reuses, and a $ref into it is how an
// operation reaches that. Walking only the inline positions documents
// a $ref into components/schemas - because the walk meets the target
// there - and silently skips one into components/parameters, with no
// error and no change in the count reported.
func TestEveryComponentPositionIsReached(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"parameters", `
components:
  parameters:
    Limit: {name: limit, in: query, schema: {type: integer, maximum: 50}}
`},
		{"headers", `
components:
  headers:
    XRate: {schema: {type: integer, maximum: 9}}
`},
		{"responses", `
components:
  responses:
    Page:
      description: ok
      content: {application/json: {schema: {type: string, maxLength: 4}}}
`},
		{"requestBodies", `
components:
  requestBodies:
    Body:
      content: {application/json: {schema: {type: string, maxLength: 4}}}
`},
		{"schemas", `
components:
  schemas:
    Thing: {type: string, maxLength: 4}
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if n := apispec.AnnotateNode(decode(t, tc.body)); n != 1 {
				t.Errorf("annotated %d under components/%s, want 1", n, tc.name)
			}
		})
	}
}

// A schema reached by alias is documented like any other.
//
// A spec that declares a shape once with an anchor holds an AliasNode
// where a mapping is expected. Skipping those documents whichever use
// the walk met first - or none of them, when the anchor sits outside
// the positions walked.
func TestAnAliasedSchemaIsDocumented(t *testing.T) {
	t.Run("shared by two parameters", func(t *testing.T) {
		doc := decode(t, `
paths:
  /things:
    get:
      parameters:
        - {name: slug, in: query, description: One., schema: &s {type: string, maxLength: 30}}
        - {name: other, in: query, description: Two., schema: *s}
`)
		if n := apispec.AnnotateNode(doc); n != 2 {
			t.Errorf("annotated %d parameters, want both", n)
		}
	})

	t.Run("anchored outside the walk", func(t *testing.T) {
		doc := decode(t, `
x-common:
  slug: &slug {type: string, maxLength: 30}
components:
  schemas:
    Thing: *slug
`)
		if n := apispec.AnnotateNode(doc); n != 1 {
			t.Errorf("annotated %d, want the aliased schema", n)
		}
	})
}
