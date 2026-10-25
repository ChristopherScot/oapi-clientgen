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

// A consumer that cannot build the client - because its call sites
// rely on the generated module-level one - still has to be able to
// get the retries. setConfig takes a fetch, so the transport has to
// be reachable without createClient.
func TestResilientFetchIsReachableWithoutCreateClient(t *testing.T) {
	files, err := TypeScript(Params{
		Name: "zoom-users", Version: "2.0.0", ImportPath: "./users",
	})
	if err != nil {
		t.Fatal(err)
	}

	var resilience, index string
	for _, f := range files {
		switch {
		case strings.HasSuffix(f.Path, "resilience.ts"):
			resilience = f.Body
		case strings.HasSuffix(f.Path, "index.ts"):
			index = f.Body
		}
	}
	if resilience == "" || index == "" {
		t.Fatal("expected both resilience.ts and index.ts")
	}

	if !strings.Contains(resilience, "export function createResilientFetch") {
		t.Error("the transport is not exported on its own; a consumer can only get it by building a client it will not use")
	}
	if !strings.Contains(index, "createResilientFetch") {
		t.Error("index.ts does not re-export createResilientFetch, so the package entry hides it")
	}
	// It must not demand a baseUrl: the client being configured
	// already has one.
	if !strings.Contains(resilience, "Omit<ClientOptions, 'baseUrl'>") {
		t.Error("createResilientFetch should not require baseUrl - it is not building a client")
	}
}

// Every file in tsFiles/goFiles is rewritten by regen, so every one
// has to say so. The generated/ directory gets this from
// MarkGenerated; these are written from templates and would not,
// which is how resilience.ts came to look hand-editable while being
// overwritten on every run.
func TestEveryRewrittenFileSaysItIsGenerated(t *testing.T) {
	for _, tc := range []struct {
		lang  string
		files func(Params) ([]File, error)
		p     Params
	}{
		{"ts", TypeScript, Params{Name: "x", Version: "1.0.0", ImportPath: "./x"}},
		{"go", Go, Params{Name: "x", Version: "1.0.0", Package: "api", ImportPath: "example.com/x"}},
	} {
		files, err := tc.files(tc.p)
		if err != nil {
			t.Fatalf("%s: %v", tc.lang, err)
		}
		if len(files) == 0 {
			t.Fatalf("%s: no files", tc.lang)
		}
		for _, f := range files {
			if !strings.Contains(f.Body, "Do not edit") {
				t.Errorf("%s/%s does not say it is generated, but regen rewrites it", tc.lang, f.Path)
			}
		}
	}
}
