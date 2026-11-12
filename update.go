package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

const (
	repoOwner = "ChristopherScot"

	// A release is per REPOSITORY. In a monorepo that is the PARENT repo,
	// not the service.
	repoName = "oapi-gen"

	tagPrefix = ""

	maxBinarySize = 100 * 1024 * 1024
)

type githubRelease struct {
	TagName string `json:"tag_name"`

	// /releases returns drafts and prereleases, unlike /releases/latest.
	Draft      bool `json:"draft"`
	Prerelease bool `json:"prerelease"`

	Assets []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func updateCmd() *cobra.Command {
	var checkOnly bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "update this binary to the latest release",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runUpdate(checkOnly)
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "report whether an update exists, install nothing")
	return cmd
}

func runUpdate(checkOnly bool) error {
	v := version()
	fmt.Printf("current version: %s\n", v)

	if v == "dev" {
		fmt.Println("running a dev build; not updating")
		return nil
	}
	current := ensureV(v)

	rel, err := latestRelease()
	if err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}
	latest := releaseVersion(rel.TagName)
	if !isNewer(latest, current) {
		fmt.Println("already up to date")
		return nil
	}
	fmt.Printf("new version available: %s\n", latest)
	if checkOnly {
		fmt.Println("run 'oapi-gen update' to install it")
		return nil
	}

	want := assetName()
	var url, sumsURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case want:
			url = a.BrowserDownloadURL
		case checksumsName:
			sumsURL = a.BrowserDownloadURL
		}
	}
	if url == "" {
		return fmt.Errorf("release %s has no asset for %s/%s", rel.TagName, runtime.GOOS, runtime.GOARCH)
	}
	// Refused rather than installed unverified: this writes an
	// executable over the running one, so a release nobody can check
	// is the case to stop on, not the case to shrug at.
	if sumsURL == "" {
		return fmt.Errorf("release %s publishes no %s, so the download cannot be verified", rel.TagName, checksumsName)
	}
	if err := installFrom(url, sumsURL, want); err != nil {
		return err
	}
	fmt.Printf("updated to %s\n", rel.TagName)
	return nil
}

// latestRelease returns the newest release belonging to THIS tool.
// Not /releases/latest, which in a monorepo would be whichever sibling
// released most recently. Releases come back newest-first.
func latestRelease() (*githubRelease, error) {
	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Get(fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?per_page=100", repoOwner, repoName))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned %s", resp.Status)
	}
	var rels []githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rels); err != nil {
		return nil, err
	}
	rel := pickRelease(rels)
	if rel == nil {
		return nil, fmt.Errorf("no oapi-gen release found in %s/%s", repoOwner, repoName)
	}
	return rel, nil
}

// pickRelease prefers a prefixed tag, then falls back to any release with
// this tool's asset so pre-namespace releases stay installable.
func pickRelease(rels []githubRelease) *githubRelease {
	want := assetName()
	var fallback *githubRelease
	for i := range rels {
		r := &rels[i]
		if r.Draft || r.Prerelease {
			continue
		}
		if tagPrefix != "" && strings.HasPrefix(r.TagName, tagPrefix) {
			return r
		}
		if tagPrefix == "" {
			return r
		}
		if fallback == nil && hasAsset(r, want) {
			fallback = r
		}
	}
	return fallback
}

func hasAsset(r *githubRelease, name string) bool {
	for _, a := range r.Assets {
		if a.Name == name {
			return true
		}
	}
	return false
}

func assetName() string {
	return fmt.Sprintf("oapi-gen_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
}

func releaseVersion(tag string) string {
	return ensureV(strings.TrimPrefix(tag, tagPrefix))
}

// ensureV normalises toward the leading "v" that x/mod/semver requires.
func ensureV(v string) string {
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// isNewer uses x/mod/semver rather than a hand-rolled comparison: a
// pairwise compare reads "10" as older than "2" and goes silent the
// moment any component reaches double digits. An unparseable version
// returns false, leaving the caller on a working binary.
func isNewer(latest, current string) bool {
	if !semver.IsValid(latest) || !semver.IsValid(current) {
		return false
	}
	return semver.Compare(latest, current) > 0
}

// checksumsName is the file the release workflow publishes beside the
// binaries: plain `sha256sum` output, one "sha256<2 spaces>filename"
// line per asset. Not goreleaser's binary mode, which writes "*name"
// and would not parse - an update would refuse every release rather
// than accept a wrong one, but it would refuse them all.
const checksumsName = "checksums.txt"

// maxSumsSize bounds checksums.txt, which lists four assets and runs
// to a few hundred bytes. Separate from maxBinarySize because a bound
// sized for a binary says nothing about a list of hashes.
const maxSumsSize = 64 * 1024

// fetchTimeout bounds a download. GitHub being slow should fail the
// command rather than hang a terminal waiting on it.
const fetchTimeout = 30 * time.Second

// execTimeout bounds the downloaded binary's one trial run. It only
// has to print a version; anything slower is not working.
const execTimeout = 10 * time.Second

// fetch reads a URL into memory, bounded in both size and time. The
// size is the caller's because the two things fetched here differ by
// five orders of magnitude.
func fetch(url string, limit int64) ([]byte, error) {
	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("download %s returned %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s is over the %d byte limit", url, limit)
	}
	return body, nil
}

// verify checks the archive against the checksum published for it.
//
// Without this, update is an arbitrary-code path gated only by TLS:
// whatever can answer for the release URL decides what runs as the
// user on the next invocation. A name that is not in the file is
// refused rather than skipped - "no line for this asset" and "the
// bytes are wrong" are the same answer here, which is do not install.
func verify(archive []byte, asset string, sums []byte) error {
	var want []byte
	found := false
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || f[1] != asset {
			continue
		}
		decoded, err := hex.DecodeString(f[0])
		if err != nil {
			return fmt.Errorf("%s has an unreadable checksum for %s: %w", checksumsName, asset, err)
		}
		want, found = decoded, true
		break
	}
	if !found {
		return fmt.Errorf("%s has no line for %s, so the download cannot be verified", checksumsName, asset)
	}
	got := sha256.Sum256(archive)
	// Constant time, and free. Not load-bearing here - both sides are
	// public, and an attacker can compute either offline - but
	// comparing digests in variable time is a habit worth keeping.
	if !hmac.Equal(got[:], want) {
		return fmt.Errorf("%s does not match its published checksum - refusing to install", asset)
	}
	return nil
}

// runnable reports whether the downloaded file executes on this
// machine.
//
// It asserts that the file runs and exits zero, which is what catches
// a wrong-architecture or failed build - not that it is this tool.
// `version` is the subcommand every release has had, and it needs no
// network, no config and no arguments. The bytes are already known to
// be the published ones; verify ran first.
//
// A wrong-architecture binary fails here with an exec format error
// rather than at the user's next invocation, when the working copy is
// already gone.
func runnable(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "version")
	// Without this the deadline does nothing: CombinedOutput waits on
	// the output pipes, not the process, so a binary that is killed
	// while a child still holds stdout keeps the call blocked. A
	// sleep 30 against a 10s deadline returned after 30s.
	cmd.WaitDelay = time.Second

	out, err := cmd.CombinedOutput()
	if err != nil {
		// Took too long is not the same as does not run, and reading
		// "does not run here" for a slow binary sends someone
		// chasing an architecture mismatch that is not there.
		if ctx.Err() != nil {
			return fmt.Errorf("the downloaded binary did not print a version within %v, so it was not installed", execTimeout)
		}
		// Quoted and bounded: it has already run, so it chooses this
		// text, and raw terminal escapes in an error message are its
		// choice too.
		out = bytes.TrimSpace(out)
		if len(out) > 2048 {
			out = append(out[:2048:2048], "..."...)
		}
		return fmt.Errorf("the downloaded binary does not run here (%w), so it was not installed: %q", err, out)
	}
	return nil
}

// installFrom replaces the running binary with the one in the archive
// at url.
func installFrom(url, sumsURL, asset string) error {
	binPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	binPath, err = filepath.EvalSymlinks(binPath)
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	return installTo(url, sumsURL, asset, binPath)
}

// installTo is the half that does not need to be the running binary,
// so a test can watch what happens to a file it owns rather than to
// the test runner.
//
// Replaces binPath via a rename dance: a running executable cannot be
// overwritten in place on every platform, but it can be renamed out of
// the way. The old binary is kept until the swap succeeds so a failure
// is recoverable.
func installTo(url, sumsURL, asset, binPath string) error {
	// Held in memory rather than streamed, because a hash is only a
	// check if it is computed before anything acts on the bytes.
	// Streaming would extract first and verify after, which verifies
	// nothing. Bounded by maxBinarySize rather than by whatever
	// today's release happens to weigh.
	archive, err := fetch(url, maxBinarySize)
	if err != nil {
		return err
	}
	sums, err := fetch(sumsURL, maxSumsSize)
	if err != nil {
		return err
	}
	if err := verify(archive, asset, sums); err != nil {
		return err
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("decompress: %w", err)
	}
	defer gz.Close()

	// The one regular file in the archive, rather than a file with a
	// particular name.
	//
	// The release workflow tars a single binary at the root, so the
	// name was only ever a proxy for "the thing in here" - and a
	// copy of a name this tool has already renamed once, which the
	// rename had to find and move in step with the workflow. The
	// shape cannot drift that way.
	//
	// Written to a temporary file as it is read, because the body is
	// a stream and cannot be rewound, then the rest of the archive is
	// read to be sure nothing follows. Installing swaps the running
	// binary, so an archive holding two files is refused while that
	// is still a no-op.
	// Cleaned up unless the install succeeds, rather than at each
	// error return: the returns that skipped it were only safe while
	// no temp file existed, and a .new left by an interrupted run
	// makes them reachable with one that does.
	tmp := binPath + ".new"
	installed := false
	defer func() {
		if !installed {
			os.Remove(tmp)
		}
	}()

	tr := tar.NewReader(gz)
	found := ""
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		// Regular files only. A symlink entry is skipped rather
		// than followed, so an archive naming one cannot aim the
		// install at a path outside it.
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if found != "" {
			return fmt.Errorf("archive holds more than one file (%s, %s)", found, h.Name)
		}
		if h.Size > maxBinarySize {
			return fmt.Errorf("binary is %d bytes, over the %d limit", h.Size, maxBinarySize)
		}
		found = h.Name

		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return fmt.Errorf("create %s: %w", tmp, err)
		}
		// One byte past the ceiling, so exceeding it and exactly
		// reaching it are distinguishable. Reading to the ceiling
		// made a binary of exactly maxBinarySize look like overflow.
		n, err := io.Copy(f, io.LimitReader(tr, maxBinarySize+1))
		f.Close()
		if err != nil {
			return fmt.Errorf("write %s: %w", tmp, err)
		}
		if n > maxBinarySize {
			return fmt.Errorf("binary exceeded the %d byte limit", maxBinarySize)
		}
		// A build that failed without halting its workflow publishes
		// an archive shaped exactly right and empty. Installing it
		// leaves nothing able to install anything else, and the copy
		// that could have undone it is removed two statements later.
		if n == 0 {
			return fmt.Errorf("the one file in the archive (%s) is empty, so it is not a binary", h.Name)
		}
	}
	if found == "" {
		return fmt.Errorf("archive held no binary")
	}

	// Run it before trusting it. A checksum proves the bytes are the
	// ones published; it cannot prove they are a binary this machine
	// can execute - a release built for the wrong architecture, or
	// from a failed build, hashes correctly and still bricks the
	// tool. The swap is the last thing that happens.
	if err := runnable(tmp); err != nil {
		return err
	}

	old := binPath + ".old"
	if err := os.Rename(binPath, old); err != nil {
		return fmt.Errorf("move current binary aside: %w", err)
	}
	if err := os.Rename(tmp, binPath); err != nil {
		os.Rename(old, binPath) // put it back
		return fmt.Errorf("install new binary: %w", err)
	}
	installed = true
	os.Remove(old)
	return nil
}
