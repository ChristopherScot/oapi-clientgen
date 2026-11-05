package generate

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ChristopherScot/oapi-clientgen/apiclient"
	"gopkg.in/yaml.v3"
)

// The cursor convention. An operation taking cursorParam AND answering
// with nextField pages, and needs to say nothing else. Both halves are
// required: after on its own is a plausible date filter.
//
// Convention rather than configuration because the specs this
// generates from are mostly ours, and three lines stating the same two
// names on every paged operation is three lines nobody reads. A spec
// that pages differently - a vendor's, where next_page_token is their
// word and not ours - says so with the extension below.
const (
	cursorParam = "after"
	nextField   = "nextCursor"
)

// extensionKey is namespaced because the bare x-pagination is already
// APIMatic's, with a different shape, and a spec carrying both would
// say two things at once.
const extensionKey = "x-oapi-clientgen-pagination"

type specParam struct {
	Name     string `yaml:"name"`
	In       string `yaml:"in"`
	Required bool   `yaml:"required"`
}

// specPagination is what an operation says when the convention does
// not describe it. Every field is an override; an operation following
// the convention needs none of them, and most need only Items.
type specPagination struct {
	// Items is the response array holding the page, as a path. There
	// is no convention for it because a response may hold more than
	// one array - a move list that also names the pokemon that learn
	// it - and picking one would be a guess that compiles.
	Items string `yaml:"items"`

	// Cursor and Next name the parameter and the response field when
	// they are not the convention's.
	Cursor *specParam `yaml:"cursor"`
	Next   string     `yaml:"next"`
}

type specOperation struct {
	OperationID string          `yaml:"operationId"`
	Parameters  []specParam     `yaml:"parameters"`
	Pagination  *specPagination `yaml:"x-oapi-clientgen-pagination"`
}

// pagedOperations reads the operations a spec says page.
//
// OpenAPI cannot say "this operation is paged" and the field names
// vary by API, so a generator either guesses or is told. Every
// commercial generator is told. This one is told too, but briefly -
// the cursor names are a convention and the extension exists for the
// parts a convention cannot cover.
func pagedOperations(specPath string) ([]apiclient.PagedOp, error) {
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return nil, err
	}

	// Decoded twice on purpose: once into the shape this needs, and
	// once as plain maps for the schema walk. A typed struct cannot
	// describe a schema, which is recursive, and a map cannot be read
	// as tersely as the operation fields are read below.
	var doc struct {
		Paths map[string]map[string]specOperation `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("reading the operations of %s: %w", specPath, err)
	}
	var raw2 map[string]any
	if err := yaml.Unmarshal(raw, &raw2); err != nil {
		return nil, fmt.Errorf("reading %s: %w", specPath, err)
	}

	var out []apiclient.PagedOp
	for path, methods := range doc.Paths {
		for method, op := range methods {
			where := fmt.Sprintf("%s %s", strings.ToUpper(method), path)

			cursor, ok, err := op.cursor(where)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			// The same contradiction as a required response cursor,
			// one request earlier: the FIRST call has no cursor to
			// send, so requiring one leaves no way to ask for page
			// one. ogen renders it as a plain string where the
			// generated wrapper assigns an optional, so the client
			// would not compile either.
			if cursor.Required {
				return nil, fmt.Errorf(
					"%s: %s declares %q required, so the first page has no cursor to send - a cursor parameter has to be optional; drop it from the parameter's required field, or --patch it out of a spec you do not own",
					where, op.OperationID, cursor.Name)
			}
			if op.OperationID == "" {
				return nil, fmt.Errorf("%s: %s needs an operationId to name the generated method", where, extensionKey)
			}

			// The other half of the convention. Taking a parameter
			// called after is not enough on its own - after is a
			// plausible date filter - and an operation that takes one
			// without answering with a cursor would generate a
			// wrapper reading a field that is not there, failing in
			// the consumer's build inside a file telling them not to
			// edit it.
			//
			// An override says the operation pages, so a response that
			// cannot be read is the author's mistake. The convention
			// only guesses, so the same response means "not paged".
			stated := op.Pagination != nil

			schema, err := responseSchema(raw2, path, method)
			if err != nil {
				if !stated {
					continue
				}
				return nil, fmt.Errorf("%s: %s", where, err)
			}
			if !hasProperty(raw2, schema, "/"+op.next()) {
				if !stated {
					continue
				}
				return nil, fmt.Errorf("%s: %s.next names %q, which is not a property of %s's response; it holds: %s",
					where, extensionKey, op.next(), op.OperationID, propertyNames(schema))
			}

			// A required cursor cannot mean what a cursor means. Its
			// absence is how a response says the collection ended, so
			// a spec requiring it on every page has no way to say
			// "no more" - and ogen renders a required string as a
			// plain string, where the generated iterator reads an
			// optional one. The client would not compile.
			//
			// Refused rather than generated for, because a vendor
			// whose last page sends an empty cursor instead would
			// work: both templates stop on "". Supporting that means
			// carrying optionality into PagedOp and branching the Go
			// template on it. Worth doing when such a spec turns up
			// and not before - a refusal can become support, where
			// support cannot be withdrawn.
			if requiredProperty(raw2, schema, "/"+op.next()) {
				return nil, fmt.Errorf(
					"%s: %s's response requires %q, so the last page cannot omit it - a cursor field has to be optional for its absence to mean the end of the collection; drop it from the response schema's required list, or --patch it out of a spec you do not own",
					where, op.OperationID, op.next())
			}

			// Past both halves, so this pages whether it said so or
			// not, and an array nothing can pick out is an error
			// either way.
			items, err := resolveItems(raw2, schema, op, where)
			if err != nil {
				return nil, err
			}

			out = append(out, apiclient.PagedOp{
				OperationID: op.OperationID,
				CursorParam: cursor.Name,
				NextPath:    "/" + op.next(),
				ItemsPath:   "/" + items.field,
				ItemType:    items.itemType,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].OperationID < out[j].OperationID })
	return out, nil
}

// cursor reports the parameter carrying the cursor, and whether this
// operation pages at all.
//
// An operation pages if it declares the convention's parameter or
// names its own. Saying nothing and declaring nothing means it does
// not page, which is most operations.
func (op specOperation) cursor(where string) (specParam, bool, error) {
	if op.Pagination == nil || op.Pagination.Cursor == nil {
		// The convention. Whatever is found was found in the list, so
		// there is nothing left to check.
		for _, p := range op.Parameters {
			if p.Name == cursorParam && p.In == "query" {
				return p, true, nil
			}
		}
		return specParam{}, false, nil
	}

	want := *op.Pagination.Cursor
	if want.In == "" {
		want.In = "query"
	}
	declared := make([]string, 0, len(op.Parameters))
	for _, p := range op.Parameters {
		if p.Name == want.Name && p.In == want.In {
			return p, true, nil
		}
		declared = append(declared, fmt.Sprintf("%s (%s)", p.Name, p.In))
	}
	if len(declared) == 0 {
		return specParam{}, false, fmt.Errorf("%s: %s.cursor names %q in %q, and %s declares no parameters",
			where, extensionKey, want.Name, want.In, op.OperationID)
	}
	return specParam{}, false, fmt.Errorf("%s: %s.cursor names %q in %q, which %s does not declare; it declares: %s",
		where, extensionKey, want.Name, want.In, op.OperationID, strings.Join(declared, ", "))
}

func (op specOperation) next() string {
	if op.Pagination != nil && op.Pagination.Next != "" {
		return strings.TrimPrefix(op.Pagination.Next, "/")
	}
	return nextField
}

func (op specOperation) items() string {
	if op.Pagination != nil {
		return op.Pagination.Items
	}
	return ""
}

// arrayProperty is one array a response holds: the property carrying
// it, and what one element is called.
type arrayProperty struct{ field, itemType string }

// resolveItems finds the array a page arrives in.
//
// The convention stops here. A response may hold more than one array -
// a move list that also names the pokemon that learn it - and picking
// one would be a guess that compiles, pages the wrong collection and
// looks right. So one array is inferred and more than one has to be
// named.
func resolveItems(doc map[string]any, schema map[string]any, op specOperation, where string) (arrayProperty, error) {
	arrays := arrayProperties(doc, schema)

	if want := strings.TrimPrefix(op.items(), "/"); want != "" {
		for _, a := range arrays {
			if a.field == want {
				return a, nil
			}
		}
		return arrayProperty{}, fmt.Errorf("%s: %s.items names %q, which is not an array in %s's response; it holds: %s",
			where, extensionKey, want, op.OperationID, fieldNames(arrays))
	}

	switch len(arrays) {
	case 1:
		return arrays[0], nil
	case 0:
		return arrayProperty{}, fmt.Errorf("%s: %s pages but its response holds no array to page over",
			where, op.OperationID)
	default:
		return arrayProperty{}, fmt.Errorf("%s: %s's response holds %d arrays (%s), so %s.items has to say which is the page",
			where, op.OperationID, len(arrays), fieldNames(arrays), extensionKey)
	}
}

// responseSchema is the 200 response's JSON schema, $ref followed.
func responseSchema(doc map[string]any, path, method string) (map[string]any, error) {
	paths, _ := doc["paths"].(map[string]any)
	m, _ := paths[path].(map[string]any)
	op, _ := m[method].(map[string]any)
	responses, _ := op["responses"].(map[string]any)
	ok200, _ := responses["200"].(map[string]any)
	content, _ := ok200["content"].(map[string]any)
	json, _ := content["application/json"].(map[string]any)
	schema, _ := json["schema"].(map[string]any)
	if schema == nil {
		return nil, fmt.Errorf("no 200 response with a JSON body, so there is no page to read")
	}
	return deref(doc, schema), nil
}

// deref follows a $ref into components, once. A schema that refs a
// schema that refs another is not something these specs do.
func deref(doc map[string]any, node map[string]any) map[string]any {
	ref, _ := node["$ref"].(string)
	if ref == "" {
		return node
	}
	cur := any(doc)
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, ok := cur.(map[string]any)
		if !ok {
			return node
		}
		cur = m[part]
	}
	if m, ok := cur.(map[string]any); ok {
		return m
	}
	return node
}

// arrayProperties are the schema's array properties, each with the
// name of its element type.
func arrayProperties(doc map[string]any, schema map[string]any) []arrayProperty {
	props, _ := schema["properties"].(map[string]any)
	out := make([]arrayProperty, 0, len(props))
	for name, p := range props {
		prop, _ := p.(map[string]any)
		if prop["type"] != "array" {
			continue
		}
		items, _ := prop["items"].(map[string]any)
		if items == nil {
			continue
		}
		out = append(out, arrayProperty{field: name, itemType: itemTypeName(items)})
	}
	// Sorted so an ambiguity error names them the same way twice, and
	// so a one-array spec does not depend on map order.
	sort.Slice(out, func(i, j int) bool { return out[i].field < out[j].field })
	return out
}

// itemTypeName is what the generated code calls one element: the
// component name for a $ref, the primitive otherwise.
func itemTypeName(items map[string]any) string {
	if ref, _ := items["$ref"].(string); ref != "" {
		return ref[strings.LastIndex(ref, "/")+1:]
	}
	t, _ := items["type"].(string)
	return t
}

// hasProperty reports whether the schema declares the property a
// pointer names, following it through nested objects: an API
// answering {meta: {nextCursor}} is ordinary, which is why next and
// items are pointers rather than names.
func hasProperty(doc map[string]any, schema map[string]any, pointer string) bool {
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		props, _ := deref(doc, schema)["properties"].(map[string]any)
		next, ok := props[part].(map[string]any)
		if !ok {
			return false
		}
		schema = next
	}
	return true
}

// requiredProperty reports whether the schema makes the property at
// pointer required.
//
// Walks to the property's PARENT and asks that schema, because
// required is a list on the object holding the property, not a flag
// on the property itself.
func requiredProperty(doc map[string]any, schema map[string]any, pointer string) bool {
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	for _, part := range parts[:len(parts)-1] {
		props, _ := deref(doc, schema)["properties"].(map[string]any)
		next, ok := props[part].(map[string]any)
		if !ok {
			return false
		}
		schema = next
	}
	required, _ := deref(doc, schema)["required"].([]any)
	for _, r := range required {
		if name, _ := r.(string); name == parts[len(parts)-1] {
			return true
		}
	}
	return false
}

// propertyNames are the schema's properties, sorted, for an error that
// says what IS there.
func propertyNames(schema map[string]any) string {
	props, _ := schema["properties"].(map[string]any)
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func fieldNames(arrays []arrayProperty) string {
	names := make([]string, 0, len(arrays))
	for _, a := range arrays {
		names = append(names, a.field)
	}
	return strings.Join(names, ", ")
}
