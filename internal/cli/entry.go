package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/wormgate/wormgate/internal/config"
	"github.com/wormgate/wormgate/internal/version"
)

// maybeOpenConsole opens the matching console for desktop launches. It is a
// no-op for terminals and service managers unless a launcher explicitly sets
// WORMGATE_OPEN_BROWSER (the .bat launchers do).
func maybeOpenConsole(url string) {
	if !DoubleClickLaunch() && os.Getenv("WORMGATE_OPEN_BROWSER") == "" {
		return
	}
	go func() {
		time.Sleep(1200 * time.Millisecond)
		if err := OpenBrowser(url); err != nil {
			fmt.Fprintf(os.Stderr, "could not open browser: %v\n", err)
		}
	}()
}

// loopbackAddr turns a wildcard listen address into a browsable loopback URL.
func loopbackAddr(addr string) string {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if h == "" || h == "0.0.0.0" || h == "::" {
		h = "127.0.0.1"
	}
	return net.JoinHostPort(h, p)
}

// RunServerMain is the fps entry point. No subcommand and no
// configuration file are required: `fps` alone starts a working server.
func RunServerMain(args []string) int { return runRoleMain(args, "server") }

// RunClientMain is the fpc entry point. `fpc --server ... --token ...
// --tunnel ...` is enough to publish a service; a config file is optional.
func RunClientMain(args []string) int { return runRoleMain(args, "client") }

func runRoleMain(args []string, role string) int {
	bin := cmdName()

	// Help works in any position and does not require a started config manager.
	for _, a := range args {
		if a == "help" || a == "-h" || a == "--help" {
			roleUsage(bin, role)
			return 0
		}
	}

	// The tool subcommands are shared by both binaries. Everything else is
	// treated as startup flags for this binary's role.
	if len(args) > 0 {
		switch args[0] {
		case "version":
			printVersion(bin, role)
			return 0
		case "config":
			return runConfig(baseOptions(args), args[1:])
		case "init":
			return runInit(args[1:])
		case "password-hash":
			return runPasswordHash(args[1:])
		case "server":
			// Tolerate `fps server ...` from older scripts.
			if role == "server" {
				args = args[1:]
			}
		case "client":
			// Tolerate `fpc client ...` from older scripts.
			if role == "client" {
				args = args[1:]
			}
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opts := baseOptions(args)
	if role == "server" {
		return runServer(ctx, opts, args)
	}
	return runClient(ctx, opts, args)
}

// baseOptions extracts the bootstrap flags that must be known before config
// loading. Overrides (log level, role-specific flags) are attached later by
// runServer / runClient so they always win over environment and YAML.
func baseOptions(args []string) config.Options {
	early := scanEarly(args)
	return config.Options{
		ConfigPath: early.configPath,
		Profile:    early.profile,
		NoGlobal:   early.noGlobal,
	}
}

func printVersion(bin, role string) {
	fmt.Printf("%s %s (%s role, protocol %s, %s, %s/%s)\n",
		bin, version.Version, role, version.Protocol,
		runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func roleUsage(bin, role string) {
	tpl := serverUsageText
	if role == "client" {
		tpl = clientUsageText
	}
	fmt.Fprint(os.Stderr, strings.NewReplacer("$BIN", bin, "$VER", version.Version).Replace(tpl))
}

const serverUsageText = `$BIN $VER - Wormgate server (control plane + public ingress)

Usage:
  $BIN [flags]                        start the server - no config file needed
  $BIN config show|validate [flags]   inspect or validate configuration
  $BIN init [flags]                   generate a configuration file
  $BIN password-hash <pw>             generate an argon2id password hash
  $BIN version                        print version and build platform

Quick start (copy-paste friendly):
  $BIN
      prints the one-time admin password, a demo token, and a ready-to-run
      fpc command. Nothing to edit before the first connection.

Common flags:
  --config <file>      project config (default: ./wormgate.yaml if present)
  --profile <name>     apply profiles.<name> overlay
  --no-global          ignore the per-user global config
  --log-level <lvl>    debug|info|warn|error
  --log-format <fmt>   console|json

Server flags:
  --control-addr <host:port>      control + data listener (TLS 1.3)
  --http-addr <host:port>         public HTTP ingress
  --http-domain <domain>          base domain for subdomain routing
  --http-public-port <port>       port advertised in public URLs
  --tcp-bind <host>               public TCP bind host
  --tcp-port-range <a-b>          automatic TCP port range
  --webui-addr <host:port>        admin console (0.0.0.0:7100)
  --metrics-addr <host:port>      Prometheus endpoint (0.0.0.0:9090)
  --data-dir <dir>                persisted state
  --tls-cert / --tls-key <file>   production certificate
  --tls-client-ca <file>          require client certificates (mTLS)
  --token <t1,t2>                 extra static client tokens
  --admin-password <pw>           bootstrap admin password (development)

Environment: every field accepts WORMGATE_<PATH>, e.g.
  WORMGATE_SERVER_HTTP_DOMAIN=example.com
The two consoles are at the webui address; the admin password is only
printed on the first start of a new data directory.
`

const clientUsageText = `$BIN $VER - Wormgate client (publish local services)

Usage:
  $BIN [flags]                        connect - no config file needed
  $BIN config show|validate [flags]   inspect or validate configuration
  $BIN init [flags]                   generate a configuration file
  $BIN password-hash <pw>             generate an argon2id password hash
  $BIN version                        print version and build platform

Quick start (one line, no file to edit):
  $BIN --server example.com:7000 --token <token> \
      --tunnel web=http://127.0.0.1:8080 --insecure
  The local console at http://127.0.0.1:7200/ can then configure everything
  else (server, TLS, tunnels, quotas) from forms instead of editing YAML.

Common flags:
  --config <file>      project config (default: ./wormgate.yaml if present)
  --profile <name>     apply profiles.<name> overlay
  --no-global          ignore the per-user global config
  --log-level <lvl>    debug|info|warn|error
  --log-format <fmt>   console|json

Client flags:
  --server <host:port>       server control address (no scheme, TLS is always on)
  --token <token>            authentication token
  --name <name>              client name shown in the admin console
  --tunnel <spec>            repeatable: name=type://local[@subdomain|@port]
                             type is http or tcp
  --insecure                 skip TLS verification (development only)
  --ca-file <file>           custom CA bundle
  --client-cert/--client-key <file>   mTLS client certificate
  --webui-addr <host:port>   local console (127.0.0.1:7200)
  --webui-token <token>      console access token

Examples:
  $BIN --server 127.0.0.1:7000 --token tx_xxx --insecure \
      --tunnel web=http://127.0.0.1:8080 \
      --tunnel ssh=tcp://127.0.0.1:22@2222

Environment: every field accepts WORMGATE_<PATH>, e.g.
  WORMGATE_CLIENT_TOKEN=tx_xxx
$BIN and the server binary can share ONE configuration file: each side only
reads its own section.
`
