package apiclient_test

import (
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ChristopherScot/oapi-clientgen/apiclient"
)

// A Retry-After header every generated client answers differently on
// purpose, with the reason. Meant to stay empty: an entry is a promise
// that the disagreement is intended, not a note that one exists.
var parityExceptions = map[string]string{}

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

// parityRecord holds each client's answers, keyed by language so a
// third generator needs no change here.
var parityRecord = map[string]map[string]string{}

// recordParity stores one client's answers for the comparison. The
// harnesses call it because they are what can run a client.
//
// Every case is required, so a client answering a subset cannot
// quietly shrink what gets compared rather than failing.
func recordParity(t *testing.T, lang string, got map[string]string) {
	t.Helper()
	for _, header := range apiclient.ParityCases() {
		if _, ok := got[header]; !ok {
			t.Fatalf("the %s client did not answer Retry-After %q; it was rendered every case in apiclient.ParityCases()", lang, header)
		}
	}
	parityRecord[lang] = got
}

// TestAllClientsAgreeOnRetryAfter compares what every client answered.
//
// Reads records the harnesses write, so it has to run after them: Go
// runs tests in source order within a file and across files in file
// order, which is why this file's name sorts after theirs. A rename
// that broke the order would leave this with nothing to compare, so
// too few records is a failure here rather than a pass on an empty
// comparison - unless a harness said why it could not run a client,
// which is the one case where no record is the honest answer.
func TestAllClientsAgreeOnRetryAfter(t *testing.T) {
	if testing.Short() {
		t.Skip("compares what the rendered clients answered")
	}
	if len(parityUnavailable) > 0 && len(parityRecord) < 2 {
		t.Skipf("%d of %d clients ran: %s", len(parityRecord), len(parityRecord)+len(parityUnavailable),
			strings.Join(parityUnavailable, "; "))
	}
	if len(parityRecord) < 2 {
		t.Fatalf("%d client(s) recorded answers and none said why it could not run; a comparison needs two, so this compared nothing",
			len(parityRecord))
	}

	langs := make([]string, 0, len(parityRecord))
	for lang := range parityRecord {
		langs = append(langs, lang)
	}
	sort.Strings(langs)

	t.Logf("compared %d cases across %s", len(parityRecord[langs[0]]), strings.Join(langs, ", "))

	for _, header := range apiclient.ParityCases() {
		byVerdict := map[string][]string{}
		for _, lang := range langs {
			byVerdict[parityRecord[lang][header]] = append(byVerdict[parityRecord[lang][header]], lang)
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

// parityUnavailable records a client the harness could not run, and
// why. Distinguishes a missing toolchain from a comparison that
// stopped happening.
var parityUnavailable []string

func skipParity(lang, why string) { parityUnavailable = append(parityUnavailable, lang+": "+why) }

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
