// Package codegen holds what every OpenAPI generation in this tool
// agrees on: which generators run, at which versions.
//
// There are two callers and they are not variations of one another. A
// scaffolded service owns its spec, so its generator runs from a
// //go:generate directive its own CI re-runs and diffs. A third-party
// client does not own its spec, so its generator runs from this tool.
// Those stay separate.
//
// What they must NOT disagree on is the generator versions. When the
// ogen version lived in both the template and the client command, the
// same spec could produce different code depending on which command a
// person reached for - and nothing compared the two numbers.
//
// The ogen.yml each one writes is deliberately NOT shared, even though
// both currently disable 'ogen/otel'. A scaffolded service disables it
// because its own middleware already does metrics and logging; a
// third-party client disables it so a consumer does not inherit 13
// modules it never asked for. Those are different reasons that happen
// to agree today: adding tracing to our services should flip the
// scaffold without touching generated third-party clients. Sharing the
// value would make that one edit break the other caller silently.
package codegen

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ChristopherScot/homelabctl/internal/apiclient"
)

// OgenVersion is the ogen release every generated Go client is built
// with.
//
// Pinned exactly: with @latest the build breaks on the day ogen
// publishes, in a file nobody touched.
const OgenVersion = "v1.24.0"

// HeyAPIVersion is the @hey-api/openapi-ts release every generated
// TypeScript client is built with.
//
// hey-api rather than openapi-typescript because it emits a NAMED
// function per operationId - listPokemon({...}) - which is the shape
// ogen already gives the Go client. The two halves of one generated
// client disagreeing about how you call them is the kind of thing
// nobody notices until they have written both.
const HeyAPIVersion = "0.99.0"

// TypescriptVersion is hey-api's peer dependency, pinned for the same
// reason as the generators themselves: a floating major would change
// generated output on a day nobody touched the spec.
const TypescriptVersion = "5.9.3"

// OgenModule is the module path of ogen's command.
const OgenModule = "github.com/ogen-go/ogen/cmd/ogen"

// OgenCommand is the `go run` invocation of the pinned ogen.
func OgenCommand() string { return OgenModule + "@" + OgenVersion }

// HeyAPICommand generates the TypeScript client, reading its settings
// from openapi-ts.config.ts in dir.
//
// Config file rather than flags, so the settings live beside the
// generated code the way ogen.yml does - and so a reader can see what
// produced it without going through this tool.
func HeyAPICommand(configPath string) []string {
	// typescript is a peer dependency hey-api loads at run time, and
	// npx does not install peers: without it the generator dies on
	// "Cannot read properties of undefined (reading 'AnyKeyword')",
	// which names neither hey-api nor the missing package. -p for each
	// so npx provisions both before running the binary.
	//
	// -f names the config instead of letting hey-api look for one.
	// npx chdirs to the nearest package.json before running, so the
	// search starts there, and a client generated into a subdirectory
	// of an app would never be found. What the generator does then is
	// not to complain: it builds an empty config and dies inside its
	// own defaulting with "Cannot read properties of undefined
	// (reading 'importFileExtension')".
	return []string{
		"npx", "--yes",
		"-p", "@hey-api/openapi-ts@" + HeyAPIVersion,
		"-p", "typescript@" + TypescriptVersion,
		"openapi-ts", "-f", configPath,
	}
}

// HeyAPICommandIn runs the generator for a client that sits at its
// own package root, named relative to the service directory.
//
// The cd is load-bearing twice over. It is what puts npx's search for
// a package.json inside the CLIENT rather than the repo above it,
// which is what makes the committed config's relative paths resolve
// against the client - and it is why the config can be named by its
// bare filename. Quoted so a directory with a space in it does not
// become two arguments.
func HeyAPICommandIn(dir string) []string {
	return []string{"sh", "-c", "cd '" + dir + "' && " + strings.Join(HeyAPICommand("openapi-ts.config.ts"), " ")}
}

// npx runs a binary from the nearest package.json, NOT from the
// directory it was invoked in. Every path in a generated config, and
// the path naming the config itself, has to survive that move, and
// the two callers sit differently relative to that package.json - so
// each gets a constructor that owns one answer rather than a pair of
// strings a caller has to get right.
//
// Getting it wrong does not fail loudly. hey-api finds no config,
// builds an empty one and dies inside its own defaulting with
// "Cannot read properties of undefined (reading
// 'importFileExtension')" - or it succeeds and writes the client
// somewhere else entirely.
//
// A plain object rather than hey-api's defineConfig(): the generator
// runs through npx, whose temporary install is not on this file's
// module path, so importing from it fails with a MODULE_NOT_FOUND
// that blames the config.
func heyAPIConfig(input, outDir string, published bool) string {
	ext := ""
	if published {
		ext = ", importFileExtension: 'ts'"
	}
	return apiclient.Header("//") + `//
// How the TypeScript client is produced. output.path is a directory
// hey-api CLEARS on every run, so it holds nothing but generated
// code; the package entry and the resilience layer sit above it.
export default {
  input: ` + strconv.Quote(input) + `,
  output: { path: ` + strconv.Quote(outDir) + ext + `, postProcess: [] },
}
`
}

// HeyAPIConfigAtRoot is the config for a client that sits at its own
// package root - the scaffolded case, where npx lands in the client.
//
// Relative paths are REQUIRED here, not merely adequate: this file is
// committed to the service's repo and read on machines that never saw
// the one that scaffolded it, where an absolute path would name
// nothing.
//
// Published, so imports carry '.ts': the client owns the tsconfig
// that rewrites them into dist/. See apiclient.Params.Published.
func HeyAPIConfigAtRoot(specPath string) string {
	return heyAPIConfig(specPath, "./generated", true)
}

// HeyAPIConfigNestedIn is the config for a client vendored INSIDE
// another package, at clientDir - the third-party case.
//
// Absolute throughout, because npx moves to the host application's
// package.json before the generator reads any of it. A relative
// output path resolved from there once wrote an entire client into
// the host app's root and reported success.
//
// The config's own path comes back with it, absolute for the same
// reason: building that path at the call site is what produced a -f
// argument pointing at a directory the generator had already left.
//
// Not published: this client is compiled by a tsconfig it does not
// own, where a '.ts' import is TS5097 unless the host repo opts in,
// which is not ours to ask.
func HeyAPIConfigNestedIn(clientDir, specPath string) (cfgPath, contents string, err error) {
	absDir, err := filepath.Abs(clientDir)
	if err != nil {
		return "", "", err
	}
	absSpec, err := filepath.Abs(specPath)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(absDir, "openapi-ts.config.ts"),
		heyAPIConfig(absSpec, filepath.Join(absDir, "generated"), false),
		nil
}

// Swagger2OpenAPIVersion converts a Swagger 2.0 document to OpenAPI
// 3.0, which ogen requires and openapi-typescript prefers.
//
// Only third-party specs need this: a spec this tool scaffolds is
// written as 3.x from the start. It lives here rather than beside the
// client command because it is a generator version like the others -
// the thing that must not float.
const Swagger2OpenAPIVersion = "7.0.8"

// Swagger2OpenAPICommand converts in to out.
func Swagger2OpenAPICommand(in, out string) []string {
	return []string{"npx", "--yes", "swagger2openapi@" + Swagger2OpenAPIVersion, in, "-o", out}
}
