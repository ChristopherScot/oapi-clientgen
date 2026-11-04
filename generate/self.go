package generate

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ChristopherScot/oapi-clientgen/apiclient"
	"github.com/ChristopherScot/oapi-clientgen/codegen"
	"gopkg.in/yaml.v3"
)

// Self generates everything a service's own spec implies: the server
// scaffolding it implements, the typed client its consumers import,
// and the resilience layer beside both.
//
// A service that owns its spec needs none of what Generate does first.
// There is nothing to fetch, nothing to vendor against a third party
// going down, and nothing to patch - a spec you own is fixed at the
// source. What is left is the generating, which is why this takes a
// path rather than a manifest.
//
// It runs from the service's own go:generate, so CI's staleness gate
// catches a spec change that was not regenerated. A generator invoked
// from a Makefile step alone escapes that gate.
func (g Generator) Self(dir, specPath string, opts SelfOptions) error {
	raw, err := os.ReadFile(filepath.Join(dir, specPath))
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("reading %s: %w", specPath, err)
	}

	name := opts.Name
	apiVersion := "unknown"
	if info, ok := doc["info"].(map[string]any); ok {
		if v, ok := info["version"].(string); ok && v != "" {
			apiVersion = v
		}
		if name == "" {
			if t, ok := info["title"].(string); ok {
				name = t
			}
		}
	}
	if name == "" {
		name = PackageFromDir(opts.GoOut)
	}

	if !opts.SupportOnly {
		if err := g.runOgenForSelf(dir, specPath, opts); err != nil {
			return err
		}
	}
	if err := g.goWrapper(filepath.Join(dir, opts.GoOut), filepath.Join(dir, specPath), name, opts.Package, apiVersion); err != nil {
		return err
	}

	// The TypeScript client is published, so it lives in its own
	// directory with its own package.json rather than beside the Go
	// one. A service without one says so by leaving TSOut empty.
	if opts.TSOut == "" {
		return nil
	}
	return g.tsSelf(dir, specPath, name, apiVersion, opts)
}

// tsSelf generates and wraps the published TypeScript client, which
// is built at its own package root rather than vendored into the
// repo that generated it.
func (g Generator) tsSelf(dir, specPath, name, apiVersion string, opts SelfOptions) error {
	out := filepath.Join(dir, opts.TSOut)
	if !opts.SupportOnly {
		// AtRoot, not NestedIn: this client is published, and a
		// published client's relative imports carry .ts so tsc can
		// rewrite them to .js on the way into dist. The nested config
		// omits the extension, which is right for a client a
		// consumer compiles in place and wrong for one that ships
		// compiled.
		//
		// Written beside the client and left there, unlike the Go
		// path's temp config: hey-api resolves output.path relative
		// to the config, so a temp path would put generated/ under
		// /var/folders. Rewritten every run, and it carries the
		// generated header, so an edit here does not survive.
		rel, err := filepath.Rel(out, filepath.Join(dir, specPath))
		if err != nil {
			return err
		}
		cfgPath := filepath.Join(out, "openapi-ts.config.ts")
		if err := os.WriteFile(cfgPath, []byte(codegen.HeyAPIConfigAtRoot(rel)), 0o644); err != nil {
			return err
		}

		// Not codegen.HeyAPICommandIn, which builds an `sh -c "cd
		// '<dir>' && ..."` for a caller that cannot set a working
		// directory: a directory name holding a quote escapes that
		// quoting. runIn sets cmd.Dir and execs argv directly.
		if err := runIn("generating the TypeScript client for "+name, out, codegen.HeyAPICommand("openapi-ts.config.ts")...); err != nil {
			return err
		}
		if err := apiclient.MarkGenerated(filepath.Join(out, "generated")); err != nil {
			return err
		}
	}
	return g.tsSelfWrapper(out, filepath.Join(dir, specPath), name, apiVersion, opts)
}

// SelfOptions is what a service has to say that its spec does not.
type SelfOptions struct {
	// GoOut is where the server and client go, relative to dir.
	GoOut string

	// Package is the Go package name. Defaults to GoOut's last element.
	Package string

	// Name is the API's name for doc comments. Defaults to the spec's
	// info.title, then to the package name.
	Name string

	// TSOut is where the published TypeScript client goes, relative
	// to dir. Empty for a service that does not publish one.
	//
	// The directory must already hold the package.json and tsconfig
	// the client ships with: the generated resilience layer reads its
	// version from that package.json, and its imports carry '.ts' on
	// the assumption that tsconfig rewrites them.
	TSOut string

	// NPMPackage is how a consumer imports the TypeScript client.
	// Required with TSOut: a published client's imports carry it.
	NPMPackage string

	// SupportOnly skips the generators and writes only the resilience
	// layer, for a service that still runs ogen from its own
	// go:generate. A migration aid: remove it once none do.
	SupportOnly bool
}

// runOgenForSelf generates the server and client from a spec the
// service owns.
//
// otel is off. ogen instruments every operation by default, which
// costs a consumer 13 modules and ~1.5MB of binary and produces spans
// that go nowhere unless a TracerProvider exists and a collector is
// there to receive them. Neither is true in this cluster. Turn it back
// on in the same commit that adds one - here, once, rather than in
// every service.
//
// Metrics and request logging do not come from ogen. They are the
// service's own middleware, keyed on the spec's operation IDs.
//
// No ignore_not_implemented: a spec you own is fixed when ogen cannot
// express it, so skipping an operation would hide a problem you can
// correct. A third-party config keeps it for the opposite reason -
// see thirdPartyOgenConfig.
//
// That config is also written to ogen.yml beside the output and left
// for the author to edit. This one is composed into a temp file every
// run, which is the bigger difference: there is one right answer for
// every service built this way, and a file per service is a copy that
// drifts.
func (g Generator) runOgenForSelf(dir, specPath string, opts SelfOptions) error {
	out := filepath.Join(dir, opts.GoOut)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", opts.GoOut, err)
	}

	pkg := opts.Package
	if pkg == "" {
		pkg = PackageFromDir(opts.GoOut)
	}

	cfg, err := os.CreateTemp("", "ogen-*.yml")
	if err != nil {
		return err
	}
	defer os.Remove(cfg.Name())
	if _, err := cfg.WriteString(selfOgenConfig); err != nil {
		return err
	}
	if err := cfg.Close(); err != nil {
		return err
	}

	// The resilience layer is written after ogen, so --clean does not
	// take it.
	argv := codegen.OgenArgv(cfg.Name(), out, pkg, filepath.Join(dir, specPath))
	return runIn("generating the server and client for "+pkg, dir, argv...)
}

const selfOgenConfig = `# Generator settings for a spec this service owns.
# Composed by oapi-clientgen; see runOgenForSelf for why.
generator:
  features:
    disable:
      - 'ogen/otel'
`

// tsSelfWrapper writes the resilience layer for a published client,
// which differs from a vendored one in how a consumer imports it:
// by package name rather than by path.
func (g Generator) tsSelfWrapper(out, specPath, name, apiVersion string, opts SelfOptions) error {
	paged, err := pagedOperations(specPath)
	if err != nil {
		return err
	}
	if err := g.syncSupportFiles(out, apiclient.TypeScript, apiclient.Params{
		Name: name, Version: apiVersion, Paged: paged,
		ImportPath: opts.NPMPackage,
		Published:  true,
	}); err != nil {
		return err
	}
	g.logf("  ts: %s\n", opts.TSOut)
	return nil
}
