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

	"github.com/spf13/cobra"
)

// Version is set at build time via ldflags; "dev" refuses to self-update.
var Version = "dev"

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "oapiclient-gen",
		Short:        "oapiclient-gen",
		SilenceUsage: true,
	}
	root.AddCommand(updateCmd(), versionCmd())

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
			fmt.Println(Version)
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
