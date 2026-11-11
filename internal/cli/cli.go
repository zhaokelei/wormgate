// Package cli holds the command-line surface shared by the fps (server),
// fpc (client) and legacy tunnelx entry points. Keeping the flag definitions,
// config bootstrap and helpers here lets every binary behave identically while
// still being a standalone executable.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/wormgate/wormgate/internal/auth"
	"github.com/wormgate/wormgate/internal/client"
	"github.com/wormgate/wormgate/internal/config"
	"github.com/wormgate/wormgate/internal/logx"
	"github.com/wormgate/wormgate/internal/server"
	"github.com/wormgate/wormgate/internal/version"
)

// Run is the legacy single-binary dispatcher used by the cmd/tunnelx shim.
// New deployments use RunServerMain (fps) or RunClientMain (fpc) instead.
func Run(args []string) int {
	if len(args) == 0 {
		usage()
		return 2
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage()
		return 0
	}
	cmd, rest := splitCommand(args)
	if cmd == "" {
		usage()
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	early := scanEarly(args)
	opts := config.Options{
		ConfigPath: early.configPath,
		Profile:    early.profile,
		NoGlobal:   early.noGlobal,
	}
	switch cmd {
	case "server":
		return runServer(ctx, opts, rest)
	case "client":
		return runClient(ctx, opts, rest)
	case "init":
		return runInit(rest)
	case "config":
		return runConfig(opts, rest)
	case "password-hash":
		return runPasswordHash(rest)
	case "version":
		fmt.Printf("wormgate %s (protocol %s, %s, %s/%s)\n",
			version.Version, version.Protocol, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown command %q\n\n", cmdName(), cmd)
		usage()
		return 2
	}
}

type earlyOpts struct {
	configPath string
	profile    string
	noGlobal   bool
}

// scanEarly extracts the bootstrap flags needed before the configuration can
// be loaded. Environment variables act as the next layer; CLI wins.
func scanEarly(args []string) earlyOpts {
	o := earlyOpts{
		configPath: strings.TrimSpace(os.Getenv("WORMGATE_CONFIG")),
		profile:    strings.TrimSpace(os.Getenv("WORMGATE_PROFILE")),
	}
	// Compatibility with installations predating the rename: the old variable
	// names still work when the new ones are unset.
	if o.configPath == "" {
		o.configPath = strings.TrimSpace(os.Getenv("TUNNELX_CONFIG"))
	}
	if o.profile == "" {
		o.profile = strings.TrimSpace(os.Getenv("TUNNELX_PROFILE"))
	}
	v := strings.TrimSpace(os.Getenv("WORMGATE_NO_GLOBAL"))
	if v == "" {
		v = strings.TrimSpace(os.Getenv("TUNNELX_NO_GLOBAL"))
	}
	if v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			o.noGlobal = parsed
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config" || a == "-config":
			if i+1 < len(args) {
				o.configPath = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--config="):
			o.configPath = strings.TrimPrefix(a, "--config=")
		case strings.HasPrefix(a, "-config="):
			o.configPath = strings.TrimPrefix(a, "-config=")
		case a == "--profile" || a == "-profile":
			if i+1 < len(args) {
				o.profile = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--profile="):
			o.profile = strings.TrimPrefix(a, "--profile=")
		case strings.HasPrefix(a, "-profile="):
			o.profile = strings.TrimPrefix(a, "-profile=")
		case a == "--no-global" || a == "-no-global":
			o.noGlobal = true
		}
	}
	return o
}

// splitCommand finds the first non-flag token while skipping values of
// bootstrap flags, allowing both `wormgate server --config x` and
// `tunnelx --config x server`.
func splitCommand(args []string) (string, []string) {
	known := map[string]bool{
		"server": true, "client": true, "init": true, "config": true,
		"password-hash": true, "version": true, "help": true,
	}
	for i, a := range args {
		if !known[a] {
			continue
		}
		rest := make([]string, 0, len(args)-1)
		rest = append(rest, args[:i]...)
		rest = append(rest, args[i+1:]...)
		return a, rest
	}
	return "", nil
}

type commonFlags struct {
	logLevel   string
	logFormat  string
	configPath string
	profile    string
	noGlobal   bool
}

func newCommonFlagSet(name string) (*flag.FlagSet, *commonFlags) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	v := &commonFlags{}
	fs.StringVar(&v.logLevel, "log-level", "", "debug|info|warn|error (overrides config)")
	fs.StringVar(&v.logFormat, "log-format", "", "console|json (overrides config)")
	fs.StringVar(&v.configPath, "config", "", "project configuration file")
	fs.StringVar(&v.profile, "profile", "", "configuration profile (dev/prod/...)")
	fs.BoolVar(&v.noGlobal, "no-global", false, "ignore the per-user global config")
	return fs, v
}

func setFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

func commonOverrides(set map[string]bool, v *commonFlags) func(*config.Config) {
	return func(c *config.Config) {
		if set["log-level"] {
			c.Log.Level = v.logLevel
		}
		if set["log-format"] {
			c.Log.Format = v.logFormat
		}
	}
}

type serverFlagValues struct {
	controlAddr   string
	httpAddr      string
	httpDomain    string
	publicPort    int
	tcpBind       string
	tcpRange      string
	webuiAddr     string
	metricsAddr   string
	dataDir       string
	certFile      string
	keyFile       string
	clientCA      string
	token         string
	adminPassword string
}

func addServerFlags(fs *flag.FlagSet) *serverFlagValues {
	v := &serverFlagValues{}
	fs.StringVar(&v.controlAddr, "control-addr", "", "control-plane listen address (host:port)")
	fs.StringVar(&v.httpAddr, "http-addr", "", "public HTTP listen address")
	fs.StringVar(&v.httpDomain, "http-domain", "", "base domain for HTTP tunnels")
	fs.IntVar(&v.publicPort, "http-public-port", 0, "port advertised in public URLs (0 = derive)")
	fs.StringVar(&v.tcpBind, "tcp-bind", "", "public TCP bind host")
	fs.StringVar(&v.tcpRange, "tcp-port-range", "", "automatic TCP port range, e.g. 20000-30000")
	fs.StringVar(&v.webuiAddr, "webui-addr", "", "admin console listen address")
	fs.StringVar(&v.metricsAddr, "metrics-addr", "", "Prometheus listen address")
	fs.StringVar(&v.dataDir, "data-dir", "", "data directory")
	fs.StringVar(&v.certFile, "tls-cert", "", "server certificate PEM")
	fs.StringVar(&v.keyFile, "tls-key", "", "server private key PEM")
	fs.StringVar(&v.clientCA, "tls-client-ca", "", "client CA for mutual TLS")
	fs.StringVar(&v.token, "token", "", "additional static client token(s), comma separated")
	fs.StringVar(&v.adminPassword, "admin-password", "", "bootstrap admin password (development)")
	return v
}

func serverOverrides(set map[string]bool, v *serverFlagValues) func(*config.Config) {
	return func(c *config.Config) {
		if set["control-addr"] {
			c.Server.ControlAddr = v.controlAddr
		}
		if set["http-addr"] {
			c.Server.HTTP.Addr = v.httpAddr
		}
		if set["http-domain"] {
			c.Server.HTTP.Domain = v.httpDomain
		}
		if set["http-public-port"] {
			c.Server.HTTP.PublicPort = v.publicPort
		}
		if set["tcp-bind"] {
			c.Server.TCP.BindAddr = v.tcpBind
		}
		if set["tcp-port-range"] {
			c.Server.TCP.PortRange = v.tcpRange
		}
		if set["webui-addr"] {
			c.Server.WebUI.Addr = v.webuiAddr
		}
		if set["metrics-addr"] {
			c.Server.Metrics.Addr = v.metricsAddr
		}
		if set["data-dir"] {
			c.Server.DataDir = v.dataDir
		}
		if set["tls-cert"] {
			c.Server.TLS.CertFile = v.certFile
		}
		if set["tls-key"] {
			c.Server.TLS.KeyFile = v.keyFile
		}
		if set["tls-client-ca"] {
			c.Server.TLS.ClientCAFile = v.clientCA
		}
		if set["admin-password"] {
			c.Server.WebUI.AdminPassword = v.adminPassword
		}
		if set["token"] {
			for _, token := range strings.Split(v.token, ",") {
				if token = strings.TrimSpace(token); token != "" {
					c.Server.Auth.Tokens = append(c.Server.Auth.Tokens, token)
				}
			}
		}
	}
}

type clientFlagValues struct {
	server       string
	token        string
	name         string
	tunnels      tunnelFlags
	insecure     bool
	caFile       string
	certFile     string
	keyFile      string
	webuiAddr    string
	webuiToken   string
	streamSecret string
}

type tunnelFlags struct {
	items []config.TunnelConfig
}

func (t *tunnelFlags) String() string { return "" }

func (t *tunnelFlags) Set(raw string) error {
	tc, err := parseTunnelFlag(raw)
	if err != nil {
		return err
	}
	t.items = append(t.items, tc)
	return nil
}

func parseTunnelFlag(raw string) (config.TunnelConfig, error) {
	name, rest, ok := strings.Cut(raw, "=")
	if !ok || strings.TrimSpace(name) == "" {
		return config.TunnelConfig{}, fmt.Errorf("expected name=type://local, e.g. web=http://127.0.0.1:8080")
	}
	name = strings.TrimSpace(name)
	// A trailing "#enc" opts this one tunnel into stream encryption. It is
	// stripped before the address is parsed so the URL fragment rules never
	// come into play.
	encrypted := false
	if hash := strings.LastIndex(rest, "#"); hash >= 0 {
		switch strings.ToLower(strings.TrimSpace(rest[hash+1:])) {
		case "enc", "encrypt":
			encrypted = true
		case "":
		default:
			return config.TunnelConfig{}, fmt.Errorf("unknown tunnel option %q (supported: #enc)", rest[hash+1:])
		}
		rest = rest[:hash]
	}
	sub := ""
	if at := strings.LastIndex(rest, "@"); at >= 0 {
		sub = strings.TrimSpace(rest[at+1:])
		rest = rest[:at]
	}
	kind := ""
	local := ""
	if strings.Contains(rest, "://") {
		u, err := url.Parse(rest)
		if err != nil || u.Host == "" {
			return config.TunnelConfig{}, fmt.Errorf("invalid tunnel address %q", rest)
		}
		if u.Path != "" && u.Path != "/" {
			return config.TunnelConfig{}, fmt.Errorf("tunnel local address must not contain a path")
		}
		kind = u.Scheme
		local = u.Host
	} else if idx := strings.Index(rest, ":"); idx > 0 {
		maybeKind := strings.ToLower(rest[:idx])
		if maybeKind == "http" || maybeKind == "tcp" {
			kind = maybeKind
			local = rest[idx+1:]
		} else {
			kind = "http"
			local = rest
		}
	} else {
		return config.TunnelConfig{}, fmt.Errorf("tunnel local address must be host:port")
	}
	if kind != "http" && kind != "tcp" {
		return config.TunnelConfig{}, fmt.Errorf("tunnel type must be http or tcp")
	}
	tc := config.TunnelConfig{
		Name:       name,
		Type:       kind,
		Local:      local,
		Encryption: encrypted,
		Enabled:    config.Bool(true),
	}
	if sub != "" {
		if kind == "http" {
			tc.Subdomain = strings.ToLower(sub)
		} else {
			port, err := strconv.Atoi(sub)
			if err != nil || port < 1 || port > 65535 {
				return config.TunnelConfig{}, fmt.Errorf("TCP tunnel suffix must be a remote port (got %q)", sub)
			}
			tc.RemotePort = port
		}
	}
	return tc, nil
}

func addClientFlags(fs *flag.FlagSet) *clientFlagValues {
	v := &clientFlagValues{}
	fs.StringVar(&v.server, "server", "", "server control address (host:port)")
	fs.StringVar(&v.token, "token", "", "client authentication token")
	fs.StringVar(&v.name, "name", "", "client name used for tunnel ownership")
	fs.Var(&v.tunnels, "tunnel", "repeatable: name=type://local[@subdomain|@port][#enc]")
	fs.BoolVar(&v.insecure, "insecure", false, "skip TLS certificate verification (development)")
	fs.StringVar(&v.streamSecret, "stream-secret", "", "shared secret enabling per-tunnel stream encryption (must match the server)")
	fs.StringVar(&v.caFile, "ca-file", "", "CA bundle for server verification")
	fs.StringVar(&v.certFile, "client-cert", "", "mTLS client certificate")
	fs.StringVar(&v.keyFile, "client-key", "", "mTLS client key")
	fs.StringVar(&v.webuiAddr, "webui-addr", "", "local console listen address")
	fs.StringVar(&v.webuiToken, "webui-token", "", "local console access token")
	// Friendlier name for the same secret: the console is unlocked with a LOCAL
	// password, not with the tx_ token used to reach the server.
	fs.StringVar(&v.webuiToken, "webui-password", "", "local console login password (alias of --webui-token)")
	return v
}

func clientOverrides(set map[string]bool, v *clientFlagValues) func(*config.Config) {
	return func(c *config.Config) {
		if set["server"] {
			c.Client.Server = v.server
		}
		if set["token"] {
			c.Client.Token = v.token
		}
		if set["name"] {
			c.Client.ClientName = v.name
		}
		if set["tunnel"] {
			c.Client.Tunnels = append([]config.TunnelConfig(nil), v.tunnels.items...)
		}
		if set["insecure"] {
			c.Client.TLS.InsecureSkipVerify = v.insecure
		}
		if set["stream-secret"] {
			c.Client.StreamSecret = v.streamSecret
		}
		if set["ca-file"] {
			c.Client.TLS.CAFile = v.caFile
		}
		if set["client-cert"] {
			c.Client.TLS.CertFile = v.certFile
		}
		if set["client-key"] {
			c.Client.TLS.KeyFile = v.keyFile
		}
		if set["webui-addr"] {
			c.Client.WebUI.Addr = v.webuiAddr
		}
		if set["webui-token"] || set["webui-password"] {
			c.Client.WebUI.Token = v.webuiToken
		}
	}
}

func loadManager(opts config.Options) (*config.Manager, *logx.Logger, error) {
	mgr, err := config.NewManager(opts)
	if err != nil {
		return nil, nil, err
	}
	logger, err := logx.New(mgr.Current().Log)
	if err != nil {
		return nil, nil, err
	}
	for _, warning := range mgr.Warnings() {
		logger.Warn("configuration warning",
			zap.String("path", warning.Path),
			zap.String("message", warning.Message),
			zap.String("hint", warning.Hint))
	}
	mgr.SetErrorHandler(func(err error) {
		logger.Warn("configuration reload rejected; keeping the previous configuration active", zap.Error(err))
	})
	return mgr, logger, nil
}

func runServer(ctx context.Context, opts config.Options, args []string) int {
	opts.Role = "server"
	fs, common := newCommonFlagSet(cmdName() + " server")
	sv := addServerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	set := setFlags(fs)
	base := commonOverrides(set, common)
	extra := serverOverrides(set, sv)
	opts.Overrides = func(c *config.Config) {
		base(c)
		extra(c)
	}
	mgr, logger, err := loadManager(opts)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = logger.Sync() }()
	go func() {
		if err := mgr.Watch(ctx); err != nil {
			logger.Warn("configuration hot reload is unavailable", zap.Error(err))
		}
	}()
	srv, err := server.New(mgr, logger)
	if err != nil {
		logger.Error("server initialization failed", zap.Error(err))
		return 1
	}
	maybeOpenConsole("http://" + loopbackAddr(mgr.Current().Server.WebUI.Addr) + "/admin/")
	if err := srv.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server stopped", zap.Error(err))
		return 1
	}
	return 0
}

func runClient(ctx context.Context, opts config.Options, args []string) int {
	opts.Role = "client"
	fs, common := newCommonFlagSet(cmdName() + " client")
	cv := addClientFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	set := setFlags(fs)
	base := commonOverrides(set, common)
	extra := clientOverrides(set, cv)
	opts.Overrides = func(c *config.Config) {
		base(c)
		extra(c)
	}
	mgr, logger, err := loadManager(opts)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = logger.Sync() }()
	go func() {
		if err := mgr.Watch(ctx); err != nil {
			logger.Warn("configuration hot reload is unavailable", zap.Error(err))
		}
	}()
	agent, err := client.New(mgr, logger)
	if err != nil {
		logger.Error("client initialization failed", zap.Error(err))
		return 1
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// StartSessionGC runs a blocking loop until runCtx is cancelled (it owns a
	// 10-minute ticker), so it must never be called inline: doing so would stop
	// the console listener from ever binding.
	go agent.StartSessionGC(runCtx)

	var uiServer *http.Server
	uiErr := make(chan error, 1)
	if cfg := mgr.Current(); cfg.Client.WebUI.Enabled {
		ln, err := net.Listen("tcp", cfg.Client.WebUI.Addr)
		if err != nil {
			logger.Error("console listener failed", zap.Error(err))
			return 1
		}
		uiServer = &http.Server{
			Handler:           agent.WebUIHandler(),
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       90 * time.Second,
			BaseContext:       func(net.Listener) context.Context { return runCtx },
		}
		go func() {
			if err := uiServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				uiErr <- err
			}
		}()
		// SECURITY: the console password is a bearer credential. On a fresh
		// install it is printed once per start and persisted with 0600
		// permissions. After the operator changes it on the Security page the
		// argon2id hash in the config is authoritative, so do not print the
		// stale startup secret.
		fields := []zap.Field{zap.String("url", "http://"+ln.Addr().String()+"/")}
		if password := agent.WebToken(); password != "" {
			fields = append(fields,
				zap.String("password", password),
				zap.String("note", "local login password; the server token (tx_) belongs in the Config page"))
		} else {
			fields = append(fields,
				zap.String("note", "console password is managed on the Security page; change it there, the tx_ token belongs in the Config page"))
		}
		logger.Info("client console is ready", fields...)
		maybeOpenConsole("http://" + loopbackAddr(ln.Addr().String()) + "/")
	}

	agentErr := make(chan error, 1)
	go func() { agentErr <- agent.Run(runCtx) }()

	select {
	case <-ctx.Done():
	case err := <-uiErr:
		logger.Error("console server failed", zap.Error(err))
		cancel()
		return 1
	case err := <-agentErr:
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("client stopped", zap.Error(err))
			return 1
		}
	}

	cancel()
	if uiServer != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = uiServer.Shutdown(shutdownCtx)
		shutdownCancel()
	}
	select {
	case <-agentErr:
	case <-time.After(5 * time.Second):
	}
	return 0
}

func runConfig(opts config.Options, args []string) int {
	if len(args) == 0 || (args[0] != "show" && args[0] != "validate") {
		fmt.Fprintf(os.Stderr, "usage: %s config <show|validate> [--config file] [--profile name]\n", cmdName())
		return 2
	}
	action := args[0]
	fs, common := newCommonFlagSet(cmdName() + " config")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	set := setFlags(fs)
	opts.Overrides = commonOverrides(set, common)
	mgr, err := config.NewManager(opts)
	if err != nil {
		return fail(err)
	}
	for _, warning := range mgr.Warnings() {
		fmt.Fprintf(os.Stderr, "warning: %s\n", warning.String())
	}
	if action == "validate" {
		fmt.Println("configuration OK")
		fmt.Printf("files:\n")
		files := mgr.Files()
		if len(files) == 0 {
			fmt.Println("  (built-in defaults)")
		}
		for _, file := range files {
			fmt.Printf("  %s\n", file)
		}
		if p, err := mgr.WritablePath(); err == nil {
			fmt.Printf("writable: %s\n", p)
		}
		return 0
	}
	raw, err := mgr.YAML()
	if err != nil {
		return fail(err)
	}
	fmt.Println("# resolved configuration - contains secrets, do not share")
	os.Stdout.Write(raw)
	return 0
}

func runPasswordHash(args []string) int {
	var password string
	if len(args) > 0 {
		password = strings.Join(args, " ")
	} else {
		info, err := os.Stdin.Stat()
		if err != nil || info.Mode()&os.ModeCharDevice == 0 {
			raw, _ := io.ReadAll(os.Stdin)
			password = strings.TrimSpace(string(raw))
		}
	}
	if password == "" {
		fmt.Fprintf(os.Stderr, "usage: %s password-hash <password>\n", cmdName())
		return 2
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return fail(err)
	}
	fmt.Println(hash)
	return 0
}

type initValues struct {
	role      string
	path      string
	server    string
	token     string
	name      string
	local     string
	domain    string
	webuiAddr string
	force     bool
	yes       bool
}

func runInit(args []string) int {
	fs := flag.NewFlagSet("wormgate init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	v := &initValues{}
	fs.StringVar(&v.role, "role", "client", "client|server|both")
	fs.StringVar(&v.path, "path", "wormgate.yaml", "output configuration file")
	fs.StringVar(&v.server, "server", "127.0.0.1:7000", "server control address")
	fs.StringVar(&v.token, "token", "", "client token")
	fs.StringVar(&v.name, "name", "web", "tunnel name")
	fs.StringVar(&v.local, "local", "", "local service, e.g. http://127.0.0.1:8080")
	fs.StringVar(&v.domain, "domain", "wormgate.local", "base domain for HTTP tunnels")
	fs.StringVar(&v.webuiAddr, "webui-addr", "", "console address")
	fs.BoolVar(&v.force, "force", false, "overwrite an existing file")
	fs.BoolVar(&v.yes, "yes", false, "non-interactive: accept defaults")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if v.role != "client" && v.role != "server" && v.role != "both" {
		fmt.Fprintln(os.Stderr, "wormgate init: --role must be client, server or both")
		return 2
	}
	if !v.force {
		if _, err := os.Stat(v.path); err == nil {
			fmt.Fprintf(os.Stderr, "wormgate init: %s already exists (use --force to overwrite)\n", v.path)
			return 1
		}
	}
	if !v.yes && stdinIsTerminal() {
		interactiveInit(v)
	}
	doc, err := renderInitConfig(*v)
	if err != nil {
		return fail(err)
	}
	if err := writeInitFile(v.path, doc); err != nil {
		return fail(err)
	}
	fmt.Printf("wrote %s\n\n", v.path)
	serverCmd, clientCmd := "wormgate server", "wormgate client"
	if bin := cmdName(); bin == "fps" || bin == "fpc" {
		serverCmd, clientCmd = "fps", "fpc"
	}
	fmt.Println("next steps:")
	switch v.role {
	case "server":
		fmt.Printf("  %s --config %s\n", serverCmd, v.path)
	case "client":
		fmt.Printf("  %s --config %s\n", clientCmd, v.path)
	default:
		fmt.Printf("  %s --config %s\n", serverCmd, v.path)
		fmt.Printf("  %s --config %s\n", clientCmd, v.path)
	}
	if v.token == "" && v.role != "server" {
		fmt.Println("\nnote: client.token is empty. Start the server once; it prints a demo token (or create a user token in the admin console).")
	}
	return 0
}

func interactiveInit(v *initValues) {
	sc := bufio.NewScanner(os.Stdin)
	ask := func(label, def string) string {
		if def != "" {
			fmt.Printf("%s [%s]: ", label, def)
		} else {
			fmt.Printf("%s: ", label)
		}
		if !sc.Scan() {
			fmt.Println()
			return def
		}
		answer := strings.TrimSpace(sc.Text())
		if answer == "" {
			return def
		}
		return answer
	}
	if v.role == "client" || v.role == "both" {
		v.server = ask("server address", v.server)
		v.token = ask("client token (from the server log or admin console)", v.token)
		v.local = ask("local service to expose (http://127.0.0.1:8080)", v.local)
		if v.local != "" {
			v.name = ask("tunnel name", v.name)
		}
	}
	if v.role == "server" || v.role == "both" {
		v.domain = ask("base domain for HTTP tunnels", v.domain)
	}
	if v.webuiAddr == "" {
		if v.role == "server" {
			v.webuiAddr = "0.0.0.0:7100"
		} else {
			v.webuiAddr = "127.0.0.1:7200"
		}
	}
	v.webuiAddr = ask("console listen address", v.webuiAddr)
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func renderInitConfig(v initValues) (string, error) {
	var b strings.Builder
	b.WriteString("# Wormgate configuration\n")
	b.WriteString("# Precedence: CLI flags > env (WORMGATE_*) > project file > global file > defaults\n")
	b.WriteString("# Validate with: <binary> config validate --config <file>\n\n")
	b.WriteString("profile: dev\n\n")
	b.WriteString("log:\n  level: info\n  format: console\n")

	if v.role == "server" || v.role == "both" {
		b.WriteString("\nserver:\n")
		b.WriteString("  control_addr: \"0.0.0.0:7000\"\n")
		b.WriteString("  tls:\n    auto_self_signed: true   # development only; provide cert_file/key_file in production\n")
		b.WriteString("  http:\n    addr: \"0.0.0.0:8080\"\n")
		fmt.Fprintf(&b, "    domain: %s\n", strconv.Quote(v.domain))
		b.WriteString("  tcp:\n    bind_addr: \"0.0.0.0\"\n    port_range: \"20000-30000\"\n")
		b.WriteString("  webui:\n    enabled: true\n")
		adminAddr := "0.0.0.0:7100"
		if v.role == "server" && v.webuiAddr != "" {
			adminAddr = v.webuiAddr
		}
		fmt.Fprintf(&b, "    addr: %s\n", strconv.Quote(adminAddr))
		b.WriteString("    admin_user: admin\n")
		b.WriteString("  metrics:\n    enabled: true\n    addr: \"0.0.0.0:9090\"\n")
		b.WriteString("  data_dir: \"data\"\n")
	}

	if v.role == "client" || v.role == "both" {
		clientAddr := "127.0.0.1:7200"
		if v.role == "client" && v.webuiAddr != "" {
			clientAddr = v.webuiAddr
		}
		b.WriteString("\nclient:\n")
		fmt.Fprintf(&b, "  server: %s\n", strconv.Quote(v.server))
		fmt.Fprintf(&b, "  token: %s\n", strconv.Quote(v.token))
		b.WriteString("  tls:\n    insecure_skip_verify: true   # dev only: accepts the server's self-signed certificate\n")
		if strings.TrimSpace(v.local) != "" {
			tc, err := tunnelFromLocal(v.local, v.name)
			if err != nil {
				return "", err
			}
			b.WriteString("  tunnels:\n")
			fmt.Fprintf(&b, "    - name: %s\n", strconv.Quote(tc.Name))
			fmt.Fprintf(&b, "      type: %s\n", tc.Type)
			fmt.Fprintf(&b, "      local: %s\n", strconv.Quote(tc.Local))
			b.WriteString("      enabled: true\n")
		} else {
			b.WriteString("  tunnels: []\n")
		}
		b.WriteString("  webui:\n    enabled: true\n")
		fmt.Fprintf(&b, "    addr: %s\n", strconv.Quote(clientAddr))
		b.WriteString("    data_dir: \"data\"\n")
	}

	b.WriteString("\nprofiles:\n")
	b.WriteString("  dev:\n    log:\n      level: debug\n")
	b.WriteString("  prod:\n    log:\n      level: info\n")
	return b.String(), nil
}

func tunnelFromLocal(local, name string) (config.TunnelConfig, error) {
	local = strings.TrimSpace(local)
	if local == "" {
		return config.TunnelConfig{}, fmt.Errorf("local address is empty")
	}
	kind := "http"
	addr := local
	if strings.Contains(local, "://") {
		u, err := url.Parse(local)
		if err != nil || u.Host == "" {
			return config.TunnelConfig{}, fmt.Errorf("invalid local address %q", local)
		}
		if u.Path != "" && u.Path != "/" {
			return config.TunnelConfig{}, fmt.Errorf("local address must not include a path")
		}
		kind = u.Scheme
		addr = u.Host
	}
	if kind != "http" && kind != "tcp" {
		return config.TunnelConfig{}, fmt.Errorf("local scheme must be http or tcp")
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return config.TunnelConfig{}, fmt.Errorf("local address must be host:port: %w", err)
	}
	if strings.TrimSpace(name) == "" {
		name = "web"
	}
	return config.TunnelConfig{Name: name, Type: kind, Local: addr, Enabled: config.Bool(true)}, nil
}

func writeInitFile(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// cmdName returns the invoked binary name so help text, errors and generated
// commands always match the executable the user actually ran (fps / fpc /
// tunnelx), including on Windows where os.Args[0] ends in ".exe".
func cmdName() string {
	return strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
}

func fail(err error) int {
	var list *config.ErrorList
	if errors.As(err, &list) {
		fmt.Fprintf(os.Stderr, "%s: %v\n", cmdName(), err)
		fmt.Fprintf(os.Stderr, "\nhint: run `%s config show --config <file>` to inspect the resolved configuration.\n", cmdName())
		return 1
	}
	fmt.Fprintf(os.Stderr, "wormgate: %v\n", err)
	return 1
}

func usage() {
	fmt.Fprintf(os.Stderr, `Wormgate %s - modern reverse tunnel

Usage:
  wormgate server [flags]        run the control plane and public ingress
  wormgate client [flags]        connect this machine to a server
  wormgate init [flags]          generate a configuration file
  wormgate config show|validate  inspect or validate configuration
  wormgate password-hash <pw>    generate an argon2id hash for console passwords
  wormgate version               print version information

Common flags:
  --config <file>               project config (default: ./wormgate.yaml if present)
  --profile <name>              apply profiles.<name> overlay
  --no-global                   ignore the per-user global config
  --log-level <level>           debug|info|warn|error
  --log-format <format>         console|json

Server flags:
  --control-addr <host:port>    control/data listener
  --http-addr <host:port>       public HTTP ingress
  --http-domain <domain>        base domain for subdomain routing
  --tcp-port-range <a-b>        automatic TCP port range
  --webui-addr <host:port>      admin console
  --metrics-addr <host:port>    Prometheus endpoint
  --data-dir <dir>              persisted state
  --tls-cert/--tls-key <file>   production certificate
  --tls-client-ca <file>        require client certificates (mTLS)
  --token <token[,token]>       extra static client token

Client flags:
  --server <host:port>          server control address
  --token <token>               authentication token
  --name <name>                 client name
  --tunnel name=http://host:port[@subdomain]
  --tunnel name=tcp://host:port[@port]
  --insecure                    skip TLS verification (development only)
  --ca-file <file>              custom CA bundle
  --webui-addr <host:port>      local console
  --webui-token <token>         console access token

Example (local development, two terminals):
  wormgate server
  wormgate client --server 127.0.0.1:7000 --token <printed-token> \
      --tunnel web=http://127.0.0.1:8080 --insecure

Zero-config startup prints the generated admin password (first run) and a
demo client token. Configuration reloads automatically without dropping
established tunnels.
`, version.Version)
}
