// Command fpc is the Wormgate client: it connects to an fps server, publishes
// local services and hosts the local configuration console. It is a
// standalone binary - the server counterpart is fps.
package main

import (
	"os"

	"github.com/wormgate/wormgate/internal/cli"
)

func main() {
	code := cli.RunClientMain(os.Args[1:])
	cli.HoldConsoleOnExit(code)
	os.Exit(code)
}
