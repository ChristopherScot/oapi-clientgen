package apiclient

// ParityCases hands the external test the list the suites are
// rendered from, so it can check a client answered every case rather
// than only as many as its peers. Here rather than in apiclient.go
// because nothing outside the tests wants it.
func ParityCases() []string { return parityCases }
