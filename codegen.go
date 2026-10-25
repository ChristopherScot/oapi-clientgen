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

// OgenVersion is the ogen release every generated Go client is built
// with.
//
// Pinned exactly: with @latest the build breaks on the day ogen
// publishes, in a file nobody touched.
const OgenVersion = "v1.24.0"

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
// --yes so a CI runner with no prior install does not stop on npx's
// confirmation prompt.
func TypescriptCommand(spec, out string) []string {
	return []string{"npx", "--yes", "openapi-typescript@" + OpenAPITypescriptVersion, spec, "-o", out}
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
