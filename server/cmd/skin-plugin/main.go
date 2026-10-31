// Command skin-plugin is the offline authoring tool for signed SkinNexus
// plugin bundles and the market registry. It is intentionally not shipped in
// the server image; the signing key never leaves the operator workstation.
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stdout)
		return 2
	}
	switch args[0] {
	case "keygen":
		return runKeygen(args[1:], stdout, stderr)
	case "pack":
		return runPack(args[1:], stdout, stderr)
	case "pack-theme":
		return runPackTheme(args[1:], stdout, stderr)
	case "lint":
		return runLint(args[1:], stdout, stderr)
	case "verify":
		return runVerify(args[1:], stdout, stderr)
	case "registry":
		return runRegistry(args[1:], stdout, stderr)
	case "verify-registry":
		return runVerifyRegistry(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `skin-plugin — signed SkinNexus plugin tooling

  keygen          --id ID --out KEY.pem
  lint            --dir PLUGIN_DIR [--registry FILE] [--host-version V] [--json]
  pack            --dir PLUGIN_DIR --out FILE.zip --key KEY.pem --key-id ID [--meta FILE] [--force] [--json]
  pack-theme      --dir THEME_DIR --out FILE.zip --key KEY.pem --key-id ID [--force] [--json]
  verify          --bundle FILE.zip [--keys KEYS|SKIN_PLUGIN_TRUST_KEYS] [--host-version V] [--json]
  registry        --dist DIR --url-base URL --key KEY.pem --key-id ID [--kind plugin|theme] [--expires-days 90] [--out FILE] [--json]
  verify-registry --registry FILE [--keys KEYS|SKIN_PLUGIN_TRUST_KEYS] [--now RFC3339] [--json]
`)
}
