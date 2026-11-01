package apiclient_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/ChristopherScot/oapi-clientgen/apiclient"
)

// Every shared number reaches both languages, and reaches them from
// the declaration rather than from a literal that happens to match it.
//
// One field at a time, over whatever the struct holds: a field added
// later is covered the moment it is declared, where a list written out
// here would pass until someone remembered to extend it.
//
// Each field is rendered twice, before and after, because a template
// carrying a literal renders the same thing both times - which
// asserting against today's values cannot see. Counted rather than
// found, because a value can render at more than one site and
// containment lets one of them revert while the other keeps the test
// green.
func TestBothLanguagesRenderTheSharedDefaults(t *testing.T) {
	p := apiclient.Params{
		Name: "probe", Version: "1.0.0", Package: "api",
		ImportPath: "example.com/probe/api",
	}

	// Where each field is rendered, and how many times. Stated rather
	// than discovered, because the figures also occur as ordinary
	// numbers in these files - 5 appears thirteen times in the Go
	// client - so counting occurrences cannot tell a rendered value
	// from a coincidence.
	//
	// A field absent from this map is caught below: the clients must
	// render every field, so an entry nobody added fails there.
	sites := map[string]map[string]int{
		"TimeoutMs": {
			// A code line and the doc comment above it.
			"go_client.go.tmpl": 2, "ts_resilience.ts.tmpl": 2,
		},
		"MaxRetryAfterMs": {
			"go_client.go.tmpl": 1, "ts_resilience.ts.tmpl": 1,
			// The recorders cap Retry-After themselves. A cap of their
			// own would let the parity harness judge the clients by a
			// figure the clients do not use.
			"go_client_test.go.tmpl": 1, "ts_resilience_test.ts.tmpl": 1,
		},
	}
	const clients = 2 // the first two templates below
	templates := []string{
		"go_client.go.tmpl", "ts_resilience.ts.tmpl",
		"go_client_test.go.tmpl", "ts_resilience_test.ts.tmpl",
	}

	base := apiclient.Defaults()
	v := reflect.ValueOf(base)
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		if v.Field(i).Kind() != reflect.Int {
			continue
		}
		t.Run(name, func(t *testing.T) {
			// Odd and long, so it cannot be mistaken for an unrelated
			// number already in the output. The check below enforces
			// that rather than trusting it.
			moved := reflect.New(v.Type()).Elem()
			moved.Set(v)
			sentinel := v.Field(i).Int()*10 + 7
			moved.Field(i).SetInt(sentinel)
			s := strconv.Itoa(int(sentinel))

			for n, tmpl := range templates {
				was, err := apiclient.RenderWith(tmpl, p, base)
				if err != nil {
					t.Fatal(err)
				}
				now, err := apiclient.RenderWith(tmpl, p, moved.Interface().(apiclient.SharedDefaults))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(was, s) {
					t.Fatalf("%s already contains %s, so it cannot stand in for %s here", tmpl, s, name)
				}

				// One by default for a client, because every field has
				// to reach both; zero for a test template, which is
				// only held to what sites says it carries.
				want := 1
				if n >= clients {
					want = 0
				}
				if listed, ok := sites[name][tmpl]; ok {
					want = listed
				}
				if got := strings.Count(now, s); got != want {
					t.Errorf("%s renders %s %d times, want %d: a literal is standing in for it",
						tmpl, name, got, want)
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
