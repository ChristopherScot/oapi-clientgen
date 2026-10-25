package apiclient_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ChristopherScot/homelabctl/internal/apiclient"
)

// The TypeScript counterpart of TestTheGeneratedTestsPass, and the
// answer to the asymmetry that prompted it: the Go client shipped 34
// tests and the TypeScript one shipped none, though both carry the
// same breaker, the same Retry-After handling and the same rule about
// which methods may be retried. A behaviour guaranteed in one language
// and merely intended in the other is not guaranteed.
//
// Cheaper than the Go harness: node strips the types itself (the
// service templates set an engines floor of 24), so there is no build
// step, no install, and nothing to download. The whole run is under a
// second.
func TestTheGeneratedTypeScriptTestsPass(t *testing.T) {
	if testing.Short() {
		t.Skip("renders a TypeScript client and runs node --test against it")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node")
	}

	dir := t.TempDir()

	// Published, because that variant names its imports the way Node
	// resolves them. The unpublished one emits extensionless relative
	// imports, which its consumer resolves through a bundler
	// (moduleResolution: "Bundler") and node does not. The resilience
	// logic under test is identical either way - only the import
	// specifiers differ.
	p := apiclient.Params{
		Name: "probe", Version: "1.0.0", ImportPath: "probe-client", Published: true,
	}
	client, err := apiclient.TypeScript(p)
	if err != nil {
		t.Fatal(err)
	}
	tests, err := apiclient.TypeScriptTests(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range append(client, tests...) {
		if err := os.WriteFile(filepath.Join(dir, f.Path), []byte(f.Body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Stands in for hey-api's output. The tests are transport-level
	// and never call an operation; this exists only because
	// resilience.ts imports the generated client to build the adapter
	// at the bottom of the file.
	gen := filepath.Join(dir, "generated", "client")
	if err := os.MkdirAll(gen, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gen, "index.ts"), []byte(stubClient), 0o644); err != nil {
		t.Fatal(err)
	}

	// type: module so node reads the .ts files as ESM, and a version
	// for the Client-Version header a published client reads from here.
	pkg, err := json.Marshal(map[string]any{
		"name": "probe-client", "version": "1.0.0", "type": "module",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), pkg, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("node", "--test", "resilience.test.ts")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("node --test in %s: %v\n%s", dir, err, indent(string(out)))
	}
}

const stubClient = `export const createClient = (c: any) => ({
  interceptors: { error: { use: (_f: any) => {} } },
  ...c,
})
export const createConfig = (c: any) => c
`
