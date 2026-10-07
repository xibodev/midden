package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/xibodev/compa/pkg/config"
)

//go:embed web/*
var webFiles embed.FS

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "midden-ui:", err)
		os.Exit(1)
	}
}
func run() error {
	return runArgs(os.Args[1:], os.Stdout)
}

func runArgs(args []string, output io.Writer) error {
	var opts Options
	flags := flag.NewFlagSet("midden-ui", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&opts.Workspace, "workspace", "", "working directory (defaults to per-user Midden data)")
	flags.StringVar(&opts.State, "state", "", "isolated UI/kernel state directory")
	flags.StringVar(&opts.Core, "core", "", "Midden core executable (defaults to the sibling binary)")
	flags.StringVar(&opts.Bundle, "bundle", "", "skills directory (defaults to the sibling skills folder)")
	listen := flags.String("listen", "127.0.0.1:18890", "loopback listen address")
	noOpen := flags.Bool("no-open", false, "do not open the browser automatically")
	showVersion := flags.Bool("version", false, "print the UI release version without opening state")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if *showVersion {
		_, err := fmt.Fprintln(output, "midden-ui "+version)
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments; use --help for launch options")
	}
	var err error
	opts.Frontend, err = fs.Sub(webFiles, "web")
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	dataRoot := ""
	if opts.Workspace == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dataRoot, err = applicationDataRoot(runtime.GOOS, home, os.Getenv("LOCALAPPDATA"), os.Getenv("XDG_DATA_HOME"))
		if err != nil {
			return err
		}
	}
	var createWorkspace bool
	opts, createWorkspace, err = resolveLaunchPaths(opts, executable, dataRoot)
	if err != nil {
		return err
	}
	probe := exec.Command(opts.Core, "version")
	probeOutput, err := probe.Output()
	if err != nil {
		return fmt.Errorf("core version probe: %w", err)
	}
	opts.CoreVersion = strings.TrimSpace(string(probeOutput))
	if !strings.HasPrefix(opts.CoreVersion, "midden ") {
		return fmt.Errorf("not a Midden core executable")
	}
	opts.SourceEnv = map[string]string{}
	for _, key := range []string{"MIDDEN_CLAUDE_ROOT", "MIDDEN_COPILOT_ROOT", "MIDDEN_OPENCODE_DB"} {
		if value := os.Getenv(key); value != "" {
			opts.SourceEnv[key] = value
		}
	}
	if err := protectSourceStores(opts); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address must use a loopback IP")
	}
	if createWorkspace {
		if err := os.MkdirAll(opts.Workspace, 0700); err != nil {
			return err
		}
	}
	if err = os.Setenv(config.EnvHome, filepath.Join(opts.State, "kernel")); err != nil {
		return err
	}
	// The installer keeps the App's own Pandoc in app/tools; the agent's shell finds it first.
	if err = prependAppTools(executable); err != nil {
		return err
	}
	// The kernel's identity names the Compa release that powers Midden.
	if config.Version == "" || config.Version == "dev" {
		config.Version = kernelVersion()
	}
	app, err := NewApp(opts)
	if err != nil {
		return err
	}
	defer app.Close()
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: app, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	address := "http://" + listener.Addr().String()
	fmt.Fprintf(output, "Midden UI %s: %s\nWorkspace: %s\nState: %s\nPowered by Compa %s\n", version, address, opts.Workspace, opts.State, kernelVersion())
	if !*noOpen {
		go func() {
			if err := openBrowser(address); err != nil {
				fmt.Fprintln(os.Stderr, "midden-ui:", err)
			}
		}()
	}
	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
