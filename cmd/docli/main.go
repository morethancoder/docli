// Command docli downloads docs into small indexed files that agents can read
// cheaply. See `docli --help`.
package main

import (
	"os"
	"runtime/debug"

	"github.com/morethancoder/docli/internal/cli"
)

func main() {
	// `go install ...@v1.2.3` records the module version; use it when no
	// version was set at build time.
	if bi, ok := debug.ReadBuildInfo(); ok && cli.Version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		cli.Version = bi.Main.Version
	}
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
