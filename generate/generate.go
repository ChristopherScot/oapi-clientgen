package generate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ChristopherScot/oapi-clientgen/apiclient"
	"github.com/ChristopherScot/oapi-clientgen/apispec"
	"github.com/ChristopherScot/oapi-clientgen/codegen"
	"gopkg.in/yaml.v3"
)

// fetchTimeout bounds a spec download. A third party being slow should
// fail the command, not hang a build.
const fetchTimeout = 30 * time.Second

// Generator runs the pipeline. The zero value reports progress to
// stdout; set Log to send it somewhere else, or to io.Discard.
type Generator struct {
	Log io.Writer
}

func (g Generator) logf(format string, a ...any) {
	w := g.Log
	if w == nil {
		w = os.Stdout
	}
	fmt.Fprintf(w, format, a...)
}

// Generate runs the whole pipeline for one client with the default
// Generator: obtain the spec, patch it into a state the generators
// accept, then generate.
func Generate(dir, name string, c Client, refresh bool) error {
	return Generator{}.Generate(dir, name, c, refresh)
}

// Generate runs the whole pipeline for one client: obtain the
// spec, patch it into a state the generators accept, then generate.
//
// The patched spec is written to a temporary file rather than over the
// vendored one. The vendored copy is what upstream published, so a
// refresh diffs against their change and not against ours.
func (g Generator) Generate(dir, name string, c Client, refresh bool) error {
	if c.Out == "" {
		return fmt.Errorf("%s: out is required", name)
	}

	raw, source, err := obtainSpec(dir, c, refresh)
	if err != nil {
		return err
	}
	g.logf("%s: spec from %s\n", name, source)

	// Convert BEFORE patching. A Swagger 2.0 document keeps parameters
	// and definitions at the root, so a patch written against
	// /components/... cannot resolve - and the conversion would
	// relocate anything a patch had already edited. Converting first
	// means every patch is written against OpenAPI 3 whatever upstream
	// publishes.
	raw, converted, err := toOpenAPI3(raw, dir, name)
	if err != nil {
		return err
	}
	if converted {
		g.logf("  converted Swagger 2.0 -> OpenAPI 3.0\n")
	}

	doc, err := apispec.Decode(raw)
	if err != nil {
		return err
	}

	ops, err := loadPatches(dir, c.Patches)
	if err != nil {
		return err
	}
	if len(ops) > 0 {
		applied, err := apispec.Apply(doc, ops)
		if err != nil {
			return err
		}
		for _, note := range applied {
			g.logf("  patch: %s\n", note)
		}
	}

	patched, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("re-encoding patched spec: %w", err)
	}

	// A real file on disk, because both generators take a path.
	tmp, err := os.CreateTemp("", "oapi-clientgen-"+name+"-*.json")
	if err != nil {
		return fmt.Errorf("staging the patched spec: %w", err)
	}
	// Close as well as Remove: Remove unlinks the path and leaves the
	// descriptor open. Nothing useful to do if either fails.
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	if _, err := tmp.Write(patched); err != nil {
		return fmt.Errorf("staging the patched spec: %w", err)
	}
	// Checked, unlike the deferred Close: a write can succeed and
	// still fail to flush, handing the generator a truncated spec.
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing patched spec: %w", err)
	}

	// Client-Version reports the API version this client was generated
	// against. A spec without one says "unknown" rather than claiming
	// a version nobody published.
	apiVersion := "unknown"
	if info, ok := doc["info"].(map[string]any); ok {
		if v, ok := info["version"].(string); ok && v != "" {
			apiVersion = v
		}
	}

	langs := c.Languages
	if len(langs) == 0 {
		langs = []string{"go"}
	}
	for _, lang := range langs {
		switch lang {
		case "go":
			if err := g.generateGoClient(dir, name, apiVersion, c, tmp.Name()); err != nil {
				return err
			}
		case "ts":
			if err := g.generateTSClient(dir, name, apiVersion, c, tmp.Name()); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown language %q; known: go, ts", lang)
		}
	}
	return nil
}

// obtainSpec returns the spec bytes and a human description of where
// they came from.
//
// A remote spec is vendored on first use: a build that fetches from a
// third party fails when they are down, and gives no diff when they
// change. Vendoring turns an upstream change into a reviewable commit.
func obtainSpec(dir string, c Client, refresh bool) ([]byte, string, error) {
	if c.Spec == "" {
		return nil, "", fmt.Errorf("spec is required")
	}

	remote := strings.HasPrefix(c.Spec, "http://") || strings.HasPrefix(c.Spec, "https://")
	if !remote {
		path := filepath.Join(dir, c.Spec)
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, "", err
		}
		return b, c.Spec, nil
	}

	if c.Vendor == "" {
		return nil, "", fmt.Errorf("a remote spec needs `vendor:` - where to store the fetched copy, so a build does not depend on %s being reachable", c.Spec)
	}
	vendored := filepath.Join(dir, c.Vendor)

	if !refresh {
		b, err := os.ReadFile(vendored)
		switch {
		case err == nil:
			return b, c.Vendor + " (vendored; --refresh to re-fetch)", nil
		case !errors.Is(err, fs.ErrNotExist):
			// Only "not vendored yet" may fall through to a fetch. A
			// permission error or an unreadable file would otherwise
			// re-download and OVERWRITE the vendored copy, which is the
			// opposite of what vendoring is for.
			return nil, "", fmt.Errorf("reading vendored spec %s: %w", c.Vendor, err)
		}
	}

	b, err := fetchSpec(c.Spec)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(vendored), 0o755); err != nil {
		return nil, "", fmt.Errorf("vendoring %s: %w", c.Vendor, err)
	}
	if err := os.WriteFile(vendored, b, 0o644); err != nil {
		return nil, "", fmt.Errorf("vendoring %s: %w", c.Vendor, err)
	}
	return b, c.Spec + " (fetched, vendored to " + c.Vendor + ")", nil
}

func fetchSpec(url string) ([]byte, error) {
	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	return b, nil
}

func loadPatches(dir string, paths []string) ([]apispec.Op, error) {
	var all []apispec.Op
	for _, p := range paths {
		full := filepath.Join(dir, p)
		b, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("reading patch file: %w", err)
		}
		var ops []apispec.Op
		if err := yaml.Unmarshal(b, &ops); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", p, err)
		}
		all = append(all, ops...)
	}
	return all, nil
}

// generateGoClient runs ogen against the patched spec. Its settings
// go in ogen.yml beside the output rather than in flags, so they are
// visible and editable in the repo.
func (g Generator) generateGoClient(dir, name, apiVersion string, c Client, specPath string) error {
	out := filepath.Join(dir, c.Out)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", c.Out, err)
	}

	pkg := c.Package
	if pkg == "" {
		pkg = PackageFromDir(c.Out)
	}

	cfgPath := filepath.Join(out, "ogen.yml")
	// errors.Is, not os.IsNotExist: the latter answers "not found" to
	// a permission error too, and would overwrite a config it could
	// not read.
	switch _, err := os.Stat(cfgPath); {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.WriteFile(cfgPath, []byte(thirdPartyOgenConfig()), 0o644); err != nil {
			return err
		}
	case err != nil:
		return fmt.Errorf("reading %s: %w", cfgPath, err)
	}

	// --clean so a removed endpoint's file goes away. Without it a
	// deleted operation leaves generated code behind that still
	// compiles, and the client keeps offering a call the API dropped.
	argv := []string{
		"go", "run", codegen.OgenCommand(),
		"--config", cfgPath,
		"--target", out,
		"--clean",
		"--package", pkg,
		specPath,
	}
	if err := runIn("generating the Go client for "+name, dir, argv...); err != nil {
		return err
	}
	// ogen's NewClient takes a SecuritySource before its options when
	// the spec declares securitySchemes, so our constructor has to
	// know which signature it is wrapping.
	hasSecurity, err := specHasSecurity(specPath)
	if err != nil {
		return err
	}
	if err := g.syncSupportFiles(out, apiclient.Go, apiclient.Params{
		Name: name, Version: apiVersion, Package: pkg,
		HasSecurity: hasSecurity,
	}); err != nil {
		return err
	}
	g.logf("  go: %s\n", c.Out)
	return nil
}

// generateTSClient writes a named-operation client.
//
// The config sits next to the output because hey-api clears its
// output directory on every run.
func (g Generator) generateTSClient(dir, name, apiVersion string, c Client, specPath string) error {
	out := filepath.Join(dir, c.Out)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", c.Out, err)
	}

	cfgPath, cfg, err := codegen.HeyAPIConfigNestedIn(out, specPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		return err
	}
	defer os.Remove(cfgPath)

	if err := runIn("generating the TypeScript client for "+name, out, codegen.HeyAPICommand(cfgPath)...); err != nil {
		return err
	}
	// Mark the generator's output before the hand-written half goes
	// in beside it: resilience.ts and index.ts are ours to lint.
	if err := apiclient.MarkGenerated(filepath.Join(out, "generated")); err != nil {
		return err
	}
	if err := g.syncSupportFiles(out, apiclient.TypeScript, apiclient.Params{
		Name: name, Version: apiVersion,
		// Published is left false: this client lives in the repo that
		// generated it, so a consumer imports the directory rather
		// than a package name. See apiclient.Params for what else
		// that decides.
		ImportPath: filepath.ToSlash(c.Out),
	}); err != nil {
		return err
	}
	g.logf("  ts: %s\n", c.Out)
	return nil
}

// thirdPartyOgenConfig is the starting ogen.yml for a spec somebody
// else owns.
//
// It differs from the one a scaffolded service gets in one way that
// matters: ignore_not_implemented. A spec you own is fixed when ogen
// cannot express it; a spec you consume is not yours to fix, and
// refusing to generate 150 working methods because 2 use a construct
// ogen lacks leaves you hand-writing all 152.
//
// Written once, on first generation, and the author's to edit after
// that - which is why it is a starting point rather than a file
// regenerated every run.
func thirdPartyOgenConfig() string {
	return `# Generator settings for a spec this repo does not own.
#
# otel is off for the same reason as in a scaffolded service: it costs a
# consumer 13 modules and ~1.5MB and produces spans that go nowhere
# without a TracerProvider.
generator:
  features:
    disable:
      - 'ogen/otel'
  # A third party's spec will contain constructs ogen cannot express,
  # and they are not yours to correct upstream. Skipping those
  # operations generates the rest; without this one unsupported corner
  # of a large API means no client at all.
  #
  # Each entry is a deliberate acceptance that those operations are
  # missing from the generated client. Prefer naming them over "all",
  # so a new unsupported construct is a visible failure rather than a
  # method that silently stops existing.
  #
  # Check that the result COMPILES after adding one. Skipping is not
  # always clean: some features emit a package that does not build
  # rather than one missing those operations - "type-based
  # discrimination with same jxType" can generate a type switch with a
  # duplicate case. When that happens the spec cannot be generated
  # from, and the honest answer is to say so rather than ship a
  # package that does not build.
  ignore_not_implemented:
    - "complex form schema"
`
}

// toOpenAPI3 converts a Swagger 2.0 document, returning the bytes to
// generate from and whether a conversion happened.
//
// ogen refuses a 2.0 document outright ("unsupported version: 2.0").
// Rejecting it here and telling the author to convert by hand would
// make every regeneration a two-step ritual against a file they do not
// own, so the conversion is part of generating.
func toOpenAPI3(raw []byte, dir, name string) ([]byte, bool, error) {
	var probe struct {
		Swagger string `yaml:"swagger" json:"swagger"`
	}
	// A 2.0 document is identified by a top-level `swagger` key; 3.x
	// uses `openapi`. Both decode as YAML, which is a superset of JSON.
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return nil, false, fmt.Errorf("reading spec version: %w", err)
	}
	if !strings.HasPrefix(probe.Swagger, "2.") {
		return raw, false, nil
	}

	in, err := os.CreateTemp("", "oapi-clientgen-"+name+"-v2-*.json")
	if err != nil {
		return nil, false, err
	}
	defer func() {
		_ = in.Close()
		_ = os.Remove(in.Name())
	}()
	if _, err := in.Write(raw); err != nil {
		return nil, false, err
	}
	if err := in.Close(); err != nil {
		return nil, false, fmt.Errorf("writing spec for conversion: %w", err)
	}

	out := in.Name() + ".v3.json"
	defer os.Remove(out)
	// dir, like every other exec here: npx resolves its cache and any
	// .npmrc relative to its working directory, so running in the
	// caller's cwd would behave differently depending on where in the
	// repo the command was invoked.
	if err := runIn("converting "+name+" from Swagger 2.0 to OpenAPI 3", dir, codegen.Swagger2OpenAPICommand(in.Name(), out)...); err != nil {
		return nil, false, err
	}
	b, err := os.ReadFile(out)
	if err != nil {
		return nil, false, fmt.Errorf("reading converted spec: %w", err)
	}
	return b, true, nil
}

// syncSupportFiles makes out match the client's hand-written half -
// the retries, circuit breaker, Retry-After handling and timeout -
// beside the code the generator produced.
//
// Without it `client generate` emitted typed operations and nothing
// else, so a client for a rate-limited API retried nothing and read
// no Retry-After. That half is most of the reason to generate a client
// with this tool rather than running ogen or hey-api directly.
//
// Sync rather than write: it also removes the files this tool used to
// write and no longer does. A caller must not be able to do one
// without the other, or a dropped file lives on in the output
// directory with nobody left to update it.
func (g Generator) syncSupportFiles(out string, set func(apiclient.Params) ([]apiclient.File, error), p apiclient.Params) error {
	files, err := set(p)
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(out, f.Path), []byte(f.Body), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", f.Path, err)
		}
	}
	removed, err := apiclient.RemoveRetired(out)
	if err != nil {
		return err
	}
	// Say so. Everything else this command does prints, and a
	// deletion in someone else's repo is the one that most needs to -
	// it is what tells a consumer who had edited the file to reach
	// for git.
	for _, name := range removed {
		g.logf("  removed %s (no longer generated)\n", filepath.Join(out, name))
	}
	return nil
}

// specHasSecurity reports whether the document declares any
// securityScheme. ogen keys its constructor signature off that, and
// reading the spec is cheaper than parsing back the code it wrote.
func specHasSecurity(specPath string) (bool, error) {
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return false, err
	}
	var doc struct {
		Components struct {
			SecuritySchemes map[string]json.RawMessage `json:"securitySchemes" yaml:"securitySchemes"`
		} `json:"components" yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return false, fmt.Errorf("reading securitySchemes from %s: %w", specPath, err)
	}
	return len(doc.Components.SecuritySchemes) > 0, nil
}
