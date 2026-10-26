package generate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ManifestName is the file clients are declared in.
const ManifestName = "clients.yaml"

// Manifest is clients.yaml: every third-party API this repo
// generates a client for.
//
// Committed, because a generated client is only reproducible if the spec
// it came from and the patches that made it generatable are both pinned.
// Without the file, regenerating after an upstream change means
// rediscovering by hand which edits the spec needed.
type Manifest struct {
	Clients map[string]Client `yaml:"clients"`
}

type Client struct {
	// Spec is a local path (relative to the manifest) or an https URL.
	Spec string `yaml:"spec"`

	// Vendor is where a fetched spec is written. A remote spec is
	// vendored rather than fetched at generation time so a build does
	// not depend on a third party being up, and so a diff shows when
	// their API changed.
	Vendor string `yaml:"vendor,omitempty"`

	// Patches are files of apispec.Op applied in order before
	// generating.
	Patches []string `yaml:"patches,omitempty"`

	// Out is the directory generated code is written to.
	Out string `yaml:"out"`

	// Languages to emit: go, ts. Defaults to go.
	Languages []string `yaml:"languages,omitempty"`

	// Package is the Go package name for the generated code. Defaults
	// to the client's key in the manifest.
	Package string `yaml:"package,omitempty"`
}

// Names are the clients in m, sorted - `list` prints them and the
// "no such client" error offers them, so the order has to be stable.
func Names(m *Manifest) []string {
	out := make([]string, 0, len(m.Clients))
	for name := range m.Clients {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// PackageFromDir is the Go package name implied by an output directory.
// Hyphens are legal in a directory name and not in a package name.
func PackageFromDir(dir string) string {
	base := filepath.Base(filepath.Clean(dir))
	return strings.ReplaceAll(base, "-", "")
}

// LoadManifest reads the manifest at explicit, or the nearest one at
// or above the working directory, and returns its path with it.
func LoadManifest(explicit string) (string, *Manifest, error) {
	path := explicit
	if path == "" {
		found, start, err := findUpwards(ManifestName)
		if err != nil {
			return "", nil, err
		}
		if found == "" {
			return "", nil, fmt.Errorf("no %s in %s or any parent up to the repository root.\n"+
				"create one, or pass --manifest", ManifestName, start)
		}
		path = found
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	var m Manifest
	// KnownFields, as config.yaml does: a typo'd key would otherwise
	// decode cleanly and do nothing. `languges: [ts]` silently
	// generating Go only is the kind of failure that gets blamed on the
	// generator.
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if err := d.Decode(&m); err != nil && !errors.Is(err, io.EOF) {
		return "", nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(m.Clients) == 0 {
		return "", nil, fmt.Errorf("%s declares no clients", path)
	}
	return path, &m, nil
}
