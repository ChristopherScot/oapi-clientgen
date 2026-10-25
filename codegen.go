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

// OpenAPITypescriptVersion is the openapi-typescript release every
// generated TypeScript client is built with.
//
// Pinned exactly rather than to a major. `openapi-typescript@7`
// resolves to whatever 7.x is current at run time, so the same spec
// could produce a different schema.d.ts on two different days - the
// property the ogen pin exists to prevent. v7 is the ceiling because
// v8 requires TypeScript ^5 in the consuming project.
const OpenAPITypescriptVersion = "7.4.4"

// OgenModule is the module path of ogen's command.
const OgenModule = "github.com/ogen-go/ogen/cmd/ogen"

// OgenCommand is the `go run` invocation of the pinned ogen.
func OgenCommand() string { return OgenModule + "@" + OgenVersion }

// TypescriptCommand returns the npx argv for the pinned
// openapi-typescript, reading spec and writing out.
//
// Still here for a caller that wants types only. The scaffolded client
// uses HeyAPICommand, which emits named operations as well.
//
// --yes so a CI runner with no prior install does not stop on npx's
// confirmation prompt.
func TypescriptCommand(spec, out string) []string {
	return []string{"npx", "--yes", "openapi-typescript@" + OpenAPITypescriptVersion, spec, "-o", out}
}

// HeyAPICommand generates the TypeScript client, reading its settings
// from openapi-ts.config.ts in dir.
//
// Config file rather than flags, so the settings live beside the
// generated code the way ogen.yml does - and so a reader can see what
// produced it without going through this tool.
func HeyAPICommand() []string {
	// typescript is a peer dependency hey-api loads at run time, and
	// npx does not install peers: without it the generator dies on
	// "Cannot read properties of undefined (reading 'AnyKeyword')",
	// which names neither hey-api nor the missing package. -p for each
	// so npx provisions both before running the binary.
	return []string{
		"npx", "--yes",
		"-p", "@hey-api/openapi-ts@" + HeyAPIVersion,
		"-p", "typescript@" + TypescriptVersion,
		"openapi-ts",
	}
}

// HeyAPICommandIn runs HeyAPICommand with dir as the working
// directory.
//
// Through sh because the runner executes one argv in the SERVICE
// directory, and hey-api resolves its config relative to where it
// runs. Quoted so a directory with a space in it does not become two
// arguments.
func HeyAPICommandIn(dir string) []string {
	return []string{"sh", "-c", "cd '" + dir + "' && " + strings.Join(HeyAPICommand(), " ")}
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
func HeyAPIConfig(input string) string {
	return `// Generated by homelabctl. How the TypeScript client is produced.
//
// output.path is a directory hey-api CLEARS on every run, so it holds
// nothing but generated code; the package entry and the resilience
// layer sit above it.
export default {
  input: ` + strconv.Quote(input) + `,
  output: { path: './generated', importFileExtension: 'ts', postProcess: [] },
}
`
}
