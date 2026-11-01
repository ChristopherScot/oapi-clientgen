package apiclient_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/ChristopherScot/oapi-clientgen/apiclient"
)

// Every shared default reaches both languages.
//
// The numbers used to be literals in two templates with nothing
// comparing them, so a timeout that moved in one language only was a
// difference no test could see - and the clients are specified to
// agree on exactly these. Rendering them from one declaration is what
// makes that impossible; this is what proves the rendering happened,
// rather than a template quietly losing its action.
func TestBothLanguagesRenderTheSharedDefaults(t *testing.T) {
	p := apiclient.Params{
		Name: "probe", Version: "1.0.0", Package: "api",
		ImportPath: "example.com/probe/api",
	}
	goFiles, err := apiclient.Go(p)
	if err != nil {
		t.Fatal(err)
	}
	tsFiles, err := apiclient.TypeScript(p)
	if err != nil {
		t.Fatal(err)
	}

	rendered := map[string]string{}
	for _, f := range append(goFiles, tsFiles...) {
		rendered[f.Path] = f.Body
	}

	d := apiclient.Defaults()
	ms := func(k string) string { return strconv.Itoa(d[k]) }

	// The construct each value is rendered into, not the digits: a
	// number alone also appears in prose, so a code line that dropped
	// back to a literal would still be "found" in a doc comment.
	for _, c := range []struct{ file, want string }{
		{"client.go", "Timeout: " + ms("TimeoutMs") + " * time.Millisecond"},
		{"client.go", "maxRetryAfter: " + ms("MaxRetryAfterMs") + " * time.Millisecond"},
		{"client.go", "Window:      " + ms("BreakerWindowMs") + " * time.Millisecond"},
		{"client.go", "MinRequests: " + ms("BreakerMinReqs")},
		{"client.go", "FailPercent: " + ms("BreakerFailPct")},
		{"client.go", "Cooldown:    " + ms("BreakerCooldown") + " * time.Millisecond"},
		{"client.go", "time.Duration{" + ms("SingleBackoffMs") + " * time.Millisecond}"},
		{"resilience.ts", "timeoutMs = " + ms("TimeoutMs")},
		{"resilience.ts", "maxRetryAfterMs = " + ms("MaxRetryAfterMs")},
		{"resilience.ts", "windowMs: " + ms("BreakerWindowMs")},
		{"resilience.ts", "minRequests: " + ms("BreakerMinReqs")},
		{"resilience.ts", "failPercent: " + ms("BreakerFailPct")},
		{"resilience.ts", "cooldownMs: " + ms("BreakerCooldown")},
		{"resilience.ts", "backoffs: () => [" + ms("SingleBackoffMs") + "]"},
	} {
		body, ok := rendered[c.file]
		if !ok {
			t.Fatalf("%s was not rendered; the test is looking in the wrong place", c.file)
		}
		if !strings.Contains(body, c.want) {
			t.Errorf("%s does not contain %q: a template dropped its action, so the two languages can differ again",
				c.file, c.want)
		}
	}
}
