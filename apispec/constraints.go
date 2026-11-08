package apispec

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// constraintKeys are the validation keywords a caller cannot see any
// other way, in the order they are appended.
//
// These reject a request. A value past maximum or failing pattern is
// a 400 the caller gets at runtime, from a rule enforced in generated
// code they are told not to read: ogen holds them in a validator
// table (`Regex: regexMap[...]`) and hey-api drops them entirely.
// Neither reaches a type, because Go has no refinement types and the
// zero value defeats a constructor - Clever's wag generator lands on
// the same shape independently.
//
// default, enum and format are deliberately absent: each already
// shapes the generated type, so a caller sees them without help.
var constraintKeys = []string{
	"minimum", "maximum",
	"minLength", "maxLength",
	"pattern",
}

// AnnotateNode appends a schema's validation constraints to the
// description that carries it, so they arrive in the doc comment a
// caller hovers. Reports how many descriptions it touched.
//
// A yaml.Node rather than a map, because a map sorts its keys: the
// document is re-encoded on the way to the generators, and a map
// would hand ogen the spec's fields in alphabetical order, reordering
// every struct it emits. A node keeps the order the author wrote, and
// keeps JSON documents in JSON - YAML is a superset, so one path
// reads both.
func AnnotateNode(root *yaml.Node) int {
	doc := root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	touched := 0
	for _, item := range mapValues(child(doc, "paths")) {
		for _, op := range mapValues(item) {
			touched += annotateParameters(op)
			touched += annotateContent(child(child(op, "requestBody"), "content"))
			for _, resp := range mapValues(child(op, "responses")) {
				touched += annotateContent(child(resp, "content"))
			}
		}
		// Parameters shared by every method on the path.
		touched += annotateParameters(item)
	}
	// The same positions again under components, where a spec puts
	// what it reuses. A $ref into components/schemas is documented
	// because the walk reaches its target here; one into
	// components/parameters would not be, which is why these are not
	// only the inline cases.
	components := child(doc, "components")
	for _, schema := range mapValues(child(components, "schemas")) {
		touched += annotateSchema(schema)
	}
	for _, key := range []string{"parameters", "headers"} {
		for _, param := range mapValues(child(components, key)) {
			touched += annotateParameter(param)
		}
	}
	for _, key := range []string{"requestBodies", "responses"} {
		for _, body := range mapValues(child(components, key)) {
			touched += annotateContent(child(body, "content"))
		}
	}
	return touched
}

// annotateParameters documents the parameters a path or operation
// declares. A parameter's description sits beside its schema rather
// than inside it, and that is the one a generator renders.
func annotateParameters(node *yaml.Node) int {
	touched := 0
	for _, param := range seqValues(child(node, "parameters")) {
		touched += annotateParameter(param)
	}
	return touched
}

// annotateParameter documents one parameter or header, whose
// description sits beside its schema rather than inside it.
//
// The description carries the schema's own constraints;
// annotateChildren covers anything nested inside, without documenting
// the schema a second time.
func annotateParameter(param *yaml.Node) int {
	schema := child(param, "schema")
	if schema == nil {
		return 0
	}
	return documentConstraints(param, schema) + annotateChildren(schema)
}

func annotateContent(content *yaml.Node) int {
	touched := 0
	for _, media := range mapValues(content) {
		if schema := child(media, "schema"); schema != nil {
			touched += annotateSchema(schema)
		}
	}
	return touched
}

// annotateSchema documents a schema and the schemas inside it.
func annotateSchema(schema *yaml.Node) int {
	return documentConstraints(schema, schema) + annotateChildren(schema)
}

// annotateChildren documents the schemas nested inside this one,
// through the keywords that hold a schema.
func annotateChildren(schema *yaml.Node) int {
	touched := 0
	for _, key := range []string{"items", "additionalProperties", "not"} {
		if sub := child(schema, key); sub != nil {
			touched += annotateSchema(sub)
		}
	}
	for _, key := range []string{"properties", "patternProperties"} {
		for _, sub := range mapValues(child(schema, key)) {
			touched += annotateSchema(sub)
		}
	}
	for _, key := range []string{"allOf", "oneOf", "anyOf", "prefixItems"} {
		for _, sub := range seqValues(child(schema, key)) {
			touched += annotateSchema(sub)
		}
	}
	return touched
}

// documentConstraints appends schema's constraints to owner's
// description, and reports whether it wrote one.
//
// Two nodes because a parameter documents its schema from outside it:
// writing onto the schema there would put text where no description
// was and nothing renders one.
func documentConstraints(owner, schema *yaml.Node) int {
	text := constraintText(schema)
	if text == "" {
		return 0
	}
	desc := ""
	if d := child(owner, "description"); d != nil {
		desc = d.Value
	}
	if strings.Contains(desc, text) {
		return 0
	}
	if desc != "" {
		text = strings.TrimRight(desc, "\n ") + "\n\n" + text
	}
	setString(owner, "description", text)
	return 1
}

// constraintText is the constraints a schema declares, as the spec's
// own keywords, or "" when it declares none.
//
// Key/value rather than a sentence: nothing to phrase, nothing to
// pluralise, and maxLength reads the same way as maximum.
func constraintText(schema *yaml.Node) string {
	var parts []string
	for _, key := range constraintKeys {
		v := child(schema, key)
		// Scalars only. A mapping or sequence where a constraint
		// belongs is not valid OpenAPI, and its Value is "" - which
		// would render "maximum: " and document nothing.
		if v == nil || v.Kind != yaml.ScalarNode {
			continue
		}
		parts = append(parts, key+": "+commentSafe(key, v.Value))
	}
	return strings.Join(parts, ", ")
}

// commentSafe is a constraint's value as the spec wrote it, less
// anything that would break the comment it lands in.
//
// A regex may legally contain */, which ends a TypeScript block
// comment early: tsc reports "TS1161: Unterminated regular expression
// literal" and the generated file is truncated at that line.
// ^[a-z]*/$ is enough to do it.
//
// Escaped to *\/ rather than split with an invisible character,
// because \/ matches what / matches - a reader who copies the pattern
// out of the comment gets one that still works.
func commentSafe(key, value string) string {
	if key == "pattern" {
		return strings.ReplaceAll(value, "*/", `*\/`)
	}
	return value
}

// resolve follows an alias to the node it names.
//
// A spec that defines a schema once with an anchor and uses it by
// alias holds an AliasNode where a mapping is expected, which every
// accessor below would otherwise skip - documenting the constraint on
// whichever use the walk met first, or on none of them when the
// anchor sits somewhere the walk does not go.
//
// Visiting a shared node twice is safe: documentConstraints finds its
// own text already there and writes nothing.
func resolve(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// child is the value of key in a mapping node, or nil.
func child(n *yaml.Node, key string) *yaml.Node {
	n = resolve(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return resolve(n.Content[i+1])
		}
	}
	return nil
}

// mapValues are a mapping node's values.
func mapValues(n *yaml.Node) []*yaml.Node {
	n = resolve(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	out := make([]*yaml.Node, 0, len(n.Content)/2)
	// i+1 < len, like the accessors around it: a mapping the parser
	// built always has an even Content, and a hand-built one with a
	// dangling key would otherwise come back as a value.
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, resolve(n.Content[i+1]))
	}
	return out
}

// seqValues are a sequence node's elements.
func seqValues(n *yaml.Node) []*yaml.Node {
	n = resolve(n)
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]*yaml.Node, 0, len(n.Content))
	for _, c := range n.Content {
		out = append(out, resolve(c))
	}
	return out
}

// setString writes key on a mapping node, in place when it is already
// there and appended when it is not, so the keys around it keep their
// order.
func setString(n *yaml.Node, key, value string) {
	n = resolve(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content[i+1].SetString(value)
			return
		}
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	v := &yaml.Node{Kind: yaml.ScalarNode}
	v.SetString(value)
	n.Content = append(n.Content, k, v)
}
