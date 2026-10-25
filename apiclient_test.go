package apiclient

import (
	"strings"
	"testing"
)

// Published decides how the client's own files import each other, and
// every one of them has to make the same choice: a package that
// mixed '.ts' and extensionless imports would compile in one place
// and not the other.
//
// A vendored client is the strict case. It is compiled by a tsconfig
// it does not own, where a '.ts' import is TS5097 unless the
// consuming repo turns on allowImportingTsExtensions - which is not
// ours to ask for.
func TestVendoredClientImportsWithoutExtensions(t *testing.T) {
	files, err := TypeScript(Params{
		Name: "zoom-users", Version: "2.0.0", ImportPath: "./users",
		Published: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no files rendered")
	}

	for _, f := range files {
		for _, line := range strings.Split(f.Body, "\n") {
			if !strings.Contains(line, "from './") && !strings.Contains(line, "import pkg") {
				continue
			}
			if strings.Contains(line, ".ts'") {
				t.Errorf("%s imports with a .ts extension; TS5097 in the consuming repo:\n  %s",
					f.Path, strings.TrimSpace(line))
			}
		}
	}
}

// The published case is the mirror: its own tsconfig sets
// allowImportingTsExtensions and rewrites them into dist/, so the
// extension has to be there or the emitted JS points at nothing.
func TestPublishedClientImportsWithExtensions(t *testing.T) {
	files, err := TypeScript(Params{
		Name: "pokedex", Version: "0.8.1", ImportPath: "@o/pokedex-client",
		Published: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var sawIndex bool
	for _, f := range files {
		if !strings.HasSuffix(f.Path, "index.ts") {
			continue
		}
		sawIndex = true
		if !strings.Contains(f.Body, "./resilience.ts") {
			t.Errorf("%s does not import ./resilience.ts; tsc would emit a dangling path:\n%s",
				f.Path, f.Body)
		}
	}
	if !sawIndex {
		t.Fatal("no index.ts rendered")
	}
}
