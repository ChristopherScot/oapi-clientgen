package codegen

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ChristopherScot/homelabctl/internal/apiclient"
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

// The argv the scaffolded path runs: the cd and the -f have to agree,
// which is what a caller-built config path got wrong - it named
// clients/ts/openapi-ts.config.ts from inside clients/ts.
func TestScaffoldedCommandNamesAConfigItCanReach(t *testing.T) {
	const dir = "clients/ts"
	argv := HeyAPICommandIn(dir)

	script := argv[len(argv)-1]
	if !strings.Contains(script, "cd '"+dir+"'") {
		t.Fatalf("no cd into the client: %s", script)
	}

	m := regexp.MustCompile(`-f (\S+)`).FindStringSubmatch(script)
	if m == nil {
		t.Fatalf("no -f: hey-api would search from npx's working directory\n%s", script)
	}
	// Resolved from where the cd leaves us.
	if got := resolveFrom(filepath.Join("/repo/service", dir), m[1]); got != "/repo/service/clients/ts/openapi-ts.config.ts" {
		t.Errorf("-f resolves to %s, which does not exist", got)
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
