package apiclient_test

import (
	"sort"
	"strings"
	"testing"
)

// Waits far enough apart to be a disagreement must stay one, and two
// clocks reading the same deadline must not become one.
func TestWaitsMergeWithinClockSkewOnly(t *testing.T) {
	for _, c := range []struct {
		name   string
		given  map[string][]string
		groups int
	}{
		{
			"two clocks fifteen seconds apart are one answer",
			map[string][]string{"WAIT 24727ms": {"go"}, "WAIT 9727ms": {"ts"}},
			1,
		},
		{
			"waits beyond the tolerance disagree",
			map[string][]string{"WAIT 2000ms": {"go"}, "WAIT 120000ms": {"ts"}},
			2,
		},
		{
			"a wait never agrees with a stop",
			map[string][]string{"WAIT 2000ms": {"go"}, "STOP": {"ts"}},
			2,
		},
		{
			"a wait never agrees with a fallback",
			map[string][]string{"WAIT 0ms": {"go"}, "FALLBACK": {"ts"}},
			2,
		},
		{
			"stop never agrees with fallback",
			map[string][]string{"STOP": {"go"}, "FALLBACK": {"ts"}},
			2,
		},
		{
			// The ends are 180s apart, so they are a real
			// disagreement however the middle is grouped.
			"a wait within reach of two groups does not join them",
			map[string][]string{"WAIT 0ms": {"go"}, "WAIT 90000ms": {"ts"}, "WAIT 180000ms": {"rb"}},
			2,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := mergeWaitsWithinSkew(c.given)
			if len(got) != c.groups {
				t.Errorf("got %d answers %v, want %d", len(got), keysOf(got), c.groups)
			}
			if total := countLangs(got); total != countLangs(c.given) {
				t.Errorf("%d clients in, %d out: merging lost or invented one", countLangs(c.given), total)
			}
		})
	}
}

// Same input, same answer, however the map is walked.
func TestMergingWaitsDoesNotDependOnMapOrder(t *testing.T) {
	given := map[string][]string{
		"WAIT 0ms": {"go"}, "WAIT 90000ms": {"ts"}, "WAIT 180000ms": {"rb"},
	}
	first := strings.Join(keysOf(mergeWaitsWithinSkew(given)), "|")
	for i := 0; i < 500; i++ {
		if got := strings.Join(keysOf(mergeWaitsWithinSkew(given)), "|"); got != first {
			t.Fatalf("run %d grouped as %s, the first run as %s", i, got, first)
		}
	}
}

func keysOf(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func countLangs(m map[string][]string) int {
	n := 0
	for _, langs := range m {
		n += len(langs)
	}
	return n
}
