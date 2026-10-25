package apiclient_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherScot/homelabctl/internal/apiclient"
)

// The TypeScript counterpart of TestTheGeneratedTestsPass, and the
// answer to the asymmetry that prompted it: the Go client shipped
// tests of its own and the TypeScript one shipped none, though both carry the
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
	requireNode(t, 24)

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

	// --test-timeout so a wedged test is reported rather than waited
	// on, and a deadline on the process because the flag does not end
	// the run: node reports "test timed out", then sits on whatever
	// timer is still pending. Measured with the Retry-After cap
	// removed, which makes the client honour a 3600s Retry-After - the
	// test failed in a second and the process ran until something
	// killed it.
	//
	// Buffer the output ourselves instead of CombinedOutput, so that
	// when the deadline does fire we can still print what node said.
	// exec's own context handling kills the process and discards it,
	// which loses the diagnosis at the moment it is worth most.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "--test", "--test-timeout=30000", "resilience.test.ts")
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	if ctx.Err() != nil {
		t.Errorf("node --test in %s never exited; killed after %v\n%s",
			dir, 2*time.Minute, indent(out.String()))
		return
	}
	if err != nil {
		t.Errorf("node --test in %s: %v\n%s", dir, err, indent(out.String()))
	}
}

// requireNode fails unless node is at least major, which is the floor
// the generated services declare in their own package.json.
//
// Stated rather than assumed, because an older runtime does not fail
// honestly here. It runs the suite, reports zero failures, and marks
// five tests "cancelledByParent" - which reads as a bug in this
// harness rather than a toolchain that cannot run what it was given.
// CI had no setup-node step and silently used whatever the runner
// image shipped.
func requireNode(t *testing.T, major int) {
	t.Helper()
	out, err := exec.Command("node", "--version").Output()
	if err != nil {
		t.Fatalf("node --version: %v", err)
	}
	v := strings.TrimSpace(string(out))
	n, err := strconv.Atoi(strings.SplitN(strings.TrimPrefix(v, "v"), ".", 2)[0])
	if err != nil {
		t.Fatalf("could not read a major version from %q", v)
	}
	if n < major {
		t.Fatalf("node %s is too old: the generated client needs >=%d, "+
			"which is the engines floor its own package.json declares", v, major)
	}
}

const stubClient = `export const createClient = (c: any) => ({
  interceptors: { error: { use: (_f: any) => {} } },
  ...c,
})
export const createConfig = (c: any) => c
`
