package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ChristopherScot/oapi-clientgen/apiclient"
	"gopkg.in/yaml.v3"
)

// Wrapper writes the resilience layer beside typed operations someone
// else generated: retries, the circuit breaker, Retry-After, the
// timeout, and an iterator for every operation the spec says pages.
//
// Generate runs ogen or hey-api first and then calls this. A service
// that owns its spec already runs ogen itself - pokedex's own
// go:generate does, with its own ogen.yml - so it calls this alone
// rather than letting a second ogen run with different settings
// rewrite its server scaffolding.
//
// out is the directory the typed operations are already in. Nothing
// here runs a generator, writes an ogen.yml or cleans the directory,
// so the files already there are left alone.
func (g Generator) Wrapper(out, specPath, name, pkg string, languages []string) error {
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("reading %s: %w", specPath, err)
	}

	apiVersion := "unknown"
	if info, ok := doc["info"].(map[string]any); ok {
		if v, ok := info["version"].(string); ok && v != "" {
			apiVersion = v
		}
	}

	if len(languages) == 0 {
		languages = []string{"go"}
	}
	for _, lang := range languages {
		switch lang {
		case "go":
			if err := g.goWrapper(out, specPath, name, pkg, apiVersion); err != nil {
				return err
			}
		case "ts":
			if err := g.tsWrapper(out, specPath, name, apiVersion); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown language %q; known: go, ts", lang)
		}
	}
	return nil
}

func (g Generator) goWrapper(out, specPath, name, pkg, apiVersion string) error {
	if pkg == "" {
		pkg = PackageFromDir(out)
	}
	hasSecurity, err := specHasSecurity(specPath)
	if err != nil {
		return err
	}
	paged, err := pagedOperations(specPath)
	if err != nil {
		return err
	}
	if err := g.syncSupportFiles(out, apiclient.Go, apiclient.Params{
		Name: name, Version: apiVersion, Package: pkg,
		HasSecurity: hasSecurity, Paged: paged,
	}); err != nil {
		return err
	}
	g.logf("  go: %s\n", out)
	return nil
}

func (g Generator) tsWrapper(out, specPath, name, apiVersion string) error {
	paged, err := pagedOperations(specPath)
	if err != nil {
		return err
	}
	if err := g.syncSupportFiles(out, apiclient.TypeScript, apiclient.Params{
		Name: name, Version: apiVersion, Paged: paged,
		ImportPath: filepath.ToSlash(out),
	}); err != nil {
		return err
	}
	g.logf("  ts: %s\n", out)
	return nil
}

// WrapperName is what a wrapper calls the API when nothing names it:
// the output directory, which is what a Go package would be called
// anyway.
func WrapperName(out string) string {
	return strings.TrimSuffix(filepath.Base(filepath.Clean(out)), "/")
}
