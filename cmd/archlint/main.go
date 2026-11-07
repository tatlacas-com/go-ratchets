// Command archlint is an import-direction ratchet: it holds the packages of a
// module to the rules in its .ratchets.yml, with a baseline of known
// violations that may only shrink. See package
// github.com/tatlacas-com/go-ratchets/internal/archlint for the rules.
package main

import (
	"os"

	"github.com/tatlacas-com/go-ratchets/internal/archlint"
)

func main() {
	os.Exit(archlint.Run(os.Args[1:], os.Stdout, os.Stderr))
}
