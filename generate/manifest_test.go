package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chdir moves into dir for one test. LoadManifest searches from the
// working directory by design, so there is no seam to inject.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func writeManifest(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, ManifestName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const oneClient = `clients:
  acme:
    spec: ./acme.json
    out: ./internal/acmeapi
`

// The manifest sits at the repository root and this is run from
// wherever someone happens to be standing.
func TestTheManifestIsFoundFromASubdirectory(t *testing.T) {
	root := t.TempDir()
	want := writeManifest(t, root, oneClient)
	deep := filepath.Join(root, "services", "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	chdir(t, deep)

	got, m, err := LoadManifest("")
	if err != nil {
		t.Fatalf("LoadManifest(\"\") from %s: %v", deep, err)
	}
	if !sameFile(t, got, want) {
		t.Errorf("found %s, want %s", got, want)
	}
	if _, ok := m.Clients["acme"]; !ok {
		t.Errorf("manifest parsed but acme is missing: %v", Names(m))
	}
}

// A .git is as far up as the search may go. Past it is somebody else's
// repository, and picking up their manifest would generate their
// clients into this tree.
func TestTheSearchStopsAtTheRepositoryRoot(t *testing.T) {
	outer := t.TempDir()
	writeManifest(t, outer, oneClient)

	inner := filepath.Join(outer, "nested")
	if err := os.MkdirAll(filepath.Join(inner, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	chdir(t, inner)

	_, _, err := LoadManifest("")
	if err == nil {
		t.Fatal("found a manifest beyond the repository boundary; it belongs to another repo")
	}
	if !strings.Contains(err.Error(), ManifestName) {
		t.Errorf("error does not name what was missing: %v", err)
	}
}

// A typo'd key must not decode cleanly. `languges: [ts]` generating Go
// only is the kind of failure that gets blamed on the generator.
func TestAMisspeltKeyIsRejected(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `clients:
  acme:
    spec: ./acme.json
    out: ./out
    languges: [ts]
`)
	chdir(t, dir)

	_, _, err := LoadManifest("")
	if err == nil {
		t.Fatal("a misspelt key decoded cleanly; the client would silently emit the wrong languages")
	}
	if !strings.Contains(err.Error(), "languges") {
		t.Errorf("error does not name the offending key, so the reader cannot find it: %v", err)
	}
}

// An empty or clientless manifest is a mistake worth naming, not an
// empty run that reports success having done nothing.
func TestAManifestWithNoClientsIsAnError(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"empty file", ""},
		{"no clients key", "# nothing here\n"},
		{"empty clients map", "clients: {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeManifest(t, dir, tc.body)
			chdir(t, dir)

			if _, _, err := LoadManifest(""); err == nil {
				t.Error("declared no clients but loaded without error; the run would do nothing and say it worked")
			}
		})
	}
}

// --manifest names the file directly, which is how a caller outside
// the tree generates into it.
func TestAnExplicitPathSkipsTheSearch(t *testing.T) {
	dir := t.TempDir()
	path := writeManifest(t, dir, oneClient)
	chdir(t, t.TempDir()) // somewhere with no manifest at all

	got, m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest(%q): %v", path, err)
	}
	if got != path {
		t.Errorf("LoadManifest(%q) = %q, want the path it was given", path, got)
	}
	if len(m.Clients) != 1 {
		t.Errorf("loaded %d clients, want 1", len(m.Clients))
	}
}

// Names is what `list` prints and what the "no such client" error
// offers, so the order has to be stable rather than map order.
func TestNamesAreSorted(t *testing.T) {
	m := &Manifest{Clients: map[string]Client{
		"zulu": {}, "alpha": {}, "mike": {},
	}}
	got := Names(m)
	want := []string{"alpha", "mike", "zulu"}
	if len(got) != len(want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names() = %v, want %v", got, want)
		}
	}
}

// The package name defaults to the output directory, and a directory
// name is allowed to contain a hyphen where a Go package name is not.
func TestPackageFromDirIsAValidIdentifier(t *testing.T) {
	for _, tc := range []struct{ dir, want string }{
		{"./internal/acmeapi", "acmeapi"},
		{"./internal/acme-api", "acmeapi"},
		{"clients/zoom-meetings/", "zoommeetings"},
		{"petapi", "petapi"},
	} {
		if got := PackageFromDir(tc.dir); got != tc.want {
			t.Errorf("PackageFromDir(%q) = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

// sameFile compares two paths by identity rather than string, because
// t.TempDir on macOS hands back /var/... while the working directory
// resolves to /private/var/... - the same directory, spelt twice.
func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	fa, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(fa, fb)
}
