package apiclient_test

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherScot/oapi-clientgen/apiclient"
)

// Every shared number reaches both languages, and reaches them from
// the declaration rather than from a literal that happens to match it.
//
// One field at a time, over whatever the struct holds: a field added
// later is covered the moment it is declared, where a list written out
// here would pass until someone remembered to extend it.
//
// Each field is rendered twice, before and after, and the two outputs
// must differ: a template carrying a literal renders the same thing
// both times, which asserting against today's values cannot see.
//
// Differing, not containing the moved number. The Go template renders
// a millisecond count in the largest unit that divides it, so the
// moved value reaches the output as "50 * time.Second" rather than as
// its own digits, and a search for those digits would report a
// literal that is not there.
func TestBothLanguagesRenderTheSharedDefaults(t *testing.T) {
	p := apiclient.Params{
		Name: "probe", Version: "1.0.0", Package: "api",
		ImportPath: "example.com/probe/api",
	}

	// The recorders cap Retry-After themselves, so a cap of their own
	// would let the parity harness judge the clients by a figure the
	// clients do not use.
	clients := []string{"go_client.go.tmpl", "ts_resilience.ts.tmpl"}
	recorders := map[string][]string{
		"MaxRetryAfterMs": {"go_client_test.go.tmpl", "ts_resilience_test.ts.tmpl"},
	}

	base := apiclient.Defaults()
	v := reflect.ValueOf(base)
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		if v.Field(i).Kind() != reflect.Int {
			t.Run(name, func(t *testing.T) {
				t.Skipf("%s is a %s; this only knows how to move an int", name, v.Field(i).Kind())
			})
			continue
		}
		t.Run(name, func(t *testing.T) {
			moved := reflect.New(v.Type()).Elem()
			moved.Set(v)
			moved.Field(i).SetInt(v.Field(i).Int() * 10)

			for _, tmpl := range slices.Concat(clients, recorders[name]) {
				was, err := apiclient.RenderWith(tmpl, p, base)
				if err != nil {
					t.Fatal(err)
				}
				now, err := apiclient.RenderWith(tmpl, p, moved.Interface().(apiclient.SharedDefaults))
				if err != nil {
					t.Fatal(err)
				}
				if was == now {
					t.Errorf("%s renders the same output with %s moved: a literal is standing in for it", tmpl, name)
				}
			}
		})
	}
}

// The two wait forms are the same sentence, so a change to one has to
// be a change to both.
//
// Adjacency in the declaration makes that easy to get right; it does
// not make getting it wrong legible. Drifting GoWait to "WAITED %dms"
// reports five Retry-After cases as client disagreements, pointing a
// reader at the parsers rather than at the format string.
func TestTheTwoWaitFormsAreTheSameSentence(t *testing.T) {
	v := apiclient.Verdicts()
	// What is left once each language's way of saying "the number"
	// is taken out.
	shape := func(s, verb string) string { return strings.Replace(s, verb, "", 1) }
	if g, ts := shape(v.GoWait, "%d"), shape(v.TSWait, "${ms}"); g != ts {
		t.Errorf("GoWait %q and TSWait %q say different things: %q against %q",
			v.GoWait, v.TSWait, g, ts)
	}
}

// The Go client expresses a duration in the unit a Go author would
// have chosen: 30 * time.Second, never 30000 * time.Millisecond.
//
// Both are the same duration, so nothing downstream can tell them
// apart and no other test here would notice the difference. What
// notices is the next person to read the client, who has to divide
// before they know how long a timeout is.
//
// The shared defaults are in milliseconds because that is the unit
// TypeScript has, which is what makes the wrong form so easy to
// render.
func TestTheGoClientWritesDurationsInNaturalUnits(t *testing.T) {
	p := apiclient.Params{
		Name: "probe", Version: "1.0.0", Package: "api",
		ImportPath: "example.com/probe/api",
	}

	for _, tmpl := range []string{"go_client.go.tmpl", "go_client_test.go.tmpl"} {
		out, err := apiclient.RenderWith(tmpl, p, apiclient.Defaults())
		if err != nil {
			t.Fatal(err)
		}
		// A whole number of seconds left in milliseconds. Four or more
		// digits, because 100 * time.Millisecond is already natural
		// and 1000 is not.
		for _, m := range regexp.MustCompile(`\b(\d+)000 \* time\.Millisecond\b`).FindAllString(out, -1) {
			t.Errorf("%s renders %q; a whole number of seconds belongs in time.Second", tmpl, m)
		}
	}
}

// The expression goDuration writes means the wait it was given.
//
// Choosing a prettier unit is a rewrite of the number, and a rewrite
// that drops the division still renders a plausible-looking
// expression: 5000 * time.Second reads fine and is eighty minutes.
// Evaluating it here costs nothing; the generated suite catches it
// only by waiting.
func TestAGoDurationMeansTheMillisecondsItWasGiven(t *testing.T) {
	units := map[string]time.Duration{
		"time.Hour":        time.Hour,
		"time.Minute":      time.Minute,
		"time.Second":      time.Second,
		"time.Millisecond": time.Millisecond,
	}

	ms := []int{1, 100, 999, 1000, 1500, 5000, 10000, 30000, 60000, 90000, 3600000}
	for _, in := range ms {
		got := apiclient.GoDuration(in)
		var n int64
		var unit string
		if _, err := fmt.Sscanf(got, "%d * %s", &n, &unit); err != nil {
			t.Errorf("goDuration(%d) = %q, which is not a <count> * <unit> expression", in, got)
			continue
		}
		u, ok := units[unit]
		if !ok {
			t.Errorf("goDuration(%d) = %q, whose unit is not a time constant", in, got)
			continue
		}
		if want := time.Duration(in) * time.Millisecond; time.Duration(n)*u != want {
			t.Errorf("goDuration(%d) = %q, which is %s, not %s", in, got, time.Duration(n)*u, want)
		}
	}
}
