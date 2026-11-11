// Command fps is the Wormgate server: control plane, public ingress, admin
// console and Prometheus endpoint. It is a standalone binary - the client
// counterpart is fpc.
package main

import (
	"os"

	"github.com/wormgate/wormgate/internal/cli"
)

func main() {
	code := cli.RunServerMain(os.Args[1:])
	cli.HoldConsoleOnExit(code)
	os.Exit(code)
}
