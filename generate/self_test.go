package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The config a service's own spec gets differs from a third party's in
// one way that matters, and it is the way a reader is most likely to
// assume is an oversight.
func TestAServiceOwnsItsSpecSoNothingIsSkipped(t *testing.T) {
	if strings.Contains(selfOgenConfig, "ignore_not_implemented") {
		t.Error("the self config skips unsupported operations; a spec you own is fixed at the source, " +
			"so skipping hides a problem you can correct")
	}
	if !strings.Contains(thirdPartyOgenConfig(), "ignore_not_implemented") {
		t.Error("the third-party config does NOT skip them; one unsupported corner then means no client at all")
	}
	for _, cfg := range []string{selfOgenConfig, thirdPartyOgenConfig()} {
		if !strings.Contains(cfg, "ogen/otel") {
			t.Error("otel is not disabled; it costs a consumer 13 modules for spans nothing collects")
		}
	}
}

// --support-only is the migration path: a service still running its
// own ogen gets the resilience layer and nothing else.
func TestSupportOnlyLeavesTheGeneratorsAlone(t *testing.T) {
	dir := t.TempDir()
	api := filepath.Join(dir, "api")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatal(err)
	}
	// Content ogen would never write. It survives only if ogen did
	// not run: --clean deletes what it does not recognise, and a real
	// run would overwrite this with generated types.
	sentinel := filepath.Join(api, "oas_schemas_gen.go")
	const notOgens = "package api\n\n// written by the test, not by ogen\n"
	if err := os.WriteFile(sentinel, []byte(notOgens), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(dir, "openapi.yml")
	if err := os.WriteFile(spec, []byte(selfTestSpec), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := (Generator{}).Self(dir, "openapi.yml", SelfOptions{
		GoOut: "api", Package: "api", SupportOnly: true,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("ogen's output did not survive --support-only: %v", err)
	}
	if string(got) != notOgens {
		t.Error("oas_schemas_gen.go was rewritten, so the generators ran under --support-only")
	}
	for _, want := range []string{"client.go", "paging.go", "paging_ops.go"} {
		if _, err := os.Stat(filepath.Join(api, want)); err != nil {
			t.Errorf("%s was not written: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(api, "ogen.yml")); err == nil {
		t.Error("an ogen.yml was written; the self path composes its config rather than leaving one to drift")
	}
}

// The name reaches the generated doc comments, and defaults to the
// spec's own title rather than something a caller has to repeat.
func TestTheNameComesFromTheSpecWhenNobodySaysOtherwise(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openapi.yml"), []byte(selfTestSpec), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := (Generator{}).Self(dir, "openapi.yml", SelfOptions{
		GoOut: "api", Package: "api", SupportOnly: true,
	}); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(filepath.Join(dir, "api", "client.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "probe-service") {
		t.Error("the spec's info.title did not reach the generated client")
	}
}

const selfTestSpec = `openapi: 3.0.0
info: {title: probe-service, version: 2.1.0}
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
