package apispec

import (
	"encoding/json"
	"strings"
	"testing"
)

// doc is the shape of the real defect this package exists for: a
// component name used by both a parameter and a schema, which ogen
// turns into two Go types with one name.
func doc(t *testing.T) map[string]any {
	t.Helper()
	const src = `{
	  "openapi": "3.0.0",
	  "paths": {
	    "/meetings/{id}/registrants": {
	      "get": {
	        "operationId": "listRegistrants",
	        "parameters": [{"$ref": "#/components/parameters/RegistrantStatus"}]
	      },
	      "put": {
	        "operationId": "updateRegistrants",
	        "requestBody": {"content": {"application/json": {
	          "schema": {"$ref": "#/components/schemas/RegistrantStatus"}}}}
	      }
	    }
	  },
	  "components": {
	    "parameters": {
	      "RegistrantStatus": {"in": "query", "name": "status",
	        "schema": {"type": "string", "enum": ["pending", "approved"]}}
	    },
	    "schemas": {
	      "RegistrantStatus": {"type": "object",
	        "properties": {"action": {"type": "string"}}},
	      "RegistrantStatusDetail": {"type": "object"},
	      "UserSettings": {"type": "object", "properties": {
	        "auto_delete_days": {"type": "integer", "default": false}}}
	    }
	  }
	}`
	var d map[string]any
	if err := json.Unmarshal([]byte(src), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRenameComponentRetargetsRefs(t *testing.T) {
	d := doc(t)
	if _, err := Apply(d, []Op{{
		Op: "rename-component", From: "parameters/RegistrantStatus",
		To: "parameters/RegistrantStatusParam", Why: "collides with schemas/RegistrantStatus",
	}}); err != nil {
		t.Fatal(err)
	}

	params := d["components"].(map[string]any)["parameters"].(map[string]any)
	if _, stillThere := params["RegistrantStatus"]; stillThere {
		t.Error("old parameter name still present")
	}
	if _, renamed := params["RegistrantStatusParam"]; !renamed {
		t.Error("renamed parameter missing")
	}

	// The $ref has to follow, or the spec no longer resolves - which
	// fails at generation with an error naming neither the patch nor
	// the spec.
	out, _ := json.Marshal(d)
	if strings.Contains(string(out), `#/components/parameters/RegistrantStatus"`) {
		t.Error("a $ref still points at the old parameter name")
	}
	if !strings.Contains(string(out), `#/components/parameters/RegistrantStatusParam`) {
		t.Error("no $ref points at the new name")
	}

	// The schema of the same name is a DIFFERENT component and must be
	// untouched; renaming both would defeat the purpose.
	schemas := d["components"].(map[string]any)["schemas"].(map[string]any)
	if _, ok := schemas["RegistrantStatus"]; !ok {
		t.Error("the schema component was renamed too")
	}
}

// A prefix match would rewrite RegistrantStatusDetail while renaming
// RegistrantStatus, silently repointing an unrelated reference.
func TestRenameDoesNotMatchOnPrefix(t *testing.T) {
	d := doc(t)
	schemas := d["components"].(map[string]any)["schemas"].(map[string]any)
	schemas["Holder"] = map[string]any{"$ref": "#/components/schemas/RegistrantStatusDetail"}

	if _, err := Apply(d, []Op{{
		Op: "rename-component", From: "schemas/RegistrantStatus",
		To: "schemas/RegistrantBody", Why: "disambiguate",
	}}); err != nil {
		t.Fatal(err)
	}

	out, _ := json.Marshal(d)
	if !strings.Contains(string(out), "#/components/schemas/RegistrantStatusDetail") {
		t.Error("renaming RegistrantStatus also rewrote RegistrantStatusDetail")
	}
}

func TestSetCorrectsTypeDefaultMismatch(t *testing.T) {
	d := doc(t)
	if _, err := Apply(d, []Op{{
		Op:    "set",
		Path:  "/components/schemas/UserSettings/properties/auto_delete_days/default",
		Value: 0,
		Why:   "upstream declares type integer with default false",
	}}); err != nil {
		t.Fatal(err)
	}
	props := d["components"].(map[string]any)["schemas"].(map[string]any)["UserSettings"].(map[string]any)["properties"].(map[string]any)
	got := props["auto_delete_days"].(map[string]any)["default"]
	if got != 0 {
		t.Errorf("default = %v (%T), want 0", got, got)
	}
}

// A set that creates a key would turn a typo'd pointer into a field the
// upstream spec never had, which generates cleanly and is wrong.
//
// The LAST segment is the one that must not be created: a missing
// intermediate segment is already rejected while walking to the parent,
// so a test whose parent is also absent passes without ever reaching
// the guard it means to exercise.
func TestSetRefusesToCreate(t *testing.T) {
	d := doc(t)
	// auto_delete_days exists; "minimum" on it does not.
	_, err := Apply(d, []Op{{
		Op: "set", Path: "/components/schemas/UserSettings/properties/auto_delete_days/minimum",
		Value: 1, Why: "typo",
	}})
	if err == nil {
		t.Fatal("expected an error for a key that does not exist")
	}
	if !strings.Contains(err.Error(), "does not add one") {
		t.Errorf("error should say set does not create, got: %v", err)
	}
}

// Walking to a parent that does not exist is a different failure, and
// the message should name where the walk stopped.
func TestSetReportsMissingIntermediate(t *testing.T) {
	d := doc(t)
	_, err := Apply(d, []Op{{
		Op: "set", Path: "/components/schemas/UserSettings/properties/typo/default",
		Value: 0, Why: "typo",
	}})
	if err == nil || !strings.Contains(err.Error(), "stopped at") {
		t.Errorf("expected an error naming where the walk stopped, got: %v", err)
	}
}

// A patch nobody can justify cannot be retired: without the reason, a
// later reader cannot tell whether upstream still has the defect.
func TestWhyIsRequired(t *testing.T) {
	d := doc(t)
	_, err := Apply(d, []Op{{Op: "remove", Path: "/components/schemas/UserSettings"}})
	if err == nil || !strings.Contains(err.Error(), "why is required") {
		t.Errorf("expected a why-is-required error, got: %v", err)
	}
}

func TestRenameReportsMissingComponent(t *testing.T) {
	d := doc(t)
	_, err := Apply(d, []Op{{
		Op: "rename-component", From: "parameters/NotThere", To: "parameters/X", Why: "x",
	}})
	if err == nil || !strings.Contains(err.Error(), "not present") {
		t.Errorf("expected a not-present error, got: %v", err)
	}
}

// Renaming onto an existing name silently merges two components into
// one, which is the collision this op exists to resolve.
func TestRenameRefusesToClobber(t *testing.T) {
	d := doc(t)
	_, err := Apply(d, []Op{{
		Op: "rename-component", From: "schemas/RegistrantStatus",
		To: "schemas/RegistrantStatusDetail", Why: "x",
	}})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected an already-exists error, got: %v", err)
	}
}

func TestPointerEscapes(t *testing.T) {
	var d map[string]any
	if err := json.Unmarshal([]byte(`{"paths":{"/a~b/c":{"x":1}}}`), &d); err != nil {
		t.Fatal(err)
	}
	// ~0 is a literal ~, ~1 a literal /.
	if _, err := Apply(d, []Op{{
		Op: "set", Path: "/paths/~1a~0b~1c/x", Value: 2, Why: "escaping",
	}}); err != nil {
		t.Fatal(err)
	}
	if got := d["paths"].(map[string]any)["/a~b/c"].(map[string]any)["x"]; got != 2 {
		t.Errorf("x = %v, want 2", got)
	}
}

func TestUnknownOpIsNamed(t *testing.T) {
	d := doc(t)
	_, err := Apply(d, []Op{{Op: "patch-everything", Why: "x"}})
	if err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Errorf("expected an unknown-op error, got: %v", err)
	}
}

// `null`, an empty file and a comment-only file all decode successfully
// to a nil map. Returned as a document, that reaches the generator as
// four bytes of `null` and fails there, naming a temp file instead of
// the spec.
func TestDecodeRejectsEmptyDocuments(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"json null", "null"},
		{"empty", ""},
		{"comments only", "# nothing here\n"},
		{"whitespace", "   \n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Decode([]byte(tc.body))
			if err == nil {
				t.Fatalf("Decode(%q) = %v, want an error", tc.body, doc)
			}
			if !strings.Contains(err.Error(), "empty") {
				t.Errorf("error should say the spec is empty, got: %v", err)
			}
		})
	}
}

func TestDecodeAcceptsBothFormats(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"json", `{"openapi":"3.0.0"}`},
		{"yaml", "openapi: 3.0.0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Decode([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if doc["openapi"] != "3.0.0" {
				t.Errorf("openapi = %v, want 3.0.0", doc["openapi"])
			}
		})
	}
}

// Apply mutates in place and does not unwind, so a caller that
// continues after an error would generate from a half-patched
// document. Pinned because the doc comment promises it.
func TestApplyLeavesDocumentPartiallyModifiedOnError(t *testing.T) {
	d := doc(t)
	_, err := Apply(d, []Op{
		{Op: "rename-component", From: "parameters/RegistrantStatus",
			To: "parameters/RegistrantStatusParam", Why: "first op succeeds"},
		{Op: "set", Path: "/nope/missing", Value: 1, Why: "second op fails"},
	})
	if err == nil {
		t.Fatal("expected the second op to fail")
	}
	params := d["components"].(map[string]any)["parameters"].(map[string]any)
	if _, ok := params["RegistrantStatusParam"]; !ok {
		t.Error("the first op was unwound; Apply documents that it is not")
	}
}

// The progress line must distinguish 1 from "1" - these patches exist
// to correct values whose type is wrong - while not dumping a
// deduplicated enum twice.
func TestSetProgressLineShowsTypeAndSummarisesLists(t *testing.T) {
	var d map[string]any
	if err := json.Unmarshal([]byte(`{"a":{"n":1,"e":["x","x","y"]}}`), &d); err != nil {
		t.Fatal(err)
	}
	notes, err := Apply(d, []Op{
		{Op: "set", Path: "/a/n", Value: "1", Why: "type mismatch"},
		{Op: "set", Path: "/a/e", Value: []any{"x", "y"}, Why: "duplicate enum value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notes[0], `1 -> "1"`) {
		t.Errorf("a type change must be visible, got: %s", notes[0])
	}
	if !strings.Contains(notes[1], "3 value(s) -> 2 value(s)") {
		t.Errorf("a list should be summarised, got: %s", notes[1])
	}
}
