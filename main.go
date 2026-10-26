// oapiclient-gen
package main

// THIS WHOLE REPO IS YOURS. Nothing rewrites these files after `init`.
// Two couplings survive that:
//
//   - VERSION drives releases: CI publishes only when its first line
//     changes on main, tagging what it says.
//   - update.go downloads `oapiclient-gen_<os>_<arch>.tar.gz` from the
//     latest GitHub release; CI builds assets with exactly that name.
//     Rename either side and self-update fails.

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// Version is set at build time via ldflags by the release workflow.
//
// `go install` passes no ldflags, so it stays empty there - but the
// module version it was installed at is embedded in the binary, and
// version() reads that instead. Without the fallback a go-installed
// binary reports "dev" and refuses to self-update.
var Version string

// version is what this binary should call itself: the ldflags value,
// else the module version it was built from, else "dev".
func version() string {
	var module string
	if bi, ok := debug.ReadBuildInfo(); ok {
		module = bi.Main.Version
	}
	return pickVersion(Version, module)
}

// pickVersion is version()'s choice, separated from the build-info
// lookup so it can be tested - a test binary has no module version of
// its own to stand in for one.
//
// Only a released version counts. A build from a working tree reports
// "(devel)" or a pseudo-version derived from the commit it was built
// at; neither names a release that exists, and updating from one would
// overwrite someone's own build with a release they did not ask for.
// semver.IsValid rejects "(devel)" and "" along with anything else
// that is not a version.
func pickVersion(ldflags, modver string) string {
	switch {
	case ldflags != "":
		return ldflags
	case !semver.IsValid(modver) || module.IsPseudoVersion(modver):
		return "dev"
	default:
		return modver
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "oapiclient-gen",
		Short: "generate typed clients from an OpenAPI spec",
		Long: `Generate typed Go and TypeScript clients from an OpenAPI spec,
with a transport that already does timeouts, retries, Retry-After and
circuit breaking.

Declare clients in clients.yaml and run with no arguments, or point
--spec at one spec to try it without committing to anything.`,
		SilenceUsage: true,
	}
	root.AddCommand(clientGenerateCmd(), clientListCmd(), updateCmd(), versionCmd())

	// Cobra builds `completion` during Execute, so force it to exist now
	// and hang `install` off it.
	root.InitDefaultCompletionCmd()
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			c.AddCommand(completionInstallCmd("oapiclient-gen"))
		}
	}
	return root
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print the version",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println(version())
		},
	}
}

func main() {
	if err := rootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// makeCompleter returns ShellCompDirectiveError rather than nothing so the
// shell does not fall back to filenames, which would look like a bug.
func makeCompleter(list func() ([]string, error)) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names, err := list()
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}
