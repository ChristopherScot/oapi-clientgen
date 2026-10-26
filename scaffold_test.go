package main

import (
	"testing"
)

// A binary that cannot name itself cannot update itself: runUpdate
// refuses to act on "dev".
func TestVersionAlwaysAnswers(t *testing.T) {
	if version() == "" {
		t.Error("version() is empty; it must fall back to the build info, then to \"dev\"")
	}
}

func TestPickVersion(t *testing.T) {
	for _, tc := range []struct {
		name, ldflags, module, want string
	}{
		{"release binary", "v1.2.3", "v1.2.3", "v1.2.3"},
		// go install sets no ldflags, but the module version it was
		// installed at is embedded. Without this the binary reports
		// "dev" and refuses to update itself.
		{"go install", "", "v1.2.3", "v1.2.3"},
		{"local go build", "", "(devel)", "dev"},
		{"no build info at all", "", "", "dev"},
		// A build from a working tree, which go embeds as a
		// pseudo-version. It names no release, so updating from it
		// would overwrite someone's own build.
		{"pseudo-version", "", "v1.21.1-0.20261026015910-0fa740d72a1f", "dev"},
		// What `go install` reports for an untagged module, and the
		// form that slipped through a narrower check.
		{"untagged pseudo-version", "", "v0.0.0-20261026020159-df3906439210", "dev"},
		// A real prerelease tag is not a pseudo-version.
		{"prerelease tag", "", "v1.2.3-rc1", "v1.2.3-rc1"},
		{"dirty working tree", "", "v1.21.1-0.20261026015910-0fa740d72a1f+dirty", "dev"},
		{"not a version at all", "", "garbage", "dev"},
		{"ldflags wins", "v9.9.9", "v1.2.3", "v9.9.9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pickVersion(tc.ldflags, tc.module); got != tc.want {
				t.Errorf("pickVersion(%q, %q) = %q, want %q",
					tc.ldflags, tc.module, got, tc.want)
			}
		})
	}
}

func TestRootHasExpectedCommands(t *testing.T) {
	want := map[string]bool{"update": false, "version": false}
	for _, c := range rootCmd().Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("root command is missing %q", name)
		}
	}
}

func TestIsNewer(t *testing.T) {
	for _, tc := range []struct {
		latest, current string
		want            bool
		why             string
	}{
		{"v1.2.0", "v1.1.0", true, "ordinary bump"},
		{"v1.1.0", "v1.2.0", false, "older is not newer"},
		{"v1.1.0", "v1.1.0", false, "equal is not newer"},
		{"v1.1.1", "v1.1", true, "an omitted patch reads as .0"},

		// A pairwise compare gets these backwards and then reports
		// "already up to date" forever.
		{"v1.10.0", "v1.2.0", true, "10 is newer than 2, not older"},
		{"v1.2.0", "v1.10.0", false, "and the reverse still holds"},
		{"v2.0.0", "v1.99.99", true, "major wins over any minor"},

		{"v1.0.0", "v1.0.0-rc1", true, "release supersedes its rc"},
		{"v1.0.0-rc1", "v1.0.0", false, "an rc does not supersede the release"},

		{"not-a-version", "v1.0.0", false, "unparseable latest"},
		{"v1.0.0", "garbage", false, "unparseable current"},
		{"dev", "v1.0.0", false, "a dev build is not a version"},
	} {
		if got := isNewer(tc.latest, tc.current); got != tc.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v (%s)",
				tc.latest, tc.current, got, tc.want, tc.why)
		}
	}
}

// semver.IsValid rejects an unprefixed version, so if ensureV broke
// update would go silent rather than fail loudly.
func TestEnsureVAcceptsEitherSpelling(t *testing.T) {
	for _, in := range []string{"1.2.3", "v1.2.3"} {
		if got := ensureV(in); got != "v1.2.3" {
			t.Errorf("ensureV(%q) = %q, want %q", in, got, "v1.2.3")
		}
	}
	if !isNewer(ensureV("1.10.0"), ensureV("v1.2.0")) {
		t.Error("a mixed-spelling comparison did not reach semver intact")
	}
}

func testRelease(tag string, draft, pre bool, assets ...string) githubRelease {
	r := githubRelease{TagName: tag, Draft: draft, Prerelease: pre}
	for _, n := range assets {
		r.Assets = append(r.Assets, struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{Name: n, BrowserDownloadURL: "https://example.invalid/" + n})
	}
	return r
}

func TestASiblingsReleaseIsNotAnUpdate(t *testing.T) {
	if tagPrefix == "" {
		t.Skip("single-service repo: every release here is ours")
	}
	got := pickRelease([]githubRelease{
		testRelease("sibling/v9.9.9", false, false, "sibling.apk"),
		testRelease(tagPrefix+"v1.2.3", false, false, assetName()),
	})
	if got == nil || got.TagName != tagPrefix+"v1.2.3" {
		t.Fatalf("picked %v, want %sv1.2.3 - a sibling's release is not an update for us",
			got, tagPrefix)
	}
}

func TestTheNewestOfOursWins(t *testing.T) {
	got := pickRelease([]githubRelease{
		testRelease(tagPrefix+"v2.0.0", false, false, assetName()),
		testRelease(tagPrefix+"v1.0.0", false, false, assetName()),
	})
	if got == nil || got.TagName != tagPrefix+"v2.0.0" {
		t.Fatalf("picked %v, want the newest", got)
	}
}

// Releases cut before tags were namespaced are still installable.
func TestLegacyUnprefixedReleasesStayReachable(t *testing.T) {
	if tagPrefix == "" {
		t.Skip("no prefix, so nothing is legacy")
	}
	got := pickRelease([]githubRelease{
		testRelease("v9.9.9", false, false, "somethingelse.apk"),
		testRelease("v0.1.2", false, false, assetName()),
	})
	if got == nil || got.TagName != "v0.1.2" {
		t.Fatalf("picked %v, want the untagged v0.1.2 that carries our asset", got)
	}
}

// /releases returns drafts and prereleases; /releases/latest did not.
func TestDraftsAndPrereleasesAreSkipped(t *testing.T) {
	got := pickRelease([]githubRelease{
		testRelease(tagPrefix+"v3.0.0", true, false, assetName()),
		testRelease(tagPrefix+"v2.9.0", false, true, assetName()),
		testRelease(tagPrefix+"v2.0.0", false, false, assetName()),
	})
	if got == nil || got.TagName != tagPrefix+"v2.0.0" {
		t.Fatalf("picked %v, want the newest fully-released version", got)
	}
}

// The prefix must not reach semver, or every release reads as a downgrade.
func TestTheVersionComparedHasNoPrefix(t *testing.T) {
	if got := releaseVersion(tagPrefix + "v1.2.3"); got != "v1.2.3" {
		t.Errorf("releaseVersion(%q) = %q, want v1.2.3", tagPrefix+"v1.2.3", got)
	}
	if got := releaseVersion("v1.2.3"); got != "v1.2.3" {
		t.Errorf("releaseVersion(bare) = %q, want v1.2.3", got)
	}
}
