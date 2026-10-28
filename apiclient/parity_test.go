package apiclient_test

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChristopherScot/oapi-clientgen/apiclient"
)

// A Retry-After header every generated client answers differently on
// purpose, with the reason. Meant to stay empty: an entry is a promise
// that the disagreement is intended, not a note that one exists.
var parityExceptions = map[string]string{}

// TestAllClientsAgreeOnRetryAfter renders every client, asks each one
// what it makes of the same Retry-After headers, and fails where they
// disagree.
//
// Renders and runs the clients itself, so it depends on no other test
// having run and answers the same way alone as in a full run. They
// share nothing, so the builds overlap rather than add up.
//
// No case carries an expected answer; see apiclient.ParityCases.
func TestAllClientsAgreeOnRetryAfter(t *testing.T) {
	if testing.Short() {
		t.Skip("renders every client and compares what they answered")
	}
	t.Parallel()

	clients := map[string]func(*testing.T) map[string]string{
		"go": runGoClient,
		"ts": runTSClient,
	}
	// Subtests rather than bare goroutines, because the runners call
	// t.Fatalf and that must happen on the goroutine of the T it
	// belongs to - from a goroutine of ours it would abandon the run
	// without failing anything.
	//
	// Nested inside one more t.Run because a parallel subtest resumes
	// only after its parent's function returns: started directly here,
	// every one of them would still be waiting when the comparison
	// below read the answers. The wrapper returns once they are all
	// done, which is what makes the answers complete.
	// The mutex guards the two subtests against each other, not the
	// read below: parallel siblings write this map at once, which is
	// a race whatever the values. The wrapper's return already
	// publishes their writes to us.
	var mu sync.Mutex
	answers := map[string]map[string]string{}
	ran := map[string]bool{}
	skipped := map[string]bool{}
	t.Run("clients", func(t *testing.T) {
		for lang, run := range clients {
			t.Run(lang, func(t *testing.T) {
				t.Parallel()
				// Cleanup, because a runner that skips for want of
				// its toolchain never reaches the line after it -
				// and that skip is the thing the comparison most
				// needs to know about.
				t.Cleanup(func() {
					mu.Lock()
					defer mu.Unlock()
					ran[lang] = true
					skipped[lang] = t.Skipped()
				})
				got := run(t)
				mu.Lock()
				defer mu.Unlock()
				answers[lang] = got
			})
		}
	})

	// A client can be absent for three different reasons and only one
	// of them is this test's business, so each says which it was: a
	// skip means the toolchain for that client is missing, no entry at
	// all means a -run filter excluded it, and a failure has already
	// reported itself.
	names := make([]string, 0, len(clients))
	for lang := range clients {
		names = append(names, lang)
	}
	sort.Strings(names)
	for _, lang := range names {
		switch {
		case !ran[lang]:
			t.Fatalf("the %s client never ran, so there was nothing to compare; this test needs every client, and -run cannot select a subset of them", lang)
		case skipped[lang]:
			t.Skipf("the %s client was skipped, so only one client could be asked", lang)
		case len(answers[lang]) == 0:
			t.Fatalf("the %s client answered nothing", lang)
		}
	}

	langs := make([]string, 0, len(answers))
	for lang := range answers {
		langs = append(langs, lang)
	}
	sort.Strings(langs)

	// Every missing case at once: a template that stopped recording
	// drops all of them, and one header named out of 31 reads like a
	// single bad case rather than a recorder that is gone.
	incomplete := false
	for _, lang := range langs {
		missing := []string{}
		for _, header := range apiclient.ParityCases() {
			if _, ok := answers[lang][header]; !ok {
				missing = append(missing, strconv.Quote(header))
			}
		}
		if len(missing) > 0 {
			incomplete = true
			t.Errorf("the %s client did not answer %d of %d cases in apiclient.ParityCases(): %s",
				lang, len(missing), len(apiclient.ParityCases()), strings.Join(missing, ", "))
		}
	}
	if incomplete {
		return
	}

	t.Logf("compared %d cases across %s", len(apiclient.ParityCases()), strings.Join(langs, ", "))

	for _, header := range apiclient.ParityCases() {
		byVerdict := map[string][]string{}
		for _, lang := range langs {
			v := agreedVerdict(answers[lang][header], byVerdict)
			byVerdict[v] = append(byVerdict[v], lang)
		}
		why, excepted := parityExceptions[header]
		switch {
		case len(byVerdict) == 1 && excepted:
			t.Errorf("Retry-After %q agrees everywhere now, so drop it from parityExceptions (listed as: %s)", header, why)
		case len(byVerdict) == 1:
		case excepted:
			t.Logf("Retry-After %q differs on purpose: %s (%s)", header, describe(byVerdict), why)
		default:
			t.Errorf("Retry-After %q: %s", header, describe(byVerdict))
		}
	}
}

// clockSkew is how far apart two clients' waits may be and still
// count as the same answer.
//
// A wait computed from an HTTP date is deadline-minus-now, and the
// clients read their own clocks in separate processes seconds apart,
// so the same header gives each a different number. Wide enough to
// cover the gap between the two runs - the slower client builds for
// about fifteen seconds - and far below any difference a reader would
// call a disagreement.
const clockSkew = 90 * time.Second

// agreedVerdict returns the verdict v should be counted as: an
// equivalent one already recorded for this header, or v itself.
//
// Only waits are compared loosely, and only against each other. STOP
// and FALLBACK are classifications with nothing to round, and a wait
// from a delay in seconds is arithmetic on the header - "2" is 2000ms
// in both languages or one of them is wrong - so a tolerance there
// would hide a real defect. Both arrive as exact strings and match or
// do not.
func agreedVerdict(v string, seen map[string][]string) string {
	ms, ok := waitMillis(v)
	if !ok {
		return v
	}
	for other := range seen {
		got, ok := waitMillis(other)
		if ok && absDuration(got-ms) <= clockSkew {
			return other
		}
	}
	return v
}

// waitMillis reads the duration out of a "WAIT 1234ms" verdict.
func waitMillis(v string) (time.Duration, bool) {
	digits, ok := strings.CutPrefix(v, "WAIT ")
	if !ok {
		return 0, false
	}
	digits, ok = strings.CutSuffix(digits, "ms")
	if !ok {
		return 0, false
	}
	ms, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(ms) * time.Millisecond, true
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// verdicts picks the PARITY lines out of a test runner's output.
//
// Tagged rather than parsed positionally because the runner's own
// chatter shares the stream.
func verdicts(t *testing.T, lang, out string) map[string]string {
	t.Helper()
	got := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "PARITY\t")
		if !ok {
			continue
		}
		quoted, verdict, ok := strings.Cut(rest, "\t")
		if !ok {
			t.Fatalf("%s printed a PARITY line with no verdict: %q", lang, line)
		}
		header, err := strconv.Unquote(quoted)
		if err != nil {
			t.Fatalf("%s printed an unreadable header %q: %v", lang, quoted, err)
		}
		got[header] = verdict
	}
	return got
}

// describe renders "FALLBACK (go), STOP (ts)", so a failure names the
// answers and who gave them.
func describe(byVerdict map[string][]string) string {
	verdicts := make([]string, 0, len(byVerdict))
	for v := range byVerdict {
		verdicts = append(verdicts, v)
	}
	sort.Strings(verdicts)
	parts := make([]string, 0, len(verdicts))
	for _, v := range verdicts {
		sort.Strings(byVerdict[v])
		parts = append(parts, v+" ("+strings.Join(byVerdict[v], ", ")+")")
	}
	return strings.Join(parts, ", ")
}
