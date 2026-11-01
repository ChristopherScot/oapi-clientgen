package apiclient

import "slices"

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
