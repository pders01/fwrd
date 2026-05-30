package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	tea "github.com/charmbracelet/bubbletea"
	charmlog "github.com/charmbracelet/log"
	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/debuglog"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/mdns"
	"github.com/pders01/fwrd/internal/netbind"
	"github.com/pders01/fwrd/internal/opml"
	"github.com/pders01/fwrd/internal/plugins"
	pluginlua "github.com/pders01/fwrd/internal/plugins/lua"
	"github.com/pders01/fwrd/internal/search"
	"github.com/pders01/fwrd/internal/service"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/tui"
	"github.com/pders01/fwrd/internal/validation"
	"github.com/pders01/fwrd/internal/web"
)

// logger is the CLI's operational logger: styled, leveled output on stderr
// via charm's log (colored level badges, timestamps, key=value rendering).
// It carries plugin-load and serve diagnostics; the file-based debuglog
// package is separate and handles verbose per-request tracing.
var logger = charmlog.NewWithOptions(os.Stderr, charmlog.Options{ReportTimestamp: true})

// pluginLogger adapts charm's log to plugins/lua's printf-style Logger
// interface so plugin load events flow through the same styled output.
type pluginLogger struct{}

func (pluginLogger) Infof(format string, args ...any) { logger.Infof(format, args...) }
func (pluginLogger) Warnf(format string, args ...any) { logger.Warnf(format, args...) }

// loadLuaPlugins registers user-authored Lua plugins onto m's registry.
// Failures are logged and ignored — a malformed plugin must not break
// CLI commands that don't depend on it.
func loadLuaPlugins(m *feed.Manager) {
	dir := pluginlua.DefaultPluginDir()
	if err := pluginlua.EnsureDefaults(dir); err != nil {
		logger.Warn("seeding default lua plugins", "dir", dir, "err", err)
	}
	bindings := pluginlua.Bindings{
		HTTPClient: m.PluginHTTPClient(),
		Logger:     pluginLogger{},
	}
	if _, err := pluginlua.LoadAndRegister(m.PluginRegistry(), dir, bindings); err != nil {
		logger.Warn("loading lua plugins", "dir", dir, "err", err)
	}
}

// Version is the version of the application, set at build time
var Version = "dev"

var (
	cfgFile       string
	dbPath        string
	debugFlag     bool
	quiet         bool
	forceRefresh  bool
	serveAddr     string
	serveMDNS     bool
	serveMDNSName string
	serveMDNSIP   string
	svcAddr       string
	svcMDNS       bool
	svcMDNSName   string
	netIface      string
	netAliasIP    string
	netPort       int
	netToPort     int
	netPrefix     int
	netMask       string
	logsFollow    bool
	logsLines     int
	logsService   bool
)

var rootCmd = &cobra.Command{
	Use:   "fwrd",
	Short: "A terminal-based RSS feed aggregator",
	Long:  `fwrd is a fast, terminal-based RSS feed aggregator with full-text search capabilities.`,
	Run:   runTUI,
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.config/fwrd/config.toml)")
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "", "database file path (overrides config)")
	rootCmd.PersistentFlags().BoolVar(&debugFlag, "debug", false, "enable debug logging to ~/.fwrd/fwrd.log")

	// TUI-specific flags
	rootCmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "skip startup banner")
	rootCmd.Flags().BoolVar(&forceRefresh, "force", false, "ignore ETag/Last-Modified headers on refresh")
	rootCmd.Flags().BoolVar(&forceRefresh, "force-refresh", false, "deprecated alias for --force")
	_ = rootCmd.Flags().MarkDeprecated("force-refresh", "use --force")

	// serve flags
	serveCmd.Flags().StringVar(&serveAddr, "addr", "127.0.0.1:8080", "address to bind the web server")
	serveCmd.Flags().BoolVar(&serveMDNS, "mdns", false, "advertise the web view on the LAN over mDNS (e.g. http://fwrd.local:PORT)")
	serveCmd.Flags().StringVar(&serveMDNSName, "mdns-name", "fwrd", "mDNS hostname label; advertised as <name>.local")
	serveCmd.Flags().StringVar(&serveMDNSIP, "mdns-ip", "", "advertise <name>.local for only this IP (e.g. the alias IP from `fwrd net up`); default: all LAN IPv4s")

	// net flags: the alias-IP + firewall redirect that exposes fwrd.local on
	// port 80 without colliding with a host process already on :80.
	netUpCmd.Flags().StringVar(&netIface, "iface", "", "LAN interface to attach the alias IP to (e.g. en0 or eth0)")
	netUpCmd.Flags().StringVar(&netAliasIP, "alias-ip", "", "dedicated, currently-unused LAN IP to give fwrd")
	netUpCmd.Flags().IntVar(&netPort, "port", 80, "public port to redirect from")
	netUpCmd.Flags().IntVar(&netToPort, "to-port", 8080, "fwrd's unprivileged port to redirect to")
	netUpCmd.Flags().IntVar(&netPrefix, "prefix", 24, "CIDR prefix length for the alias IP (Linux)")
	netUpCmd.Flags().StringVar(&netMask, "mask", "255.255.255.0", "netmask for the alias IP (macOS)")
	netCmd.AddCommand(netUpCmd)
	netCmd.AddCommand(netDownCmd)
	netCmd.AddCommand(netStatusCmd)

	// logs flags
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "stream new log lines as they are written")
	logsCmd.Flags().IntVarP(&logsLines, "lines", "n", 200, "number of trailing lines to show")
	logsCmd.Flags().BoolVar(&logsService, "service", false, "show the background service's logs instead of fwrd's debug log")

	// service flags: default to a LAN bind + mDNS, since a background
	// service exists to be reachable from other devices as fwrd.local.
	serviceInstallCmd.Flags().StringVar(&svcAddr, "addr", "0.0.0.0:8080", "address the service binds")
	serviceInstallCmd.Flags().BoolVar(&svcMDNS, "mdns", true, "advertise the service over mDNS as <name>.local")
	serviceInstallCmd.Flags().StringVar(&svcMDNSName, "mdns-name", "fwrd", "mDNS hostname label; advertised as <name>.local")
	serviceCmd.AddCommand(serviceInstallCmd)
	serviceCmd.AddCommand(serviceUninstallCmd)

	// Add commands
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(feedCmd)
	rootCmd.AddCommand(pluginsCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(serviceCmd)
	rootCmd.AddCommand(netCmd)
	rootCmd.AddCommand(logsCmd)
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve a read-only web view of stored feeds and articles",
	Long: `serve starts an HTTP server rendering the same feeds, articles, and
search backing the TUI. Article content is served as sanitized HTML rather
than the lossy terminal markdown the TUI must use.

The web server holds the database open for its lifetime, so it cannot run
against the same --db as a concurrent TUI (BoltDB is single-process).`,
	Run: runServe,
}

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Install or remove fwrd as a background web service",
	Long: `service manages a per-user background service that runs "fwrd serve":
a systemd user unit on Linux, a launchd LaunchAgent on macOS. It installs
under your home directory and needs no root.`,
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install and start the fwrd web service for the current user",
	Run:   runServiceInstall,
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Stop and remove the fwrd web service",
	Run:   runServiceUninstall,
}

var netCmd = &cobra.Command{
	Use:   "net",
	Short: "Expose fwrd at http://fwrd.local on port 80 (alias IP + firewall redirect)",
	Long: `net makes the web view reachable at http://<name>.local (port 80) without
binding a privileged port and without colliding with any server the host
already runs on port 80.

It gives fwrd its own LAN IP (an alias on your network interface) and installs
a firewall redirect — pf on macOS, nftables on Linux — from that IP's port 80
to fwrd's unprivileged port. fwrd then advertises <name>.local pointing at the
alias IP (serve --mdns-ip).

Because it changes interface and firewall state, "net up"/"net down" need root
(sudo). The binding is not reboot-persistent; re-run "fwrd net up" afterward.`,
}

var netUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Assign the alias IP and install the port-80 redirect (needs sudo)",
	Run:   runNetUp,
}

var netDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Remove the alias IP and redirect installed by `net up` (needs sudo)",
	Run:   runNetDown,
}

var netStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the active port-80 binding, if any",
	Run:   runNetStatus,
}

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Tail fwrd's logs (debug log, or the background service with --service)",
	Long: `logs is a convenience wrapper around the underlying log tools. By default
it tails fwrd's own debug log (~/.fwrd/fwrd.log). With --service it shows the
background service's output instead: journalctl on Linux, the LaunchAgent's
~/.fwrd/serve.*.log files on macOS.`,
	Run: runLogs,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("fwrd %s\n", Version)
		fmt.Println("RSS aggregator")
		fmt.Println("github.com/pders01/fwrd")
	},
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration management",
}

var configGenCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate default configuration file",
	Run: func(_ *cobra.Command, _ []string) {
		home, _ := os.UserHomeDir()
		configDir := filepath.Join(home, ".config", "fwrd")
		configFile := filepath.Join(configDir, "config.toml")

		if err := config.GenerateDefaultConfig(configFile); err != nil {
			logger.Fatal("failed to generate config", "err", err)
		}
		fmt.Printf("Generated default configuration at: %s\n", configFile)
	},
}

var feedCmd = &cobra.Command{
	Use:   "feed",
	Short: "Feed management commands",
}

var feedListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all feeds",
	Run:   listFeeds,
}

var feedAddCmd = &cobra.Command{
	Use:   "add [URL]",
	Short: "Add a new feed",
	Args:  cobra.ExactArgs(1),
	Run:   addFeed,
}

var feedDeleteCmd = &cobra.Command{
	Use:   "delete [URL or ID]",
	Short: "Delete a feed",
	Args:  cobra.ExactArgs(1),
	Run:   deleteFeed,
}

var feedRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Refresh all feeds",
	Run:   refreshFeeds,
}

var feedExportCmd = &cobra.Command{
	Use:   "export [path]",
	Short: "Export feeds to an OPML file",
	Long: `export writes all stored feeds to an OPML 2.0 file other readers can
import. Pass "-" as the path to write to stdout.`,
	Args: cobra.ExactArgs(1),
	Run:  exportFeeds,
}

var feedImportCmd = &cobra.Command{
	Use:   "import [path]",
	Short: "Import feeds from an OPML file",
	Long: `import reads an OPML file and adds each listed feed, fetching it once
so its articles are available immediately. Feeds that are already present or
fail to fetch are reported and skipped; the rest still import. Pass "-" to
read from stdin.`,
	Args: cobra.ExactArgs(1),
	Run:  importFeeds,
}

var pluginsCmd = &cobra.Command{
	Use:   "plugins",
	Short: "Inspect installed plugins",
}

var pluginsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List loaded Lua plugins",
	Run:   listPlugins,
}

func init() {
	configCmd.AddCommand(configGenCmd)
	feedCmd.AddCommand(feedListCmd)
	feedCmd.AddCommand(feedAddCmd)
	feedCmd.AddCommand(feedDeleteCmd)
	feedCmd.AddCommand(feedRefreshCmd)
	feedCmd.AddCommand(feedExportCmd)
	feedCmd.AddCommand(feedImportCmd)
	pluginsCmd.AddCommand(pluginsListCmd)

	// Add force flag to refresh command (with a deprecated alias matching
	// the root TUI flag, so the same name works in both contexts).
	feedRefreshCmd.Flags().BoolVar(&forceRefresh, "force", false, "ignore ETag/Last-Modified headers")
	feedRefreshCmd.Flags().BoolVar(&forceRefresh, "force-refresh", false, "deprecated alias for --force")
	_ = feedRefreshCmd.Flags().MarkDeprecated("force-refresh", "use --force")
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		viper.AddConfigPath(filepath.Join(home, ".config", "fwrd"))
		viper.AddConfigPath(".")
		viper.SetConfigName("config")
		viper.SetConfigType("toml")
	}

	viper.SetEnvPrefix("FWRD")
	viper.AutomaticEnv()
}

func loadConfig() (*config.Config, error) {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, err
	}
	// When --db overrides the database path, the search index must follow
	// it; otherwise a custom-db instance collides with the default
	// ~/.fwrd/index.bleve and blocks on its lock. Relocating the index
	// makes --db a fully self-contained instance.
	if dbPath != "" {
		cfg.Database.SearchIndex = deriveIndexPath(dbPath)
	}
	return cfg, nil
}

// deriveIndexPath returns the Bleve index path sited next to a database
// path, mirroring the fallback used when no index is configured.
func deriveIndexPath(dbFilePath string) string {
	switch dbFilePath {
	case "", storage.MemoryPath:
		return "fwrd.bleve"
	default:
		return strings.TrimSuffix(dbFilePath, filepath.Ext(dbFilePath)) + ".bleve"
	}
}

func getStore(cfg *config.Config) (*storage.Store, error) {
	// Override database path if provided via flag
	dbFilePath := cfg.Database.Path
	if dbPath != "" {
		dbFilePath = dbPath
	}

	// Use secure path handler for validation
	pathHandler := validation.NewSecurePathHandler()
	validatedPath, err := pathHandler.GetSecureDBPath(dbFilePath)
	if err != nil {
		return nil, fmt.Errorf("invalid database path: %w", err)
	}

	// Ensure the parent directory exists before opening the database
	dbDir := filepath.Dir(validatedPath)
	if _, err := pathHandler.EnsureSecureDirectory(dbDir); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	return storage.NewStoreWithTimeout(validatedPath, cfg.Database.Timeout)
}

// withStore provides consistent resource management for store operations
func withStore(fn func(*storage.Store) error) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := getStore(cfg)
	if err != nil {
		return fmt.Errorf("failed to open store: %w", err)
	}
	defer store.Close()

	return fn(store)
}

// withStoreAndConfig provides access to both store and config with proper cleanup
func withStoreAndConfig(fn func(*storage.Store, *config.Config) error) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := getStore(cfg)
	if err != nil {
		return fmt.Errorf("failed to open store: %w", err)
	}
	defer store.Close()

	return fn(store, cfg)
}

func runTUI(_ *cobra.Command, _ []string) {
	if !quiet {
		tui.ShowBanner(Version)
	}

	// Setup debug logging if requested
	if debugFlag {
		debuglog.SetupWithBool(true)
	}

	if err := withStoreAndConfig(func(store *storage.Store, cfg *config.Config) error {
		for _, w := range config.Warnings(cfg) {
			logger.Warn(w)
		}
		app := tui.NewApp(store, cfg)
		defer app.Close()

		// Pass force refresh option to TUI
		if forceRefresh {
			app.SetForceRefresh(true)
		}

		p := tea.NewProgram(app, tea.WithAltScreen())

		if _, err := p.Run(); err != nil {
			return fmt.Errorf("TUI error: %w", err)
		}

		return nil
	}); err != nil {
		exitWithError(err)
	}
}

// buildSearcher constructs the Bleve-backed searcher, mirroring the index
// path resolution the TUI uses. A locked index (another fwrd holding it) is
// returned as an error so the caller can fail loudly with a hint; any other
// bleve failure falls back to the basic in-memory engine so search still
// works, just less well.
func buildSearcher(store *storage.Store, cfg *config.Config) (search.Searcher, error) {
	idxPath := cfg.Database.SearchIndex
	if idxPath == "" {
		idxPath = deriveIndexPath(cfg.Database.Path)
	}
	be, err := search.NewBleveEngine(store, idxPath)
	if err == nil && be != nil {
		return be, nil
	}
	if errors.Is(err, search.ErrIndexLocked) {
		return nil, err
	}
	return search.NewEngine(store), nil
}

func runServe(_ *cobra.Command, _ []string) {
	if debugFlag {
		debuglog.SetupWithBool(true)
	}

	if err := withStoreAndConfig(func(store *storage.Store, cfg *config.Config) error {
		for _, w := range config.Warnings(cfg) {
			logger.Warn(w)
		}

		searcher, err := buildSearcher(store, cfg)
		if err != nil {
			return err
		}

		// Wire the manager exactly as the TUI does so feeds added or
		// refreshed via the web UI are indexed for search.
		manager := feed.NewManager(store, cfg)
		loadLuaPlugins(manager)
		if dl, ok := searcher.(feed.DataListener); ok {
			manager.RegisterDataListener(dl)
		}
		if bs, ok := searcher.(feed.BatchScope); ok {
			manager.RegisterBatchScope(bs)
		}

		srv, err := web.NewServer(store, manager, searcher, cfg)
		if err != nil {
			return fmt.Errorf("failed to build web server: %w", err)
		}

		// Bind before announcing anything: if the port is taken, fail fast with
		// a clear error rather than logging "serving" and advertising an mDNS
		// name for a server that never came up.
		ln, err := srv.Listen(serveAddr)
		if err != nil {
			return err
		}

		if !isLoopbackBind(serveAddr) && !srv.AuthEnabled() {
			logger.Warn("serving on a non-loopback address without authentication; "+
				"anyone who can reach it can read and modify your feeds",
				"fix", "set [web.auth] in your config, or front it with a reverse proxy "+
					"handling auth/TLS (README: \"Exposing the web view\")")
		}

		if serveMDNS {
			if adv := startMDNS(serveAddr); adv != nil {
				defer func() { _ = adv.Close() }()
			}
		}

		logger.Info("serving", "url", "http://"+serveAddr)
		if err := srv.Serve(ln); err != nil {
			return fmt.Errorf("web server error: %w", err)
		}
		return nil
	}); err != nil {
		exitWithError(err)
	}
}

func runServiceInstall(_ *cobra.Command, _ []string) {
	bin, err := os.Executable()
	if err != nil {
		logger.Fatal("cannot resolve the fwrd binary path", "err", err)
	}
	// Resolve symlinks so the unit points at the real binary, not a launcher
	// shim that might move.
	if resolved, rerr := filepath.EvalSymlinks(bin); rerr == nil {
		bin = resolved
	}
	path, err := service.Install(&service.Options{
		BinPath:  bin,
		Addr:     svcAddr,
		MDNS:     svcMDNS,
		MDNSName: svcMDNSName,
		Config:   cfgFile,
		DB:       dbPath,
	})
	if err != nil {
		// A non-empty path means the file was written but activation failed —
		// surface the path so the user can enable it by hand.
		if path != "" {
			logger.Error("service file written but activation failed", "path", path, "err", err)
			os.Exit(1)
		}
		logger.Fatal("service install failed", "err", err)
	}
	logger.Info("service installed and started", "path", path)
	if svcMDNS {
		if _, port, perr := net.SplitHostPort(svcAddr); perr == nil {
			logger.Info("reachable on the LAN", "url", "http://"+svcMDNSName+".local:"+port)
		}
	}
}

func runServiceUninstall(_ *cobra.Command, _ []string) {
	path, err := service.Uninstall()
	if err != nil {
		logger.Fatal("service uninstall failed", "err", err)
	}
	logger.Info("service removed", "path", path)
}

func runNetUp(_ *cobra.Command, _ []string) {
	if !netbind.Supported() {
		logger.Fatal("fwrd net is only supported on Linux and macOS")
	}
	st, err := netbind.Up(&netbind.Options{
		Iface:   netIface,
		AliasIP: netAliasIP,
		Port:    netPort,
		ToPort:  netToPort,
		Prefix:  netPrefix,
		Mask:    netMask,
	})
	if err != nil {
		logger.Fatal("net up failed", "err", err)
	}
	logger.Info("port-80 redirect installed",
		"alias", st.AliasIP, "iface", st.Iface, "redirect", fmt.Sprintf(":%d → :%d", st.Port, st.ToPort), "backend", st.Backend)
	// The redirect targets the loopback port, so fwrd must accept off-box
	// traffic: bind 0.0.0.0 and advertise the alias IP only.
	logger.Info("now start the server",
		"run", fmt.Sprintf("fwrd serve --addr 0.0.0.0:%d --mdns --mdns-name %s --mdns-ip %s", st.ToPort, serveMDNSName, st.AliasIP))
	logger.Info("then reach it from any LAN device", "url", "http://"+serveMDNSName+".local")
	logger.Info("undo with", "run", "sudo fwrd net down")
}

func runNetDown(_ *cobra.Command, _ []string) {
	st, err := netbind.Down()
	if err != nil {
		logger.Fatal("net down failed", "err", err)
	}
	logger.Info("port-80 redirect removed", "alias", st.AliasIP, "iface", st.Iface, "backend", st.Backend)
}

func runNetStatus(_ *cobra.Command, _ []string) {
	st, err := netbind.Status()
	if err != nil {
		// No binding is a normal state, not a failure.
		logger.Info("no active port-80 binding", "hint", "sudo fwrd net up --iface <if> --alias-ip <ip>")
		return
	}
	logger.Info("active port-80 binding",
		"alias", st.AliasIP, "iface", st.Iface, "redirect", fmt.Sprintf(":%d → :%d", st.Port, st.ToPort),
		"backend", st.Backend, "url", "http://"+serveMDNSName+".local")
}

func runLogs(_ *cobra.Command, _ []string) {
	var name string
	var args []string

	if logsService {
		n, a, err := service.LogCommand(logsFollow, logsLines)
		if err != nil {
			logger.Fatal("cannot locate the service logs", "err", err)
		}
		name, args = n, a
	} else {
		path, err := debuglog.DefaultPath()
		if err != nil {
			logger.Fatal("cannot locate the log file", "err", err)
		}
		if _, serr := os.Stat(path); errors.Is(serr, os.ErrNotExist) {
			logger.Info("no debug log yet", "path", path,
				"hint", "run fwrd with --debug to create it, or `fwrd logs --service` for the background service")
			return
		}
		name = "tail"
		args = []string{"-n", strconv.Itoa(logsLines)}
		if logsFollow {
			args = append(args, "-f")
		}
		args = append(args, path)
	}

	bin, err := exec.LookPath(name)
	if err != nil {
		logger.Fatal("required tool not found on PATH", "tool", name, "err", err)
	}
	c := exec.Command(bin, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		// Pass through the wrapped tool's exit code (e.g. tail on a missing file)
		// rather than masking it behind our own.
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		logger.Fatal("logs command failed", "err", err)
	}
}

// startMDNS advertises the web view over mDNS as <mdns-name>.local. A failure
// is non-fatal — the server still runs, just without the .local alias — so it
// logs and returns nil rather than aborting serve.
func startMDNS(addr string) *mdns.Advertiser {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		logger.Warn("mDNS disabled: cannot parse serve address", "addr", addr, "err", err)
		return nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		logger.Warn("mDNS disabled: invalid port", "port", portStr, "err", err)
		return nil
	}
	if isLoopbackBind(addr) && serveMDNSIP == "" {
		logger.Warn("mDNS advertises a LAN address but the server is bound to loopback; "+
			"clients resolving the .local name cannot reach it",
			"fix", "bind a non-loopback address, e.g. --addr 0.0.0.0:"+portStr)
	}

	var adv *mdns.Advertiser
	if serveMDNSIP != "" {
		// Pin the A record to one address — the dedicated alias IP behind a
		// `fwrd net` redirect — so clients resolve the name to the redirect
		// target rather than every LAN interface.
		ip := net.ParseIP(serveMDNSIP)
		if ip == nil {
			logger.Warn("mDNS disabled: invalid --mdns-ip", "ip", serveMDNSIP)
			return nil
		}
		adv, err = mdns.AdvertiseOn(serveMDNSName, port, []net.IP{ip})
	} else {
		adv, err = mdns.Advertise(serveMDNSName, port)
	}
	if err != nil {
		logger.Warn("mDNS disabled", "err", err)
		return nil
	}
	logger.Info("mDNS advertising", "url", "http://"+serveMDNSName+".local:"+portStr)
	return adv
}

// isLoopbackBind reports whether addr binds only the loopback interface,
// so the warning about unauthenticated exposure is suppressed for the
// default 127.0.0.1 bind. A bare port (":8080") or 0.0.0.0/empty host
// counts as non-loopback because it accepts off-box connections.
func isLoopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// exitWithError prints err to stderr and exits non-zero. For known
// conditions (e.g. another fwrd holding the bolt lock) it adds a hint
// instead of the raw wrapped error.
func exitWithError(err error) {
	if errors.Is(err, storage.ErrDatabaseLocked) {
		fmt.Fprintln(os.Stderr, "Error: another fwrd process is already using the database.")
		fmt.Fprintln(os.Stderr, "Hint: close the other instance, or pass --db to use a different file.")
		os.Exit(1)
	}
	if errors.Is(err, search.ErrIndexLocked) {
		fmt.Fprintln(os.Stderr, "Error: the search index is locked by another fwrd process.")
		fmt.Fprintln(os.Stderr, "Hint: close the other instance, or pass --db to use a different file (the index follows it).")
		os.Exit(1)
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Hint: another process is already on that port. Pick a free one with --addr, "+
			"or expose port 80 without a conflict via `fwrd net up` (see README: \"Serving on port 80\").")
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

func listFeeds(_ *cobra.Command, _ []string) {
	if err := withStore(func(store *storage.Store) error {
		feeds, err := store.GetAllFeeds()
		if err != nil {
			return fmt.Errorf("failed to get feeds: %w", err)
		}

		if len(feeds) == 0 {
			fmt.Println("No feeds found.")
			return nil
		}

		fmt.Printf("Found %d feeds:\n\n", len(feeds))
		for _, feed := range feeds {
			fmt.Printf("Title: %s\n", feed.Title)
			fmt.Printf("URL:   %s\n", feed.URL)
			fmt.Printf("ID:    %s\n", feed.ID)

			// Get article count
			articles, _ := store.GetArticles(feed.ID, 0)
			fmt.Printf("Articles: %d\n", len(articles))

			fmt.Printf("Last Fetched: %s\n", feed.LastFetched.Format("2006-01-02 15:04:05"))
			if feed.ETag != "" {
				fmt.Printf("ETag: %s\n", feed.ETag)
			}
			if feed.LastModified != "" {
				fmt.Printf("Last-Modified: %s\n", feed.LastModified)
			}
			fmt.Println()
		}
		return nil
	}); err != nil {
		exitWithError(err)
	}
}

func addFeed(_ *cobra.Command, args []string) {
	url := args[0]

	if err := withStoreAndConfig(func(store *storage.Store, cfg *config.Config) error {
		manager := feed.NewManager(store, cfg)
		loadLuaPlugins(manager)

		fmt.Printf("Adding feed: %s\n", url)
		feed, err := manager.AddFeed(url)
		if err != nil {
			return fmt.Errorf("failed to add feed: %w", err)
		}

		fmt.Printf("Successfully added feed: %s (%s)\n", feed.Title, feed.URL)
		fmt.Printf("Feed ID: %s\n", feed.ID)

		// Get article count
		articles, _ := store.GetArticles(feed.ID, 0)
		fmt.Printf("Articles fetched: %d\n", len(articles))

		return nil
	}); err != nil {
		exitWithError(err)
	}
}

func deleteFeed(_ *cobra.Command, args []string) {
	urlOrID := args[0]

	if err := withStore(func(store *storage.Store) error {
		// Find feed by URL or ID
		feeds, err := store.GetAllFeeds()
		if err != nil {
			return fmt.Errorf("failed to get feeds: %w", err)
		}

		var targetFeed *storage.Feed
		for _, feed := range feeds {
			if feed.ID == urlOrID || feed.URL == urlOrID {
				targetFeed = feed
				break
			}
		}

		if targetFeed == nil {
			return fmt.Errorf("feed not found: %s", urlOrID)
		}

		fmt.Printf("Deleting feed: %s (%s)\n", targetFeed.Title, targetFeed.URL)

		if err := store.DeleteFeed(targetFeed.ID); err != nil {
			return fmt.Errorf("failed to delete feed: %w", err)
		}

		fmt.Println("Feed deleted successfully.")
		return nil
	}); err != nil {
		exitWithError(err)
	}
}

func exportFeeds(_ *cobra.Command, args []string) {
	path := args[0]
	if err := withStore(func(store *storage.Store) error {
		feeds, err := store.GetAllFeeds()
		if err != nil {
			return fmt.Errorf("failed to get feeds: %w", err)
		}
		data, err := opml.Export(feeds, time.Now())
		if err != nil {
			return fmt.Errorf("failed to render OPML: %w", err)
		}
		if path == "-" {
			_, err = os.Stdout.Write(data)
			return err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("failed to write %s: %w", path, err)
		}
		fmt.Printf("Exported %d feed(s) to %s\n", len(feeds), path)
		return nil
	}); err != nil {
		exitWithError(err)
	}
}

func importFeeds(_ *cobra.Command, args []string) {
	path := args[0]
	if err := withStoreAndConfig(func(store *storage.Store, cfg *config.Config) error {
		var data []byte
		var err error
		if path == "-" {
			data, err = io.ReadAll(os.Stdin)
		} else {
			data, err = os.ReadFile(path)
		}
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		feeds, err := opml.Parse(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("failed to parse OPML: %w", err)
		}
		if len(feeds) == 0 {
			fmt.Println("No feeds found in OPML file.")
			return nil
		}

		manager := feed.NewManager(store, cfg)
		loadLuaPlugins(manager)

		// Snapshot existing URLs so already-subscribed feeds are skipped
		// rather than re-fetched.
		existing, _ := store.GetAllFeeds()
		have := make(map[string]bool, len(existing))
		for _, f := range existing {
			have[f.URL] = true
		}

		var added, skipped, failed int
		for _, f := range feeds {
			if have[f.URL] {
				skipped++
				continue
			}
			fmt.Printf("Adding %s\n", f.URL)
			if _, err := manager.AddFeed(f.URL); err != nil {
				fmt.Fprintf(os.Stderr, "  failed: %v\n", err)
				failed++
				continue
			}
			added++
		}
		fmt.Printf("Imported %d feed(s); %d skipped (already present); %d failed.\n", added, skipped, failed)
		return nil
	}); err != nil {
		exitWithError(err)
	}
}

func listPlugins(_ *cobra.Command, _ []string) {
	cfg, err := loadConfig()
	if err != nil {
		logger.Fatal("failed to load config", "err", err)
	}
	dir := pluginlua.DefaultPluginDir()
	if seedErr := pluginlua.EnsureDefaults(dir); seedErr != nil {
		logger.Warn("seeding default lua plugins", "dir", dir, "err", seedErr)
	}

	reg := plugins.NewRegistry(cfg.Feed.HTTPTimeout)
	bindings := pluginlua.Bindings{Logger: pluginLogger{}}
	if _, err := pluginlua.LoadAndRegister(reg, dir, bindings); err != nil {
		logger.Fatal("loading plugins", "dir", dir, "err", err)
	}

	loaded := reg.ListPlugins()
	fmt.Printf("Plugin directory: %s\n\n", dir)
	if len(loaded) == 0 {
		fmt.Println("No plugins loaded. Drop *.lua files into the directory and rerun.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPRIORITY\tPATH")
	for _, p := range loaded {
		path := ""
		if pp, ok := p.(interface{ Path() string }); ok {
			path = pp.Path()
		}
		fmt.Fprintf(w, "%s\t%d\t%s\n", p.Name(), p.Priority(), path)
	}
	_ = w.Flush()
}

func refreshFeeds(_ *cobra.Command, _ []string) {
	if err := withStoreAndConfig(func(store *storage.Store, cfg *config.Config) error {
		manager := feed.NewManager(store, cfg)
		loadLuaPlugins(manager)

		// Set force refresh if requested
		if forceRefresh {
			fmt.Println("Force refresh enabled - ignoring ETag/Last-Modified headers")
			manager.SetForceRefresh(true)
		}

		fmt.Println("Refreshing all feeds...")
		summary, err := manager.RefreshAllFeeds()
		if err != nil {
			return fmt.Errorf("failed to refresh feeds: %w", err)
		}

		fmt.Printf("Refreshed %d feed(s), added %d article(s).\n",
			summary.UpdatedFeeds, summary.AddedArticles)
		return nil
	}); err != nil {
		exitWithError(err)
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
