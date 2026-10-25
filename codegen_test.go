package codegen

import (
	"strings"
	"testing"
)

// npx runs a binary from the nearest package.json, not from the
// directory it was invoked in. Everything a config says about where
// files are has to survive that move, and nothing in the generated
// output shows when it has not: hey-api reports success and writes
// somewhere else.
func TestHeyAPIConfigTakesAbsolutePathsForAThirdPartyClient(t *testing.T) {
	const in = "/repo/app/src/vendor/zoomAPI/specs/zoom-users.json"
	const out = "/repo/app/src/vendor/zoomAPI/users/generated"

	cfg := HeyAPIConfig(in, out)

	if !strings.Contains(cfg, `"`+in+`"`) {
		t.Errorf("spec path is not in the config verbatim:\n%s", cfg)
	}
	if !strings.Contains(cfg, `"`+out+`"`) {
		t.Errorf("output path is not in the config verbatim:\n%s", cfg)
	}
	// The bug this guards: './generated' resolved against npx's own
	// working directory, which for a client nested in an app is the
	// app root - so the client's own directory stayed empty.
	if strings.Contains(cfg, `"./generated"`) && out != "./generated" {
		t.Error("output path is relative; npx will resolve it from the package root")
	}
}

// The command must name its config. Letting hey-api search for one
// finds nothing (the search starts from npx's working directory), and
// it then dies inside its own defaulting rather than saying so.
func TestHeyAPICommandNamesItsConfigFile(t *testing.T) {
	const cfg = "/repo/app/src/vendor/zoomAPI/users/openapi-ts.config.ts"

	argv := HeyAPICommand(cfg)

	var sawFlag bool
	for i, a := range argv {
		if a == "-f" {
			if i+1 >= len(argv) || argv[i+1] != cfg {
				t.Fatalf("-f is not followed by the config path: %v", argv)
			}
			sawFlag = true
		}
	}
	if !sawFlag {
		t.Errorf("no -f: hey-api would search from npx's working directory, not ours\n%v", argv)
	}
}
