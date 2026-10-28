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
