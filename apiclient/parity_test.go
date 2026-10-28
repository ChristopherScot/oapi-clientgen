package apiclient_test

import (
	"os/exec"
	"sort"
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
	if _, err := exec.LookPath("node"); err != nil {
		// One client cannot answer, and the rest would agree with
		// each other about everything.
		t.Skip("no node, so only one client can be asked")
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
	var mu sync.Mutex
	answers := map[string]map[string]string{}
	t.Run("clients", func(t *testing.T) {
		for lang, run := range clients {
			t.Run(lang, func(t *testing.T) {
				t.Parallel()
				got := run(t)
				mu.Lock()
				defer mu.Unlock()
				answers[lang] = got
			})
		}
	})

	// A -run that selects one client leaves nothing to compare it
	// with, which is a filter this test cannot honour rather than a
	// disagreement - so it says which clients are missing instead of
	// reporting a count.
	if len(answers) != len(clients) {
		missing := make([]string, 0, len(clients))
		for lang := range clients {
			if _, ok := answers[lang]; !ok {
				missing = append(missing, lang)
			}
		}
		sort.Strings(missing)
		t.Fatalf("no answers from %s, so there was nothing to compare; this test needs every client, and -run must not select a subset of them",
			strings.Join(missing, " and "))
	}

	langs := make([]string, 0, len(answers))
	for lang := range answers {
		langs = append(langs, lang)
	}
	sort.Strings(langs)

	for lang, got := range answers {
		for _, header := range apiclient.ParityCases() {
			if _, ok := got[header]; !ok {
				t.Fatalf("the %s client did not answer Retry-After %q; it was rendered every case in apiclient.ParityCases()", lang, header)
			}
		}
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
		case len(byVerdict) == 1:
		case excepted:
			t.Logf("Retry-After %q differs on purpose: %s (%s)", header, describe(byVerdict), why)
		default:
			t.Errorf("Retry-After %q: %s", header, describe(byVerdict))
		}
	}
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
			t.Errorf("%s printed a PARITY line with no verdict: %q", lang, line)
			return nil
		}
		header, err := strconv.Unquote(quoted)
		if err != nil {
			t.Errorf("%s printed an unreadable header %q: %v", lang, quoted, err)
			return nil
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
