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
func fatals(f func(*testing.T)) bool {
	done := make(chan bool)
	go func() {
		inner := &testing.T{}
		defer func() { done <- inner.Failed() }()
		f(inner)
	}()
	return <-done
}
