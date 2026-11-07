// Command sizelint is a size ratchet: it holds the Go files, functions and
// shell scripts of a repo to the limits in its .ratchets.yml, with a baseline
// of known violations that may only shrink. See package
// github.com/tatlacas-com/go-ratchets/internal/sizelint for the rules.
package main

import (
	"os"

	"github.com/tatlacas-com/go-ratchets/internal/sizelint"
)

func main() {
	os.Exit(sizelint.Run(os.Args[1:], os.Stdout, os.Stderr))
}
