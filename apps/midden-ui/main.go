package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
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
	var opts Options
	flag.StringVar(&opts.Workspace, "workspace", "", "existing working directory")
	flag.StringVar(&opts.State, "state", "", "isolated UI/kernel state directory")
	flag.StringVar(&opts.Core, "core", "", "Midden core executable")
	flag.StringVar(&opts.Bundle, "bundle", "", "canonical bundle directory or extracted bundle archive root")
	listen := flag.String("listen", "127.0.0.1:18890", "loopback listen address")
	flag.Parse()
	if opts.Workspace == "" || opts.Core == "" || opts.Bundle == "" {
		return fmt.Errorf("--workspace, --core and --bundle are required")
	}
	var err error
	opts.Frontend, err = fs.Sub(webFiles, "web")
	if err != nil {
		return err
	}
	opts.Workspace, err = filepath.Abs(opts.Workspace)
	if err != nil {
		return err
	}
	if opts.State == "" {
		opts.State = filepath.Join(opts.Workspace, ".midden-ui")
	}
	opts.State, err = filepath.Abs(opts.State)
	if err != nil {
		return err
	}
	opts.Core, err = filepath.Abs(opts.Core)
	if err != nil {
		return err
	}
	opts.Bundle, err = filepath.Abs(opts.Bundle)
	if err != nil {
		return err
	}
	probe := exec.Command(opts.Core, "version")
	output, err := probe.Output()
	if err != nil {
		return fmt.Errorf("core version probe: %w", err)
	}
	opts.CoreVersion = strings.TrimSpace(string(output))
	if !strings.HasPrefix(opts.CoreVersion, "midden ") {
		return fmt.Errorf("not a Midden core executable")
	}
	opts.SourceEnv = map[string]string{}
	for _, key := range []string{"MIDDEN_CLAUDE_ROOT", "MIDDEN_COPILOT_ROOT", "MIDDEN_OPENCODE_DB"} {
		if value := os.Getenv(key); value != "" {
			opts.SourceEnv[key] = value
		}
	}
	host, _, err := net.SplitHostPort(*listen)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address must use a loopback IP")
	}
	if err = os.Setenv("FACET_STUDIO_HOME", filepath.Join(opts.State, "kernel")); err != nil {
		return err
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
	fmt.Printf("Midden UI: http://%s\nWorkspace: %s\nKernel: %s (candidate)\n", listener.Addr(), opts.Workspace, kernelVersion)
	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
