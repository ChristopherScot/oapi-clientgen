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

// HeyAPICommandIn runs the generator for a client rooted at dir,
// whose config and output live inside it.
//
// Through sh because the runner executes one argv in the SERVICE
// directory. The cd no longer decides anything - npx would leave it
// anyway - but it keeps relative paths in any hook meaning what they
// say. Quoted so a directory with a space in it does not become two
// arguments.
func HeyAPICommandIn(dir string) []string {
	cfg := filepath.Join(dir, "openapi-ts.config.ts")
	return []string{"sh", "-c", "cd '" + dir + "' && " + strings.Join(HeyAPICommand(cfg), " ")}
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

// HeyAPIConfig is the openapi-ts.config.ts for a client generated
// from the spec at input.
//
// One function rather than a template and a string literal, because
// the OUTPUT half must not drift: './generated' is a directory hey-api
// clears on every run, so the hand-written files sit above it, and
// importFileExtension 'ts' matches the TypeScript sources the package
// actually ships. A client whose layout disagreed with the other
// clients would be wrong in a way nothing checks.
//
// A plain object rather than hey-api's defineConfig(): the generator
// runs through npx, whose temporary install is not on this file's
// module path, so importing from it fails with a MODULE_NOT_FOUND
// that blames the config.
func HeyAPIConfig(input, outDir string) string {
	return `// Generated by homelabctl. How the TypeScript client is produced.
//
// output.path is a directory hey-api CLEARS on every run, so it holds
// nothing but generated code; the package entry and the resilience
// layer sit above it.
//
// Both paths are absolute. npx runs the generator from the nearest
// package.json rather than from the directory it was invoked in, so
// a relative path here resolves somewhere else entirely - for a
// client nested inside an app, into the app root.
export default {
  input: ` + strconv.Quote(input) + `,
  output: { path: ` + strconv.Quote(outDir) + `, importFileExtension: 'ts', postProcess: [] },
}
`
}
