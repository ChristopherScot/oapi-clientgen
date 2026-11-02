package apiclient_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/ChristopherScot/oapi-clientgen/apiclient"
)

// The rule that lets the clients' answers be compared exactly.
//
// A wait is deadline-minus-now for a date case, so two clients reading
// their own clocks in separate processes would differ over nothing.
// Nothing in the clients prevents that; what prevents it is this list
// holding no date inside the window where it could happen - past
// dates clamp to 0ms and distant ones exceed the cap and stop.
//
// Asserted here rather than left to the comment that says so, because
// a date a few seconds out looks like an ordinary case to add.
func TestNoDateCaseFallsInsideTheCap(t *testing.T) {
	// The client's own cap, so a change to it moves this rule with
	// it: raise the cap past a date case and the clients each compute
	// their own wait, which the comparison reports as them disagreeing.
	maxWait := time.Duration(apiclient.Defaults().MaxRetryAfterMs) * time.Millisecond
	for _, header := range apiclient.ParityCases() {
		when, err := http.ParseTime(header)
		if err != nil {
			continue
		}
		if d := time.Until(when); d > 0 && d <= maxWait {
			t.Errorf("Retry-After %q is %v away, inside the %v cap: each client would compute its own wait and they would disagree over nothing. Use a date in the past, or far enough ahead to exceed the cap.",
				header, d.Round(time.Second), maxWait)
		}
	}
}
