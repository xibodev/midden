package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/mekjr1/midden/internal/index"
	"github.com/mekjr1/midden/internal/render"
	"github.com/mekjr1/midden/internal/web"
)

// cmdUI serves the embedded web interface.
//
// The UI ships inside the binary via embed.FS and binds to loopback only.
// Technically client-server; operationally a double-click. A browser tab
// cannot read ~/.claude/projects or open a 223 MB SQLite file, which is why a
// purely static page was never possible.
func cmdUI(args []string) error {
	fs := flag.NewFlagSet("ui", flag.ExitOnError)
	port := fs.Int("port", 7777, "port to bind on loopback")
	noOpen := fs.Bool("no-open", false, "do not open a browser")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return err
	}

	db, err := index.Open()
	if err != nil {
		return err
	}
	defer db.Close()

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("bind %s: %w (try --port)", addr, err)
	}

	srv := &http.Server{
		Handler:           web.NewServer(db).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	url := "http://" + addr
	fmt.Printf("\n  %s  %s\n", render.Bold("midden ui"), url)
	fmt.Printf("  %s\n\n", render.Dim("loopback only · Ctrl-C to stop"))

	if !*noOpen {
		openBrowser(url)
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case <-stop:
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		fmt.Println("  stopped")
		return nil
	}
}

// openBrowser is best-effort: failing to open a browser must never stop the
// server, since the URL is printed anyway.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err == nil {
		go cmd.Wait()
	}
}
