package generate

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runIn runs argv in dir, turning a non-zero exit into an error that
// carries what the tool actually said.
//
// Generators fail by writing to stderr and exiting 1, so without the
// captured output the error is "exit status 1" and the reason - a
// malformed spec, a missing config - is thrown away.
func runIn(what, dir string, argv ...string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s: %w\n%s", what, err, msg)
		}
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// findUpwards looks for name in the working directory and each parent,
// stopping at a repository boundary.
//
// The manifest sits at the root of the repo that owns the clients, and
// this is run from wherever someone happens to be standing. Going past
// a .git would pick up an unrelated file from a parent checkout.
func findUpwards(name string) (found, start string, err error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("working directory: %w", err)
	}
	start = dir

	for {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, start, nil
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", start, nil
}
