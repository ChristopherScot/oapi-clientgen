package apiclient_test

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

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
	slices.Sort(names)
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
	slices.Sort(langs)

	// Every missing case at once: a template that stopped recording
	// drops all of them, and one header named out of 31 reads like a
	// single bad case rather than a recorder that is gone.
	incomplete := false
	for _, lang := range langs {
		var missing []string
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
			byVerdict[answers[lang][header]] = append(byVerdict[answers[lang][header]], lang)
		}
		why, excepted := parityExceptions[header]
		switch {
		case len(byVerdict) == 1 && excepted:
			t.Errorf("Retry-After %q agrees everywhere now, so drop it from parityExceptions (listed as: %s)", header, why)
		case len(byVerdict) == 1: // agreed, which is the point
		case excepted:
			t.Logf("Retry-After %q differs on purpose: %s (%s)", header, describe(byVerdict), why)
		default:
			t.Errorf("Retry-After %q: %s", header, describe(byVerdict))
		}
	}
}

// verdicts reads what a client answered out of its runner's output.
//
// Two things keep a test's own output from passing itself off as the
// answers, and they cover different halves of it.
//
// Only a line beginning "PARITY " at column 0 counts. Both inner
// runners indent the output of a failing assertion, continuation
// lines included, so an assertion quoting these answers back is
// disqualified by that alone. What a test prints deliberately is not:
// a bare Printf reaches stdout unindented, and node --test does not
// indent console.log even inside a subtest.
//
// So exactly one such line may appear, and a second is refused rather
// than ranked. The harness cannot tell which of two documents came
// from the recorder, and picking one was how an assertion message
// once got itself reported as a client's answer.
//
// One document rather than a line per case is what makes that
// refusal possible, and it also makes a partial or interleaved read
// fail here instead of returning some of the answers.
func verdicts(t *testing.T, lang, out string) map[string]string {
	t.Helper()
	var answers map[string]string
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "PARITY ")
		if !ok {
			continue
		}
		if answers != nil {
			t.Fatalf("%s printed a second PARITY line %q; only the recorder prints one, so one of them is not the recorder's", lang, rest)
		}
		if err := json.Unmarshal([]byte(rest), &answers); err != nil {
			t.Fatalf("%s printed an unreadable PARITY line %q: %v", lang, rest, err)
		}
		if answers == nil {
			t.Fatalf("%s printed a PARITY line with no answers in it: %q", lang, rest)
		}
	}
	if answers == nil {
		t.Fatalf("%s printed no PARITY line", lang)
	}
	return answers
}

// describe renders "FALLBACK (go), STOP (ts)", so a failure names the
// answers and who gave them.
func describe(byVerdict map[string][]string) string {
	answers := make([]string, 0, len(byVerdict))
	for v := range byVerdict {
		answers = append(answers, v)
	}
	slices.Sort(answers)
	parts := make([]string, 0, len(answers))
	for _, v := range answers {
		slices.Sort(byVerdict[v])
		parts = append(parts, v+" ("+strings.Join(byVerdict[v], ", ")+")")
	}
	return strings.Join(parts, ", ")
}
