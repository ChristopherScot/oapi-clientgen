package generate

import (
	"fmt"
	"os"

	"github.com/ChristopherScot/oapi-gen/apispec"
	"gopkg.in/yaml.v3"
)

// stageSpec documents the document's constraints and writes it where
// a generator can read it, returning the path and a cleanup.
//
// A real file on disk, because both generators take a path, and a
// temp one because what they read is not what the author wrote: the
// constraints a spec enforces are appended to the descriptions that
// carry them, so they reach the doc comment a caller hovers.
//
// Shared by both paths. They differ in how they come by the document
// - one patches it, one reads it as the service's own - and agree
// from here on, which is what keeps them generating from the same
// bytes.
func stageSpec(raw []byte, name string, logf func(string, ...any)) (path string, cleanup func(), err error) {
	// A node, not a map: a map sorts its keys, so re-encoding through
	// one hands the generators every field in alphabetical order and
	// reorders every struct they emit. YAML is a superset of JSON, so
	// this reads a spec in either.
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", nil, fmt.Errorf("reading the spec: %w", err)
	}

	if n := apispec.AnnotateNode(&doc); n > 0 && logf != nil {
		logf("  documented constraints on %d field(s)\n", n)
	}

	encoded, err := yaml.Marshal(&doc)
	if err != nil {
		return "", nil, fmt.Errorf("re-encoding the spec: %w", err)
	}

	tmp, err := os.CreateTemp("", "oapi-gen-"+name+"-*.yml")
	if err != nil {
		return "", nil, fmt.Errorf("staging the spec: %w", err)
	}
	// Close as well as Remove: Remove unlinks the path and leaves the
	// descriptor open. Nothing useful to do if either fails.
	cleanup = func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}
	if _, err := tmp.Write(encoded); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("staging the spec: %w", err)
	}
	// Checked, unlike the Close in cleanup: a write can succeed and
	// still fail to flush, handing the generator a truncated spec.
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("writing the staged spec: %w", err)
	}
	return tmp.Name(), cleanup, nil
}
