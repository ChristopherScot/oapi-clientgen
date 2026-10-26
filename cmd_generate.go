package main

// The CLI over the generate package. Everything here is flags,
// arguments and output: the pipeline itself lives in generate/, so a
// caller that wants it without a shell can import it.

import (
	"fmt"
	"path/filepath"

	"github.com/ChristopherScot/oapi-clientgen/generate"
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

func clientGenerateCmd() *cobra.Command {
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
		Use:   "generate [name...]",
		Short: "generate clients from a third-party OpenAPI spec",
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
		Example: `  oapi-clientgen generate
  oapi-clientgen generate acme --refresh
  oapi-clientgen generate --spec ./acme.json --out ./internal/acmeapi`,
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
