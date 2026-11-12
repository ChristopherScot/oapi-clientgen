package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// archiveOf builds a .tar.gz holding the named files, so a test can
// say what shape an archive has rather than what a file inside it is
// called. Built rather than streamed because the checksum has to be
// taken over the same bytes the server will send.
func archiveOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// release serves an archive and the checksums.txt that vouches for it,
// at /asset and /sums. sums is what the file says; passing nil means
// "whatever the archive actually hashes to", which is the honest case.
func release(t *testing.T, archive []byte, sums []byte) (assetURL, sumsURL string) {
	t.Helper()
	if sums == nil {
		h := sha256.Sum256(archive)
		sums = []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(h[:]), assetName()))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) { w.Write(archive) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, _ *http.Request) { w.Write(sums) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL + "/asset", srv.URL + "/sums"
}

// runs is a payload that executes and prints a version, which is what
// installTo requires of the file it is about to install. A shell
// script stands in for the real binary: the tests are about which
// file is chosen and whether it is trusted, not about Go linking.
const runs = "#!/bin/sh\necho v9.9.9\n"

// install is the call under test, with a release that vouches for
// itself: the shape tests care about the archive, not the checksum.
func install(t *testing.T, files map[string]string, binPath string) error {
	t.Helper()
	asset, sums := release(t, archiveOf(t, files), nil)
	return installTo(asset, sums, assetName(), binPath)
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
	for _, c := range []struct{ name, entry string }{
		{"bare", "oapi-gen"},
		{"dot_slash_prefixed", "./oapi-gen"},
		{"the_name_before_the_rename", "oapi-clientgen"},
		{"a_name_this_tool_never_used", "whatever"},
	} {
		t.Run(c.name, func(t *testing.T) {
			name := c.entry
			bin := standIn(t, "old")
			if err := install(t, map[string]string{name: runs}, bin); err != nil {
				t.Fatalf("installing an archive holding %q: %v", name, err)
			}
			got, err := os.ReadFile(bin)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != runs {
				t.Errorf("binary is %q, want the archive's contents", got)
			}
		})
	}
}

// Two files is a release nobody meant to publish, and picking one
// would install a coin flip over the running binary.
func TestAnArchiveHoldingTwoFilesIsRefused(t *testing.T) {
	bin := standIn(t, "old")
	err := install(t, map[string]string{"oapi-gen": runs, "README": "hello"}, bin)
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
	err := install(t, nil, bin)
	if err == nil {
		t.Fatal("an empty archive was accepted")
	}
	if got, _ := os.ReadFile(bin); string(got) != "old" {
		t.Errorf("the running binary changed: %q", got)
	}
}

// An archive shaped exactly right and empty is what a build that failed
// without halting its workflow publishes. Installing it leaves nothing
// able to install anything else, and the copy that could undo it is
// removed moments later.
func TestAnEmptyBinaryIsRefused(t *testing.T) {
	bin := standIn(t, "the working binary")
	err := install(t, map[string]string{"oapi-gen": ""}, bin)
	if err == nil {
		t.Fatal("an empty file was installed over the running binary")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error does not say the file was empty: %v", err)
	}
	if got, _ := os.ReadFile(bin); string(got) != "the working binary" {
		t.Errorf("the binary was replaced: %q", got)
	}
}

// A .new left by an interrupted run must not survive the next failure:
// it sits beside the binary, executable, indefinitely.
func TestAStaleTempFileDoesNotSurviveAFailure(t *testing.T) {
	bin := standIn(t, "old")
	if err := os.WriteFile(bin+".new", []byte("stale"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := install(t, nil, bin); err == nil {
		t.Fatal("an empty archive was accepted")
	}
	if _, err := os.Stat(bin + ".new"); err == nil {
		t.Error("a stale .new survived the failure")
	}
}

// The whole point: bytes that are not what the release published do
// not become the binary the user runs next.
func TestATamperedArchiveIsRefused(t *testing.T) {
	bin := standIn(t, "the working binary")
	// The checksums file vouches for one archive; a different one is
	// served. That is what a compromised mirror or a MITM looks like.
	honest := archiveOf(t, map[string]string{"oapi-gen": runs})
	tampered := archiveOf(t, map[string]string{"oapi-gen": runs + "# swapped in\n"})
	h := sha256.Sum256(honest)
	sums := []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(h[:]), assetName()))

	asset, sumsURL := release(t, tampered, sums)
	err := installTo(asset, sumsURL, assetName(), bin)
	if err == nil {
		t.Fatal("an archive that does not match its checksum was installed")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Errorf("error does not say what failed: %v", err)
	}
	if got, _ := os.ReadFile(bin); string(got) != "the working binary" {
		t.Errorf("the binary was replaced: %q", got)
	}
}

// A checksums.txt with no line for this asset cannot vouch for it, and
// an unverifiable download is refused rather than installed.
func TestAnAssetWithNoChecksumLineIsRefused(t *testing.T) {
	bin := standIn(t, "old")
	archive := archiveOf(t, map[string]string{"oapi-gen": runs})
	h := sha256.Sum256(archive)
	// Correct hash, wrong filename: the line is for a different build.
	sums := []byte(fmt.Sprintf("%s  oapi-gen_some_other_platform.tar.gz\n", hex.EncodeToString(h[:])))

	asset, sumsURL := release(t, archive, sums)
	err := installTo(asset, sumsURL, assetName(), bin)
	if err == nil {
		t.Fatal("an asset with no checksum line was installed")
	}
	if !strings.Contains(err.Error(), "no line for") {
		t.Errorf("error does not say the asset was unlisted: %v", err)
	}
	if got, _ := os.ReadFile(bin); string(got) != "old" {
		t.Errorf("the binary was replaced: %q", got)
	}
}

// A truncated or garbled checksums.txt is not a reason to proceed.
func TestAnUnreadableChecksumIsRefused(t *testing.T) {
	bin := standIn(t, "old")
	archive := archiveOf(t, map[string]string{"oapi-gen": runs})
	sums := []byte("nothexatall  " + assetName() + "\n")

	asset, sumsURL := release(t, archive, sums)
	if err := installTo(asset, sumsURL, assetName(), bin); err == nil {
		t.Fatal("an unreadable checksum was accepted")
	}
	if got, _ := os.ReadFile(bin); string(got) != "old" {
		t.Errorf("the binary was replaced: %q", got)
	}
}

// A checksum proves the bytes are the published ones. It cannot prove
// they run here - a release built for the wrong architecture matches
// its checksum perfectly and bricks the tool, and the working copy is
// gone by the time anyone finds out.
func TestABinaryThatDoesNotRunIsNotInstalled(t *testing.T) {
	bin := standIn(t, runs)
	// Correctly checksummed, and not an executable this machine can
	// run - which is what a wrong-arch build looks like from here.
	err := install(t, map[string]string{"oapi-gen": "\x7fELF not really"}, bin)
	if err == nil {
		t.Fatal("a file that does not execute was installed over the running binary")
	}
	if !strings.Contains(err.Error(), "does not run here") {
		t.Errorf("error does not say the binary failed to run: %v", err)
	}
	if got, _ := os.ReadFile(bin); string(got) != runs {
		t.Errorf("the binary was replaced: %q", got)
	}
	if _, err := os.Stat(bin + ".new"); err == nil {
		t.Error("the rejected download was left behind")
	}
}

// checksums.txt lists four assets and runs to a few hundred bytes. A
// bound sized for a 100MB binary would let a hostile endpoint spend
// the client's memory on a file that is never that large.
func TestAnOversizedChecksumsFileIsRefused(t *testing.T) {
	bin := standIn(t, runs)
	archive := archiveOf(t, map[string]string{"oapi-gen": runs})
	huge := bytes.Repeat([]byte("x"), maxSumsSize+1)

	asset, sumsURL := release(t, archive, huge)
	err := installTo(asset, sumsURL, assetName(), bin)
	if err == nil {
		t.Fatal("an oversized checksums.txt was accepted")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error does not say it was over the limit: %v", err)
	}
	if got, _ := os.ReadFile(bin); string(got) != runs {
		t.Errorf("the binary was replaced: %q", got)
	}
}

// A binary that hangs must not hang the update. CombinedOutput waits
// on the output pipes rather than the process, so the context's
// deadline alone does not return control - a sleep 30 against a 10s
// deadline returned after 30s until WaitDelay was set.
func TestAHangingBinaryDoesNotHangTheUpdate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "slow")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	err := runnable(p)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a binary that never printed a version was accepted")
	}
	// Generous, because the point is "bounded", not "fast": without
	// the fix this takes the payload's full 30s.
	if elapsed > execTimeout+5*time.Second {
		t.Errorf("runnable took %v for a %v deadline, so the deadline is not bounding it", elapsed.Round(time.Second), execTimeout)
	}
	if !strings.Contains(err.Error(), "did not print a version") {
		t.Errorf("a timeout is reported as something else: %v", err)
	}
}

// What a failing binary prints ends up in an error message, and it
// has already run, so it chooses that text.
func TestABinarysOutputCannotPaintTheTerminal(t *testing.T) {
	p := filepath.Join(t.TempDir(), "noisy")
	// Clears the screen, homes the cursor, then claims success.
	body := "#!/bin/sh\nprintf '\\033[2J\\033[1;1HFAKE: update succeeded'\nexit 1\n"
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	err := runnable(p)
	if err == nil {
		t.Fatal("a binary exiting non-zero was accepted")
	}
	if strings.ContainsRune(err.Error(), '\x1b') {
		t.Error("the binary's terminal escapes reached the error message raw")
	}
}
