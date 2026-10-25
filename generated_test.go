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

// The Go client template ships tests of its own, and until now they
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
//
// It reaches the network on a cold module cache, and `go mod tidy`
// resolves ogen's transitive dependencies unpinned - so an upstream
// release can turn this red with no change here. Check upstream before
// bisecting the template.
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
	// irrelevant - every one of the shipped tests is transport-level
	// and none of them name an operation - so this is the smallest
	// valid document that makes ogen emit a client.
	spec := filepath.Join(dir, "openapi.yml")
	if err := os.WriteFile(spec, []byte(minimalSpec), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, dir, "go", "run", codegen.OgenCommand(),
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

	// No `go` directive: tidy writes whatever the dependency graph
	// demands, so a number here would be overwritten and would state
	// a floor it does not hold anyone to.
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, dir, "go", "mod", "tidy")

	// Bare, not -race. This is the configuration a consumer is least
	// likely to run - their Makefile runs -race - and -race is slow
	// enough to mask the timing assumptions the generated tests make.
	// See the zero-valued Breaker test in go_client_test.go.tmpl for
	// the case that proved it.
	//
	// -timeout so a hung inner run reports inside the outer test's own
	// 10m budget. Killed from above, it would be reaped without its
	// output, at the moment that output matters most.
	cmd := exec.Command("go", "test", "./api/", "-count=1", "-timeout=5m")
	cmd.Dir = dir
	cmd.Env = goflagsCleared()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("go test ./api/ in %s: %v\n%s", dir, err, indent(string(out)))
	}
}

// mustRun fails the test rather than skipping it. A skip here would be
// indistinguishable from a pass, and this is the only test that
// compiles the template - so a toolchain or network problem has to be
// loud or the coverage silently disappears.
func mustRun(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = goflagsCleared()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s in %s: %v\n%s",
			name, strings.Join(args, " "), dir, err, indent(string(out)))
	}
}

// goflagsCleared is the environment with GOFLAGS emptied. Every `go`
// command reads it fresh, so a developer or runner with GOFLAGS=-race
// exported would hand -race to the inner run - quietly throwing away
// the one thing it is here to cover, while still reporting green.
func goflagsCleared() []string {
	return append(os.Environ(), "GOFLAGS=")
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
