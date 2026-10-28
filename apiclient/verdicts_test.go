package apiclient_test

import (
	"strings"
	"testing"
)

// What the harness must refuse, and what it must say about it. The
// wording is asserted because this harness exists to name the right
// culprit: a message blaming the client for a broken recorder sends
// the next reader into the wrong file.
func TestParseVerdictsRefusesWhatIsNotAnAnswer(t *testing.T) {
	// The recorder's own line, at column 0.
	const recorded = `PARITY {"2":"WAIT 2000ms"}`

	// A case the parser must accept rather than complain about.
	const accepted = ""

	for _, c := range []struct {
		name string
		out  string
		want string
	}{
		{
			// Assertion output is indented by both runners, so an
			// echo of these answers is disqualified by that alone.
			"a failing assertion echoing the answers",
			recorded + "\n  ✖ Retry-After reads a signed delay\n    PARITY {\"2\":\"WAIT 9999ms\"}\n",
			accepted,
		},
		{
			// A bare Printf is not indented, and the recorder is the
			// last test in its file, so an impostor's line arrives
			// first. Refusing rather than ranking is what closes it.
			"an unindented impostor arriving first",
			"PARITY {\"2\":\"WAIT 9999ms\"}\n" + recorded + "\n",
			"printed a second PARITY line",
		},
		{
			// An empty document is still a document. Skipping it
			// would hand the choice to whatever came next, which is
			// the ranking this refuses to do.
			"an empty impostor arriving first",
			"PARITY {}\n" + recorded + "\n",
			"printed a second PARITY line",
		},
		{
			"a null impostor arriving first",
			"PARITY null\n" + recorded + "\n",
			"printed a second PARITY line",
		},
		{"no PARITY line at all", "ok  \tprobe\t0.1s\n", "printed no answers"},
		{"an empty document", "PARITY {}\n", "printed no answers"},
		{"a null document", "PARITY null\n", "printed no answers"},
		{"a truncated document", "PARITY {\"2\":\n", "printed an unreadable PARITY line"},
		{"trailing prose after the document", recorded + " and then some\n", "printed an unreadable PARITY line"},
	} {
		t.Run(c.name, func(t *testing.T) {
			answers, err := parseVerdicts(c.out)
			switch {
			case c.want == accepted && err != nil:
				t.Fatalf("refused the recorder's own answers: %v", err)
			case c.want == accepted:
				if answers["2"] != "WAIT 2000ms" {
					t.Errorf("read %q, want the recorder's own %q", answers["2"], "WAIT 2000ms")
				}
			case err == nil:
				t.Fatalf("accepted it, reading %v", answers)
			case !strings.Contains(err.Error(), c.want):
				t.Errorf("complained %q, want something about %q", err, c.want)
			}
		})
	}
}
