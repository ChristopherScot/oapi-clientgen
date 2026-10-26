package generate_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherScot/oapiclient-gen/generate"
)

const petstore = `openapi: 3.0.0
info: {title: petstore, version: 2.1.0}
paths:
  /pets:
    get:
      operationId: listPets
      responses:
        '200': {description: ok}
`

// Spec is relative to dir, so this returns the bare name.
func specIn(t *testing.T, dir string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "petstore.yml"), []byte(petstore), 0o644); err != nil {
		t.Fatal(err)
	}
	return "petstore.yml"
}

// A library that writes to os.Stdout cannot be embedded in anything
// that has its own idea of output - a server, a test, another CLI.
func TestProgressGoesWhereTheCallerSays(t *testing.T) {
	dir := t.TempDir()
	var log bytes.Buffer
	err := generate.Generator{Log: &log}.Generate(dir, "pet", generate.Client{
		Spec:      specIn(t, dir),
		Out:       "./petapi",
		Languages: []string{"go"},
	}, false)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(log.String(), "pet: spec from") {
		t.Errorf("progress did not reach the caller's writer:\n%s", log.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "petapi", "client.go")); err != nil {
		t.Errorf("no client was written: %v", err)
	}
}

// The name has to be in the error. --spec calls Generate directly,
// with no manifest loop to wrap it.
func TestAMissingOutNamesTheClient(t *testing.T) {
	err := generate.Generate(".", "acme", generate.Client{Spec: "x.yml"}, false)
	if err == nil {
		t.Fatal("no out, but no error")
	}
	if !strings.Contains(err.Error(), "acme") {
		t.Errorf("error does not name the client: %v", err)
	}
}
