// Package codegen pins which generators run, at which versions.
//
// The ogen.yml and openapi-ts.config.ts each caller writes are
// deliberately not shared, even where they agree today. A scaffolded
// service and a third-party client disable ogen/otel for different
// reasons, and sharing the value would make changing one silently
// change the other.
package codegen

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ChristopherScot/oapiclient-gen/apiclient"
)

// Generator versions are pinned exactly. With @latest a build breaks
// on the day an upstream publishes, in a file nobody touched.
const (
	OgenVersion   = "v1.24.0"
	HeyAPIVersion = "0.99.0"
	OgenModule    = "github.com/ogen-go/ogen/cmd/ogen"

	// TypescriptVersion is hey-api's peer dependency.
	TypescriptVersion = "5.9.3"

	// Swagger2OpenAPIVersion converts Swagger 2.0 to OpenAPI 3.0,
	// which ogen requires.
	Swagger2OpenAPIVersion = "7.0.8"
)

// OgenCommand is the `go run` invocation of the pinned ogen.
func OgenCommand() string { return OgenModule + "@" + OgenVersion }

// Swagger2OpenAPICommand converts in to out.
func Swagger2OpenAPICommand(in, out string) []string {
	return []string{"npx", "--yes", "swagger2openapi@" + Swagger2OpenAPIVersion, in, "-o", out}
}

// HeyAPICommand generates the TypeScript client, reading its settings
// from the config at configPath.
//
// typescript is a peer dependency npx will not install on its own, so
// both are provisioned with -p. -f names the config because npx
// chdirs to the nearest package.json, where hey-api's own search
// would otherwise start.
func HeyAPICommand(configPath string) []string {
	return []string{
		"npx", "--yes",
		"-p", "@hey-api/openapi-ts@" + HeyAPIVersion,
		"-p", "typescript@" + TypescriptVersion,
		"openapi-ts", "-f", configPath,
	}
}

// HeyAPICommandIn runs the generator for a client at its own package
// root, named relative to the service directory.
//
// Used by homelabctl, not by this tool: a client it scaffolds for a
// service publishes to npm and owns its own tsconfig, where a client
// generated from a third party's spec is vendored into a repo that
// does not.
//
// The cd puts npx's search for a package.json inside the client
// rather than the repo above it, which is what makes the committed
// config's relative paths resolve correctly.
func HeyAPICommandIn(dir string) []string {
	return []string{"sh", "-c", "cd '" + dir + "' && " + strings.Join(HeyAPICommand("openapi-ts.config.ts"), " ")}
}

// A plain object rather than hey-api's defineConfig(): the generator
// runs through npx, whose temporary install is not on this file's
// module path.
func heyAPIConfig(input, outDir string, published bool) string {
	ext := ""
	if published {
		ext = ", importFileExtension: 'ts'"
	}
	return apiclient.Header("//") + `//
// output.path is a directory hey-api CLEARS on every run, so it holds
// nothing but generated code; the package entry and the resilience
// layer sit above it.
export default {
  input: ` + strconv.Quote(input) + `,
  output: { path: ` + strconv.Quote(outDir) + ext + `, postProcess: [] },
}
`
}

// HeyAPIConfigAtRoot is the config for a client at its own package
// root, where npx lands in the client. Used by homelabctl; see
// HeyAPICommandIn.
//
// Paths are relative because this file is committed and read on
// machines that never saw the one that wrote it. Published, so
// imports carry '.ts': the client owns the tsconfig that rewrites
// them. See apiclient.Params.Published.
func HeyAPIConfigAtRoot(specPath string) string {
	return heyAPIConfig(specPath, "./generated", true)
}

// HeyAPIConfigNestedIn is the config for a client vendored inside
// another package, at clientDir.
//
// Absolute throughout, including the config's own returned path,
// because npx moves to the host application's package.json before
// the generator reads any of it.
//
// Not published: this client is compiled by a tsconfig it does not
// own, where a '.ts' import is TS5097 unless the host repo opts in.
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
