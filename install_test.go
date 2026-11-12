package main

import (
	"archive/tar"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tarball serves a .tar.gz holding the named files, so a test can say
// what shape an archive has rather than what a file inside it is
// called.
func tarball(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		gz := gzip.NewWriter(w)
		tw := tar.NewWriter(gz)
		for name, body := range files {
			if err := tw.WriteHeader(&tar.Header{
				Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
			}); err != nil {
				t.Error(err)
				return
			}
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Error(err)
				return
			}
		}
		tw.Close()
		gz.Close()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// installFrom writes over the running binary, so the tests point it at
// a stand-in and check what happened to that file.
func standIn(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "oapi-gen")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// The binary is found by being the one file in the archive, not by
// what it is called. A release built before the rename is named
// something else and still installs.
func TestTheBinaryIsFoundWhateverItIsCalled(t *testing.T) {
	for _, name := range []string{"oapi-gen", "./oapi-gen", "oapi-clientgen", "whatever"} {
		t.Run(name, func(t *testing.T) {
			bin := standIn(t, "old")
			srv := tarball(t, map[string]string{name: "new"})
			if err := installTo(srv.URL, bin); err != nil {
				t.Fatalf("installing an archive holding %q: %v", name, err)
			}
			got, err := os.ReadFile(bin)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "new" {
				t.Errorf("binary is %q, want the archive's contents", got)
			}
		})
	}
}

// Two files is a release nobody meant to publish, and picking one
// would install a coin flip over the running binary.
func TestAnArchiveHoldingTwoFilesIsRefused(t *testing.T) {
	bin := standIn(t, "old")
	srv := tarball(t, map[string]string{"oapi-gen": "new", "README": "hello"})

	err := installTo(srv.URL, bin)
	if err == nil {
		t.Fatal("an archive holding two files was installed")
	}
	if !strings.Contains(err.Error(), "more than one") {
		t.Errorf("error does not say what is wrong with the archive: %v", err)
	}
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Errorf("the running binary was replaced before the archive was refused: %q", got)
	}
}

// An empty archive leaves the binary alone and says so.
func TestAnArchiveHoldingNoFileIsRefused(t *testing.T) {
	bin := standIn(t, "old")
	srv := tarball(t, nil)

	err := installTo(srv.URL, bin)
	if err == nil {
		t.Fatal("an empty archive was accepted")
	}
	if got, _ := os.ReadFile(bin); string(got) != "old" {
		t.Errorf("the running binary changed: %q", got)
	}
}
