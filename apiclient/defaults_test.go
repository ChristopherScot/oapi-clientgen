package apiclient_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/ChristopherScot/oapi-clientgen/apiclient"
)

// Every shared number reaches both languages, and reaches them from
// the declaration rather than from a literal that happens to match it.
//
// Rendered twice with different numbers: a template carrying a literal
// renders the same thing both times, which asserting against today's
// values cannot see.
func TestBothLanguagesRenderTheSharedDefaults(t *testing.T) {
	p := apiclient.Params{
		Name: "probe", Version: "1.0.0", Package: "api",
		ImportPath: "example.com/probe/api",
	}

	d := apiclient.Defaults()
	// Each field moved to a value long enough not to appear anywhere
	// else in the rendered output, so a crossed wire shows up as a
	// miss rather than as a coincidental match. The second assertion
	// below is what enforces that.
	other := d
	other.TimeoutMs = 5111
	other.SingleBackoffMs = 1222
	other.MaxRetryAfterMs = 30333
	other.BreakerWindowMs = 10444
	other.BreakerMinReqs = 20666
	other.BreakerFailPct = 50777
	other.BreakerCooldownMs = 30555
	other.ExpSeedMs = 100888
	other.ExpAttempts = 5999
	other.ExpJitterPct = 26111

	for _, tmpl := range []string{"go_client.go.tmpl", "ts_resilience.ts.tmpl"} {
		was, err := apiclient.RenderWith(tmpl, p, d)
		if err != nil {
			t.Fatal(err)
		}
		now, err := apiclient.RenderWith(tmpl, p, other)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range []int{
			other.TimeoutMs, other.SingleBackoffMs, other.MaxRetryAfterMs,
			other.BreakerWindowMs, other.BreakerMinReqs, other.BreakerFailPct,
			other.BreakerCooldownMs, other.ExpSeedMs, other.ExpAttempts,
			other.ExpJitterPct,
		} {
			s := strconv.Itoa(n)
			if !strings.Contains(now, s) {
				t.Errorf("%s does not follow the declaration: %s is missing when it is the declared value, so a literal is standing in for it",
					tmpl, s)
			}
			if strings.Contains(was, s) {
				t.Errorf("%s contains %s before it was declared; the test numbers collide with something already rendered", tmpl, s)
			}
		}
	}
}

// The two wait forms are the same sentence, so a change to one has to
// be a change to both.
//
// Adjacency in the declaration makes that easy to get right; it does
// not make getting it wrong legible. Drifting GoWait to "WAITED %dms"
// reports five Retry-After cases as client disagreements, pointing a
// reader at the parsers rather than at the format string.
func TestTheTwoWaitFormsAreTheSameSentence(t *testing.T) {
	v := apiclient.Verdicts()
	// What is left once each language's way of saying "the number"
	// is taken out.
	shape := func(s, verb string) string { return strings.Replace(s, verb, "", 1) }
	if g, ts := shape(v.GoWait, "%d"), shape(v.TSWait, "${ms}"); g != ts {
		t.Errorf("GoWait %q and TSWait %q say different things: %q against %q",
			v.GoWait, v.TSWait, g, ts)
	}
}
