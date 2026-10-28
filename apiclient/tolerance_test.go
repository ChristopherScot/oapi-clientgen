package apiclient_test

import "testing"

// A disagreement that is only two clocks, and a disagreement that is
// real, must not collapse into the same verdict.
func TestWaitsAgreeWithinClockSkewButNotBeyondIt(t *testing.T) {
	for _, c := range []struct {
		name  string
		a, b  string
		agree bool
	}{
		{"two clocks, fifteen seconds apart", "WAIT 24727ms", "WAIT 9727ms", true},
		{"identical", "WAIT 2000ms", "WAIT 2000ms", true},
		{"beyond the tolerance", "WAIT 2000ms", "WAIT 120000ms", false},
		{"a wait and a stop", "WAIT 2000ms", "STOP", false},
		{"a wait and a fallback", "WAIT 0ms", "FALLBACK", false},
		{"stop and fallback", "STOP", "FALLBACK", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			seen := map[string][]string{c.a: {"first"}}
			got := agreedVerdict(c.b, seen)
			if agreed := got == c.a; agreed != c.agree {
				t.Errorf("%q vs %q: agreed=%v, want %v", c.a, c.b, agreed, c.agree)
			}
		})
	}
}
