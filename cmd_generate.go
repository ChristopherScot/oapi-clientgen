package main

// The CLI over the generate package. Everything here is flags,
// arguments and output: the pipeline itself lives in generate/, so a
// caller that wants it without a shell can import it.

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ChristopherScot/oapi-gen/generate"
	"github.com/spf13/cobra"
)

func clientListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "list the clients declared in clients.yaml",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			path, m, err := generate.LoadManifest("")
			if err != nil {
				return err
			}
			fmt.Println(path)
			for _, name := range generate.Names(m) {
				c := m.Clients[name]
				fmt.Printf("  %-16s %s -> %s\n", name, c.Spec, c.Out)
			}
			return nil
		},
	}
}

// generate has two children because there are two relationships to a
// spec, and they need different things done.
//
// `generate client` is for a spec this repo does not own: fetch it,
// vendor it so a build does not depend on a third party being up,
// patch what ogen cannot express, generate, wrap.
//
// `generate self` is for the spec a service owns. Nothing to fetch,
// nothing to vendor, nothing to patch - and it needs the server
// scaffolding too, which a consumer of someone else's API never wants.
func clientGenerateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "generate from a spec: one you consume, or one you own",
	}
	cmd.AddCommand(generateClientCmd(), generateSelfCmd())
	return cmd
}

func generateClientCmd() *cobra.Command {
	var (
		manifest  string
		specFlag  string
		outFlag   string
		pkgFlag   string
		langFlag  []string
		patchFlag []string
		refresh   bool
	)
	cmd := &cobra.Command{
		Use:   "client [name...]",
		Short: "generate clients from a spec this repo does not own",
		// The arguments are the manifest's client names, so complete
		// them rather than letting the shell offer filenames.
		ValidArgsFunction: makeCompleter(func() ([]string, error) {
			_, m, err := generate.LoadManifest("")
			if err != nil {
				return nil, err
			}
			return generate.Names(m), nil
		}),
		Long: `Generate typed clients from a third-party OpenAPI spec.

With no arguments, generates every client in clients.yaml. Named
arguments generate only those. With --spec, generates one client
without a manifest, which is the way to try a spec before committing
to it.

A remote spec is vendored on first use and reused after that, so a
build does not depend on a third party being reachable. --refresh
re-fetches it, which is how an upstream change enters the repo: as a
reviewable diff.`,
		Example: `  oapi-gen generate client
  oapi-gen generate client acme --refresh
  oapi-gen generate client --spec ./acme.json --out ./internal/acmeapi`,
		RunE: func(_ *cobra.Command, args []string) error {
			// --spec is the no-manifest path: one client, named by its
			// output directory, nothing committed. It exists so a spec
			// can be tried before anyone decides to keep it.
			if specFlag != "" {
				if len(args) > 0 {
					return fmt.Errorf("--spec generates one client; drop the name argument or use the manifest")
				}
				if outFlag == "" {
					return fmt.Errorf("--out is required with --spec")
				}
				name := pkgFlag
				if name == "" {
					name = generate.PackageFromDir(outFlag)
				}
				return generate.Generate(".", name, generate.Client{
					Spec:      specFlag,
					Patches:   patchFlag,
					Out:       outFlag,
					Languages: langFlag,
					Package:   name,
				}, refresh)
			}

			path, m, err := generate.LoadManifest(manifest)
			if err != nil {
				return err
			}
			dir := filepath.Dir(path)

			names := args
			if len(names) == 0 {
				names = generate.Names(m)
			}
			for _, name := range names {
				c, ok := m.Clients[name]
				if !ok {
					return fmt.Errorf("no client %q in %s; declared: %v", name, path, generate.Names(m))
				}
				if err := generate.Generate(dir, name, c, refresh); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&manifest, "manifest", "", "path to clients.yaml (default: nearest one)")
	cmd.Flags().StringVar(&specFlag, "spec", "", "generate one client from this spec path or URL, without a manifest")
	cmd.Flags().StringVar(&outFlag, "out", "", "output directory, with --spec")
	cmd.Flags().StringVar(&pkgFlag, "package", "", "Go package name, with --spec (default: the output directory's name)")
	cmd.Flags().StringSliceVar(&langFlag, "languages", nil, "languages to emit: go, ts (default: go)")
	cmd.Flags().StringSliceVar(&patchFlag, "patch", nil, "patch files to apply, with --spec")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "re-fetch a remote spec even if it is already vendored")
	return cmd
}

func generateSelfCmd() *cobra.Command {
	var (
		outFlag     string
		tsOutFlag   string
		npmFlag     string
		pkgFlag     string
		nameFlag    string
		supportOnly bool
	)
	cmd := &cobra.Command{
		Use:   "self [spec]",
		Short: "generate the server and client for a spec this service owns",
		Args:  cobra.MaximumNArgs(1),
		Long: `Generate everything a service's own spec implies: the server
scaffolding it implements, the typed client its consumers import, and
the resilience layer beside both.

Takes a path rather than a manifest entry. A spec you own has nothing
to fetch, nothing to vendor against a third party going down, and
nothing to patch - a spec you own is fixed at the source.

Meant to run from the service's own go:generate, so CI's staleness
gate catches a spec change that was not regenerated.

There is no ogen.yml to edit. The settings are the same for every
service built this way, so they are composed here rather than copied
into each one.`,
		Example: `  oapi-gen generate self
  oapi-gen generate self ./openapi.yml --out api`,
		RunE: func(_ *cobra.Command, args []string) error {
			spec := "openapi.yml"
			if len(args) == 1 {
				spec = args[0]
			}
			if tsOutFlag != "" && npmFlag == "" {
				return fmt.Errorf("--npm-package is required with --ts-out: a published client's imports carry it")
			}
			return generate.Generator{Log: os.Stdout}.Self(".", spec, generate.SelfOptions{
				GoOut:       outFlag,
				TSOut:       tsOutFlag,
				NPMPackage:  npmFlag,
				Package:     pkgFlag,
				Name:        nameFlag,
				SupportOnly: supportOnly,
			})
		},
	}
	cmd.Flags().StringVar(&outFlag, "out", "api", "where the generated code goes")
	cmd.Flags().StringVar(&tsOutFlag, "ts-out", "", "where the published TypeScript client goes (omit for none)")
	cmd.Flags().StringVar(&npmFlag, "npm-package", "", "how a consumer imports the TypeScript client, with --ts-out")
	cmd.Flags().StringVar(&pkgFlag, "package", "", "Go package name (default: the output directory's name)")
	cmd.Flags().StringVar(&nameFlag, "name", "", "the API's name for doc comments (default: the spec's info.title)")
	cmd.Flags().BoolVar(&supportOnly, "support-only", false,
		"skip the generators and write only the resilience layer, for a service still running its own ogen")
	return cmd
}
