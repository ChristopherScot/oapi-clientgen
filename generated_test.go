package apiclient_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherScot/homelabctl/internal/apiclient"
	"github.com/ChristopherScot/homelabctl/internal/codegen"
)

// The Go client template ships 29 tests of its own, and until now they
// only ever ran in the repos that CONSUME a generated client. That is
// the wrong place twice over: the tests exercise this template rather
// than the consumer's service, and a consumer who hits a failure
// cannot fix it - the file says "do not edit", correctly.
//
// So render a client here and run them. Every breaker bug this package
// has shipped was found by a reviewer doing this by hand.
//
// The other tests in this package read rendered text. This one is the
// only thing that compiles it, so it is also the only thing that would
// notice the template no longer being valid Go.
func TestTheGeneratedTestsPass(t *testing.T) {
	if testing.Short() {
		t.Skip("renders a client, downloads modules and runs go test against it")
	}

	dir := t.TempDir()
	api := filepath.Join(dir, "api")
	if err := os.MkdirAll(api, 0o755); err != nil {
		t.Fatal(err)
	}

	// The template wraps ogen's Client and NewClient, so the package
	// only compiles with ogen's output beside it. The spec's shape is
	// irrelevant - every one of the 29 tests is transport-level and
	// none of them name an operation - so it is the smallest valid
	// document that makes ogen emit a client.
	spec := filepath.Join(dir, "openapi.yml")
	if err := os.WriteFile(spec, []byte(minimalSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "go", "run", codegen.OgenCommand(),
		"--target", api, "--clean", "--package", "api", spec)

	files, err := apiclient.Go(apiclient.Params{
		Name: "probe", Version: "1.0.0", Package: "api",
		ImportPath: "example.com/probe/api",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(api, f.Path), []byte(f.Body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/probe\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "go", "mod", "tidy")

	// Bare, not -race. A consumer's Makefile runs -race, which slows
	// execution enough to hide timing assumptions: the zero-valued
	// Breaker test failed 60 of 200 bare runs and never once under
	// -race. Running it the slow way here would make this harness
	// agree with itself and still let that reach a consumer.
	cmd := exec.Command("go", "test", "./api/", "-count=1")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("the generated tests failed:\n%s", indent(string(out)))
	}
}

// run fails the test rather than skipping it. A skip here would be
// indistinguishable from a pass, and this is the only test that
// compiles the template - so a toolchain or network problem has to be
// loud or the coverage silently disappears.
func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, indent(string(out)))
	}
}

func indent(s string) string {
	return "\t" + strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n\t")
}

const minimalSpec = `openapi: 3.0.0
info: {title: probe, version: 1.0.0}
paths:
  /thing:
    get:
      operationId: getThing
      responses:
        '200':
          description: ok
          content:
            application/json:
              schema: {type: string}
`
