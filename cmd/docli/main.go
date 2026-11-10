// Command docli downloads docs into small indexed files that agents can read
// cheaply. See `docli --help`.
package main

import (
	"os"

	"docli/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
