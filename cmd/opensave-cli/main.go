// Command opensave-cli is the OpenSave command-line interface.
package main

import (
	"os"

	"github.com/opensave/opensave/internal/cliapp"
)

func main() {
	os.Exit(cliapp.Run(os.Args[1:]))
}
