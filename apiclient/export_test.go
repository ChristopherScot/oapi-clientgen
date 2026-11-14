package apiclient

import (
	"slices"
	"strings"
)

// ParityCases hands the external test the list the suites are
// rendered from, so it can check a client answered every case rather
// than only as many as its peers. Here rather than in apiclient.go
// because nothing outside the tests wants it.
//
// A copy, like Retired, so a caller cannot change which cases the
// suites are rendered.
func ParityCases() []string { return slices.Clone(parityCases) }

// Defaults is the numbers both templates render, for a test that
// checks they reached both.
func Defaults() SharedDefaults { return defaults }

// RenderWith renders one template with the shared numbers supplied, so
// a test can vary them and see whether the templates read them.
func RenderWith(name string, p Params, d SharedDefaults) (string, error) {
	return renderWith(name, p, d)
}

// Verdicts is the vocabulary both recorders render.
func Verdicts() VerdictWords { return verdictVocabulary }

// GoDuration is the millisecond-to-time.Duration rendering, for the
// tests that check what the Go templates write.
func GoDuration(ms int) string { return goDuration(ms) }

// GeneratedMarker is the phrase every generated file carries, for the
// test that checks each template wrote it. Here rather than in
// apiclient.go because nothing outside the tests wants it.
func GeneratedMarker() string { return generatedMarker }

// GoTemplates is every Go template this package renders, client and
// tests, as template name paired with the path it is written to.
//
// For the formatting gate, which has to read what a TEMPLATE produces
// rather than what Go() returns: Go() runs the output through gofmt,
// so a test reading it would be asking whether gofmt's output is
// gofmt-clean and could never fail.
func GoTemplates(p Params) map[string]string {
	out := map[string]string{}
	for _, set := range [][]templateFile{goFiles, goTestFiles} {
		for _, f := range set {
			// Skip what the generator would not write for these
			// params. go_paging_ops renders a header, a package
			// clause and two imports it never uses when nothing is
			// paged; `when` is why that is never written out, so a
			// test asking whether it is gofmt-clean has to honour the
			// same condition rather than guess from the content.
			if f.when != nil && !f.when(p) {
				continue
			}
			if strings.HasSuffix(f.path, ".go") {
				out[f.tmpl] = f.path
			}
		}
	}
	return out
}
