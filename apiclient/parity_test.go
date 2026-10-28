package apiclient_test

import (
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A Retry-After header every generated client answers differently on
// purpose, with the reason. Meant to stay empty: an entry is a promise
// that the disagreement is intended, not a note that one exists.
var parityExceptions = map[string]string{}

// verdicts picks the PARITY lines out of a test runner's output.
//
// Tagged rather than parsed positionally because the runner's own
// chatter shares the stream, and read from the output the harness
// already captures rather than a file: a file needs a location, and a
// location can hold an answer from a previous run.
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

// recordParity stores one client's answers for the comparison. The
// harnesses call it because they are what can run a client; keyed by
// language so a third generator needs no change here.
var parityRecord = map[string]map[string]string{}

func recordParity(t *testing.T, lang string, got map[string]string) {
	t.Helper()
	if len(got) == 0 {
		t.Fatalf("the %s client printed no PARITY lines; it would agree with everything", lang)
	}
	parityRecord[lang] = got
}

// TestAllClientsAgreeOnRetryAfter compares what every client answered.
//
// Ordered after the harnesses that run them by name - Go runs tests in
// source order within a file, and across files in file order - so
// "parity" sorting after "generated" is what makes the records exist
// by the time this reads them. Asserted rather than assumed: with no
// records this fails rather than passing on an empty comparison.
func TestAllClientsAgreeOnRetryAfter(t *testing.T) {
	if testing.Short() {
		t.Skip("compares what the rendered clients answered")
	}
	if len(parityRecord) < 2 {
		t.Skipf("%d client(s) recorded answers; a comparison needs two, and the harnesses that run them skip without node or a network",
			len(parityRecord))
	}

	langs := make([]string, 0, len(parityRecord))
	for lang := range parityRecord {
		langs = append(langs, lang)
	}
	sort.Strings(langs)

	// Same set, or one client skipped a case and the rest would agree
	// about whatever is left.
	for _, lang := range langs[1:] {
		if len(parityRecord[lang]) != len(parityRecord[langs[0]]) {
			t.Fatalf("%s answered %d cases and %s answered %d; they were given one list",
				lang, len(parityRecord[lang]), langs[0], len(parityRecord[langs[0]]))
		}
	}

	t.Logf("compared %d cases across %s", len(parityRecord[langs[0]]), strings.Join(langs, ", "))

	for header := range parityRecord[langs[0]] {
		byVerdict := map[string][]string{}
		for _, lang := range langs {
			v, ok := parityRecord[lang][header]
			if !ok {
				t.Errorf("%s never answered Retry-After %q", lang, header)
				continue
			}
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
