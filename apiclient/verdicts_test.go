package apiclient_test

import (
	"strings"
	"testing"
)

// A failing test's own output cannot pass itself off as an answer.
//
// Assertion messages quote the inputs they were given, so a runner's
// stream carries text shaped like the recorder's. Reading that as the
// client's answer would invent a disagreement on a header the clients
// agreed about, which is worse than missing one: the message names a
// header and two verdicts, and is specific enough to be believed.
func TestAFailingTestsOutputIsNotAnAnswer(t *testing.T) {
	recorded := `PARITY {"+9223372036854775808":"STOP"}`
	echoed := []string{
		`  ✖ Retry-After reads a signed delay and the cap`,
		`    expected "WAIT 25000ms" but got "STOP"`,
		`    PARITY {"+9223372036854775808":"WAIT 25000ms"}`,
	}

	got := verdicts(t, "ts", recorded+"\n"+strings.Join(echoed, "\n")+"\n")
	if v := got["+9223372036854775808"]; v != "STOP" {
		t.Errorf("recorded %q, want the recorder's own %q", v, "STOP")
	}
	if len(got) != 1 {
		t.Errorf("read %d answers, want the 1 the recorder printed: %v", len(got), got)
	}
}

// An unindented impostor cannot win by arriving first either.
//
// A bare Printf reaches stdout unindented, and the recorder is the
// last test in its file, so under a reader that took the first
// document an impostor would have won. Refusing a second rather than
// ranking them closes that whichever arrives first.
func TestASecondVerdictLineIsFatal(t *testing.T) {
	out := "PARITY {\"2\":\"WAIT 9999ms\"}\nPARITY {\"2\":\"WAIT 2000ms\"}\n"
	if !fatals(func(t *testing.T) { verdicts(t, "go", out) }) {
		t.Error("picked one of two PARITY documents instead of refusing both")
	}
}

// Output with no answers in it is not an empty set of answers.
func TestNoVerdictLineIsFatal(t *testing.T) {
	if !fatals(func(t *testing.T) { verdicts(t, "go", "ok  \tprobe\t0.1s\n") }) {
		t.Error("read answers from output that had none")
	}
}

// A truncated or malformed object is a harness that stopped working,
// not a client that answered nothing.
func TestAnUnreadableVerdictLineIsFatal(t *testing.T) {
	if !fatals(func(t *testing.T) { verdicts(t, "go", "PARITY {\"2\":\n") }) {
		t.Error("accepted a malformed PARITY line")
	}
}

// fatals reports whether f failed the test it was given.
//
// Its own T, on its own goroutine, because Fatalf ends a goroutine
// rather than returning: called on ours it would take the whole
// binary with it. Nothing here calls Fatalf on the T under test, so
// the rule against failing a test from another goroutine holds.
func fatals(f func(*testing.T)) bool {
	done := make(chan bool)
	go func() {
		inner := &testing.T{}
		defer func() { done <- inner.Failed() }()
		f(inner)
	}()
	return <-done
}
