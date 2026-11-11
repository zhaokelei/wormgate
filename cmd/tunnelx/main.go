// Command tunnelx is the deprecated single-binary compatibility shim.
//
// New deployments should use fps (server) and fpc (client). This shim is kept
// so older scripts keep working, but the release build no longer produces it.
package main

import (
	"os"

	"github.com/wormgate/wormgate/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
