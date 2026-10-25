// Package apispec prepares a third-party OpenAPI spec for code
// generation.
//
// A spec you own is kept generatable by the CI that generates from it. A
// spec you merely consume is not: it is published by someone with no
// reason to care whether ogen accepts it, and it changes when they
// change it. A published spec may be Swagger 2.0, or hold one component
// name used for two incompatible types, or repeat a value in an enum -
// none of which its consumers can fix at the source.
//
// So the spec is treated as INPUT, and the edits that make it
// generatable are committed beside it as data. Reapplying them is part
// of generating, which is what makes a regeneration against a refreshed
// spec reproducible rather than an afternoon of rediscovery.
package apispec

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Op is one edit to a spec.
//
// The set is deliberately small and declarative: these describe WHAT is
// wrong with the document, so a reader can tell from the patch file what
// the upstream spec got wrong. A general-purpose "run this script"
// escape hatch would cover more and explain nothing.
type Op struct {
	// Op is the kind of edit: "set", "remove", "rename-component".
	Op string `yaml:"op" json:"op"`

	// Path is a JSON Pointer (RFC 6901) into the document, for set and
	// remove: /components/schemas/Foo/properties/bar/default
	Path string `yaml:"path,omitempty" json:"path,omitempty"`

	// Value is the replacement, for set. Any JSON value.
	Value any `yaml:"value,omitempty" json:"value,omitempty"`

	// From and To name a component for rename-component, as
	// "<section>/<name>" - e.g. parameters/RegistrantStatus. Every
	// $ref pointing at From is rewritten.
	//
	// This is its own op rather than a set plus a find-and-replace
	// because the rename has to reach every reference to stay valid,
	// and a patch file that silently left danglers would fail at
	// generation with an error naming neither the patch nor the spec.
	From string `yaml:"from,omitempty" json:"from,omitempty"`
	To   string `yaml:"to,omitempty" json:"to,omitempty"`

	// Why records what upstream defect this works around. Required:
	// a patch whose reason is not written down cannot be retired,
	// because nobody can tell whether the upstream spec still has
	// the problem.
	Why string `yaml:"why" json:"why"`
}

// Apply runs ops against a decoded spec document, in order, and reports
// what each one did.
//
// Order matters and is the author's: a rename followed by a set on the
// renamed path is meaningful, and resolving that for them would be
// guesswork.
//
// On error the document is left PARTIALLY MODIFIED - ops before the
// failing one have already been applied. Callers must discard it rather
// than generate from it. Not unwound, because the only caller exits on
// error; a caller that wants to retry or preview must copy first.
func Apply(doc map[string]any, ops []Op) ([]string, error) {
	var applied []string
	for i, op := range ops {
		if op.Why == "" {
			return nil, fmt.Errorf("patch %d (%s): why is required - record what upstream defect this works around", i, op.Op)
		}
		var (
			note string
			err  error
		)
		switch op.Op {
		case "set":
			note, err = applySet(doc, op)
		case "remove":
			note, err = applyRemove(doc, op)
		case "rename-component":
			note, err = applyRename(doc, op)
		default:
			err = fmt.Errorf("unknown op %q; known: set, remove, rename-component", op.Op)
		}
		if err != nil {
			return nil, fmt.Errorf("patch %d (%s): %w", i, op.Op, err)
		}
		applied = append(applied, note)
	}
	return applied, nil
}

// applySet writes Value at Path, which must already exist.
//
// Creating missing paths is deliberately not supported: a patch that
// silently creates what it meant to correct is how a typo'd pointer
// becomes a field the upstream spec never had.
func applySet(doc map[string]any, op Op) (string, error) {
	parent, key, err := resolveParent(doc, op.Path)
	if err != nil {
		return "", err
	}
	old, present := parent[key]
	if !present {
		return "", fmt.Errorf("%s does not exist; set corrects a value, it does not add one", op.Path)
	}
	parent[key] = op.Value
	return fmt.Sprintf("set %s: %s -> %s", op.Path, describe(old), describe(op.Value)), nil
}

// describe renders a patched value for the progress line.
//
// %#v for scalars, because these patches exist to correct a value whose
// TYPE is wrong and %v renders 1 and "1" identically - a patch that
// changed nothing reported "1 -> 1" and read like it had worked.
//
// Lists are summarised instead: a deduplicated 10-value enum printed in
// full twice is three lines of noise that buries the other patches in
// the same run.
func describe(v any) string {
	if list, ok := v.([]any); ok {
		return fmt.Sprintf("%d value(s)", len(list))
	}
	return fmt.Sprintf("%#v", v)
}

func applyRemove(doc map[string]any, op Op) (string, error) {
	parent, key, err := resolveParent(doc, op.Path)
	if err != nil {
		return "", err
	}
	if _, present := parent[key]; !present {
		return "", fmt.Errorf("%s does not exist", op.Path)
	}
	delete(parent, key)
	return "removed " + op.Path, nil
}

// applyRename moves a component and repoints every $ref to it.
func applyRename(doc map[string]any, op Op) (string, error) {
	fromSection, fromName, err := splitComponent(op.From)
	if err != nil {
		return "", fmt.Errorf("from: %w", err)
	}
	toSection, toName, err := splitComponent(op.To)
	if err != nil {
		return "", fmt.Errorf("to: %w", err)
	}
	if fromSection != toSection {
		return "", fmt.Errorf("cannot move between component sections (%s -> %s)", fromSection, toSection)
	}

	components, _ := doc["components"].(map[string]any)
	section, _ := components[fromSection].(map[string]any)
	if section == nil {
		return "", fmt.Errorf("components/%s not present", fromSection)
	}
	val, present := section[fromName]
	if !present {
		return "", fmt.Errorf("components/%s/%s not present", fromSection, fromName)
	}
	if _, taken := section[toName]; taken {
		return "", fmt.Errorf("components/%s/%s already exists", toSection, toName)
	}
	delete(section, fromName)
	section[toName] = val

	oldRef := "#/components/" + fromSection + "/" + fromName
	newRef := "#/components/" + toSection + "/" + toName
	n := retargetRefs(doc, oldRef, newRef)
	return fmt.Sprintf("renamed components/%s/%s -> %s (%d ref(s))", fromSection, fromName, toName, n), nil
}

// retargetRefs rewrites every $ref equal to oldRef, returning the count.
//
// Exact match only. A $ref is a whole pointer, so a prefix match would
// also rewrite #/components/schemas/RegistrantStatusDetail when renaming
// RegistrantStatus.
func retargetRefs(node any, oldRef, newRef string) int {
	n := 0
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			if k == "$ref" {
				if s, ok := child.(string); ok && s == oldRef {
					v[k] = newRef
					n++
				}
				continue
			}
			n += retargetRefs(child, oldRef, newRef)
		}
	case []any:
		for _, child := range v {
			n += retargetRefs(child, oldRef, newRef)
		}
	}
	return n
}

// splitComponent parses "<section>/<name>", e.g. parameters/PageSize.
func splitComponent(s string) (section, name string, err error) {
	section, name, found := strings.Cut(s, "/")
	if !found || section == "" || name == "" {
		return "", "", fmt.Errorf("%q is not <section>/<name>, e.g. parameters/PageSize", s)
	}
	return section, name, nil
}

// resolveParent walks a JSON Pointer to the CONTAINER of its last
// segment, so the caller can set or delete that key.
//
// Arrays are traversed but not modified: an index is a position in a
// document someone else publishes, and inserting or deleting one shifts
// every later index - including the ones named by other patches in the
// same file.
func resolveParent(doc map[string]any, pointer string) (map[string]any, string, error) {
	if pointer == "" || !strings.HasPrefix(pointer, "/") {
		return nil, "", fmt.Errorf("path %q must be a JSON Pointer beginning with /", pointer)
	}
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	for i := range parts {
		parts[i] = unescapePointer(parts[i])
	}

	var cur any = doc
	for i, part := range parts[:len(parts)-1] {
		switch node := cur.(type) {
		case map[string]any:
			next, present := node[part]
			if !present {
				return nil, "", fmt.Errorf("%s does not exist (stopped at /%s)", pointer, strings.Join(parts[:i+1], "/"))
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, "", fmt.Errorf("%s: %q is not a valid index into an array of %d", pointer, part, len(node))
			}
			cur = node[idx]
		default:
			return nil, "", fmt.Errorf("%s: /%s is a scalar, not a container", pointer, strings.Join(parts[:i+1], "/"))
		}
	}

	parent, ok := cur.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("%s: parent is %T, not an object", pointer, cur)
	}
	return parent, parts[len(parts)-1], nil
}

// unescapePointer decodes RFC 6901's escapes. ~1 before ~0, or "~01"
// decodes to "/" instead of "~1".
func unescapePointer(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	return strings.ReplaceAll(s, "~0", "~")
}

// Decode reads a spec as a generic document. JSON and YAML both decode
// here: a YAML 1.2 document is JSON-compatible, and specs ship as both.
func Decode(b []byte) (map[string]any, error) {
	var doc map[string]any
	if json.Valid(b) {
		if err := json.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("parsing spec as JSON: %w", err)
		}
	} else if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parsing spec as YAML: %w", err)
	}
	// `null`, an empty file and a comment-only file all decode
	// successfully to a nil map. Returning that as a document sends
	// four bytes of `null` to the generator, which fails three steps
	// later with a stack trace naming a temp file - the opaque failure
	// this package exists to turn into a sentence.
	if doc == nil {
		return nil, fmt.Errorf("spec is empty: expected a JSON or YAML object")
	}
	return doc, nil
}
