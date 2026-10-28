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

	"github.com/ChristopherScot/oapi-clientgen/apiclient"
	"github.com/ChristopherScot/oapi-clientgen/codegen"
)

// The TypeScript counterpart of TestTheGeneratedTestsPass, and the
// answer to the asymmetry that prompted it: the Go client shipped
// tests of its own and the TypeScript one shipped none, though both carry the
// same breaker, the same Retry-After handling and the same rule about
// which methods may be retried. A behaviour guaranteed in one language
// and merely intended in the other is not guaranteed.
//
// Runs against hey-api's real output, not a stand-in: the adapter at
// the bottom of resilience.ts is the one place that names the
// generator, so a stub of it tests our call against our own guess at
// the signature. tsc checks it against the emitted types, which node
// cannot - it strips types rather than checking them.
//
// Parallel with the Go harness: nothing is shared between them, so
// the two builds overlap rather than add up.
func TestTheGeneratedTypeScriptTestsPass(t *testing.T) {
	if testing.Short() {
		t.Skip("renders a TypeScript client and runs node --test against it")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("no node")
	}
	requireNode(t, 24)
	t.Parallel()
	runTSClient(t)
}

// runTSClient generates the TypeScript client with hey-api, type-checks
// it, runs its shipped tests and returns the Retry-After verdicts they
// printed. See runGoClient on why it returns them.
func runTSClient(t *testing.T) map[string]string {
	t.Helper()

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

	// The same spec the Go harness renders, so both clients describe
	// one API. Its shape is irrelevant - every shipped test is
	// transport-level and none name an operation - but hey-api needs a
	// valid document to emit a client at all.
	if err := os.WriteFile(filepath.Join(dir, "openapi.yml"), []byte(minimalSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "openapi-ts.config.ts"),
		[]byte(codegen.HeyAPIConfigAtRoot("./openapi.yml")), 0o644); err != nil {
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

	// npx provisions hey-api and tsc into the same temporary install,
	// so the client is generated and checked by the versions codegen
	// pins rather than whatever the machine happens to have.
	gen := codegen.HeyAPICommand("openapi-ts.config.ts")
	mustRun(t, dir, gen[0], gen[1:]...)

	// What node cannot do. The adapter calls createClient,
	// createConfig and interceptors.error.use against hey-api's real
	// types; node strips the annotations and runs the code, so a call
	// that does not match the emitted signature still passes there.
	//
	// allowImportingTsExtensions because a published client's imports
	// carry '.ts' - see Params.Published - and noEmit because the
	// types are the only question here.
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(probeTSConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	// Installed rather than run through npx -p, because tsc resolves
	// "types" by walking node_modules up from the project - an npx
	// package lives outside that walk and is not found.
	//
	// @types/node is for the shipped test file's own imports:
	// node:test, node:assert/strict, and Timeout.unref. Major 24
	// matches the runtime floor requireNode checks.
	mustRun(t, dir, "npm", "install", "--no-audit", "--no-fund", "--silent",
		"typescript@"+codegen.TypescriptVersion, "@types/node@24")
	mustRun(t, dir, "npx", "tsc", "-p", "tsconfig.json")

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
		t.Fatalf("node --test in %s never exited; killed after %v\n%s",
			dir, 2*time.Minute, indent(out.String()))
	}
	if err != nil {
		t.Fatalf("node --test in %s: %v\n%s", dir, err, indent(out.String()))
	}
	return verdicts(t, "ts", out.String())
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

// skipLibCheck because the failure this guards against is in our call
// into the generated client, not inside node's own type definitions.
const probeTSConfig = `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "nodenext",
    "moduleResolution": "nodenext",
    "strict": true,
    "noEmit": true,
    "allowImportingTsExtensions": true,
    "skipLibCheck": true,
    "types": ["node"]
  },
  "include": ["*.ts", "generated/**/*.ts"]
}
`
