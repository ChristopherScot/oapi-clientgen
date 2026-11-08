package codegen

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ChristopherScot/oapi-clientgen/apiclient"
)

// configPaths pulls input and output.path back out of a generated
// config, so a test can ask where they point rather than whether the
// string we passed came back.
func configPaths(t *testing.T, cfg string) (input, output string) {
	t.Helper()
	in := regexp.MustCompile(`input: "([^"]+)"`).FindStringSubmatch(cfg)
	out := regexp.MustCompile(`path: "([^"]+)"`).FindStringSubmatch(cfg)
	if in == nil || out == nil {
		t.Fatalf("config does not name both paths:\n%s", cfg)
	}
	return in[1], out[1]
}

// cwdOf answers the question the whole file is about: which directory
// is the generator actually in when it reads these paths? npx moves
// to the nearest package.json, so it is NOT the directory we invoked
// from.
//
// The scaffolded client has its own package.json, so npx stays there.
// A vendored client has none, so npx walks up to the host app.
func TestVendoredConfigPathsResolveFromTheHostApp(t *testing.T) {
	const hostApp = "/repo/app"
	clientDir := filepath.Join(hostApp, "src/server/vendor/zoomAPI/users")
	spec := filepath.Join(hostApp, "src/server/vendor/zoomAPI/specs/zoom-users.json")

	cfgPath, cfg, err := HeyAPIConfigNestedIn(clientDir, spec)
	if err != nil {
		t.Fatal(err)
	}
	input, output := configPaths(t, cfg)

	// Resolved from where npx will be - the host app, not clientDir.
	if got := resolveFrom(hostApp, output); got != filepath.Join(clientDir, "generated") {
		t.Errorf("output resolves to %s, not into the client\n(this is how a client got written to the app root)", got)
	}
	if got := resolveFrom(hostApp, input); got != spec {
		t.Errorf("input resolves to %s, not to the spec", got)
	}
	// The -f argument is read from there too.
	if got := resolveFrom(hostApp, cfgPath); got != filepath.Join(clientDir, "openapi-ts.config.ts") {
		t.Errorf("config path resolves to %s, not to the file we wrote", got)
	}
}

// A published client's relative imports carry .ts, because its own
// tsconfig rewrites them to .js on the way into dist. A vendored one
// must not: .ts is TS5097 in a host repo that never opted into
// allowImportingTsExtensions.
//
// The two configs are near enough alike to reach for the wrong one,
// and reaching for the vendored one on a published client stripped
// the extensions and would have shipped a package whose imports
// resolve to nothing.
func TestOnlyThePublishedConfigKeepsTSExtensions(t *testing.T) {
	if !strings.Contains(HeyAPIConfigAtRoot("../../openapi.yml"), "importFileExtension: 'ts'") {
		t.Error("the published config dropped importFileExtension; its relative imports " +
			"lose .ts and tsc cannot rewrite them into dist/")
	}

	_, vendored, err := HeyAPIConfigNestedIn(t.TempDir(), "openapi.yml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(vendored, "importFileExtension") {
		t.Error("the vendored config gained importFileExtension; .ts is TS5097 in a " +
			"host repo that did not ask for it")
	}
}

// The scaffolded client keeps RELATIVE paths - the file is committed
// and read on other machines - and they work because npx stops at the
// client's own package.json.
func TestScaffoldedConfigPathsResolveFromTheClientItself(t *testing.T) {
	const clientDir = "/repo/service/clients/ts"

	cfg := HeyAPIConfigAtRoot("../../openapi.yml")
	input, output := configPaths(t, cfg)

	if filepath.IsAbs(input) || filepath.IsAbs(output) {
		t.Fatalf("committed config must not carry absolute paths from the scaffolding machine:\n%s", cfg)
	}
	if got := resolveFrom(clientDir, output); got != filepath.Join(clientDir, "generated") {
		t.Errorf("output resolves to %s, not into the client", got)
	}
	if got := resolveFrom(clientDir, input); got != "/repo/service/openapi.yml" {
		t.Errorf("input resolves to %s, not to the service's spec", got)
	}
}

func resolveFrom(cwd, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(cwd, p)
}

// This config is the file that carried the third wording.
//
// The notice said a file was generated without naming what would
// overwrite it, and nothing here noticed, because this is the one
// consumer of the header with no test. The templates have one; so
// does MarkGenerated's. Find the bug once.
func TestBothConfigsCarryTheSharedHeader(t *testing.T) {
	_, nested, err := HeyAPIConfigNestedIn("/repo/app/clients/zoom", "/repo/app/zoom.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, cfg string }{
		{"AtRoot", HeyAPIConfigAtRoot("../../openapi.yml")},
		{"NestedIn", nested},
	} {
		if !strings.HasPrefix(tc.cfg, apiclient.Header("//")) {
			t.Errorf("%s does not start with the shared header; it has its own copy "+
				"of the wording:\n%s", tc.name, tc.cfg)
		}
		if !apiclient.WroteIt([]byte(tc.cfg)) {
			t.Errorf("%s is not claimed by WroteIt, so it could never be retired", tc.name)
		}
	}
}

// @latest breaks a build on the day an upstream publishes, in a file
// nobody touched. Every generator is pinned to an exact version, and
// this is what says so when a fifth one is added.
func TestEveryGeneratorVersionIsPinnedExactly(t *testing.T) {
	for name, v := range map[string]string{
		"ogen":            OgenVersion,
		"hey-api":         HeyAPIVersion,
		"typescript":      TypescriptVersion,
		"swagger2openapi": Swagger2OpenAPIVersion,
	} {
		if v == "" || v == "latest" || strings.ContainsAny(v, "^~*") {
			t.Errorf("%s is pinned to %q, which is a range rather than a version", name, v)
		}
	}
}

// Both callers run ogen through OgenArgv, so --clean is here rather
// than in each of them. Without it a deleted operation leaves
// generated code behind that still compiles, and the client keeps
// offering a call the API dropped.
func TestOgenAlwaysCleans(t *testing.T) {
	argv := OgenArgv("cfg.yml", "out", "api", "spec.yml")
	var found bool
	for _, a := range argv {
		if a == "--clean" {
			found = true
		}
	}
	if !found {
		t.Errorf("--clean is not in %v", argv)
	}
}
