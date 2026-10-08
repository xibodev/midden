package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"
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
	if len(args) > 0 && args[0] == "kernel-hook" {
		if len(args) != 1 {
			return fmt.Errorf("kernel-hook takes no arguments")
		}
		return runKernelHook(os.Stdin, output)
	}
	var opts Options
	flags := flag.NewFlagSet("midden-ui", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&opts.Data, "data", "", "the App's data folder (defaults to the per-user Midden folder)")
	flags.StringVar(&opts.Core, "core", "", "Midden core executable (defaults to the sibling binary)")
	flags.StringVar(&opts.Skills, "skills", "", "skills folder (defaults to the sibling skills folder)")
	flags.StringVar(&opts.Kernel, "kernel", "", "compa-kernel executable (defaults to app/compa-kernel beside midden-ui)")
	listen := flags.String("listen", "127.0.0.1:18890", "loopback listen address")
	noOpen := flags.Bool("no-open", false, "do not open the browser automatically")
	showVersion := flags.Bool("version", false, "print the release version without opening data")
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
	if opts.Data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		if dataRoot, err = applicationDataRoot(runtime.GOOS, home, os.Getenv("LOCALAPPDATA"), os.Getenv("XDG_DATA_HOME")); err != nil {
			return err
		}
	}
	if opts, err = resolveLaunchPaths(opts, executable, dataRoot); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address must use a loopback IP")
	}
	release, err := lockData(opts.Data)
	if errors.Is(err, errAlreadyRunning) {
		address, ok := runningLaunchAddress(dataPaths(opts.Data))
		if !ok {
			return fmt.Errorf("%w, but it does not answer; stop it and start Midden again", err)
		}
		fmt.Fprintln(output, "Midden is already running: "+address)
		if !*noOpen {
			return openBrowser(address)
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer release()
	probe, err := exec.Command(opts.Core, "version").Output()
	if err != nil {
		return fmt.Errorf("core version probe: %w", err)
	}
	opts.CoreVersion = strings.TrimSpace(string(probe))
	if !strings.HasPrefix(opts.CoreVersion, "midden ") {
		return fmt.Errorf("not a Midden core executable")
	}
	opts.SourceEnv = map[string]string{}
	for _, key := range []string{"MIDDEN_CLAUDE_ROOT", "MIDDEN_COPILOT_ROOT", "MIDDEN_OPENCODE_DB"} {
		if value := os.Getenv(key); value != "" {
			opts.SourceEnv[key] = value
		}
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	app, err := NewApp(opts)
	if err != nil {
		listener.Close()
		return err
	}
	defer app.Close()
	if opts.Kernel != "" {
		if err := startKernel(app, executable); err != nil {
			app.notice = strings.TrimSpace(app.notice + " The assistant could not start: " + err.Error())
		}
	}
	address := app.launchAddress(listener.Addr().String())
	if err := writeLaunchAddress(app.paths, address); err != nil {
		listener.Close()
		return err
	}
	defer removeLaunchAddress(app.paths, address)
	server := &http.Server{Handler: app, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Quit in the page stops the App the same way.
	ctx, quit := context.WithCancel(ctx)
	defer quit()
	app.quit = quit
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	kernel := "not installed"
	if app.opts.KernelVersion != "" {
		kernel = app.opts.KernelVersion
	}
	fmt.Fprintf(output, "Midden App %s: %s\nYour files: %s\nPowered by Compa %s\n", version, address, app.paths.Files, kernel)
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

// startKernel starts the App's compa-kernel in the background.
func startKernel(app *App, executable string) error {
	kernelVersion, err := probeKernelVersion(app.opts.Kernel, app.paths.Kernel)
	if err != nil {
		return err
	}
	app.opts.KernelVersion = kernelVersion
	kernel, err := newKernelProcess(app.kernelSetup(executable))
	if err != nil {
		return err
	}
	app.attachKernel(kernel)
	kernel.Start()
	return nil
}
