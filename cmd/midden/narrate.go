package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mekjr1/midden/internal/adapter"
	"github.com/mekjr1/midden/internal/render"
)

// narrate prints what the adapter layer is doing on a single rewriting line,
// and returns a function that clears it.
//
// Progress goes to stderr so piping or redirecting stdout still yields clean
// output. When there is nothing slow to report — the common case, once the
// index is warm — nothing is ever printed.
func narrate() func() {
	var (
		mu      sync.Mutex
		width   int
		started = time.Now()
	)

	line := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		if s == "" {
			if width > 0 {
				fmt.Fprintf(os.Stderr, "\r%s\r", strings.Repeat(" ", width))
				width = 0
			}
			return
		}
		// Only speak up once the wait is long enough to be noticed. A fast
		// run should print nothing at all.
		if time.Since(started) < 400*time.Millisecond {
			return
		}
		msg := "  " + s
		pad := ""
		if n := width - len(msg); n > 0 {
			pad = strings.Repeat(" ", n)
		}
		fmt.Fprintf(os.Stderr, "\r%s%s", render.Dim(msg), pad)
		width = len(msg)
	}

	adapter.Progress = line
	return func() {
		line("")
		adapter.Progress = nil
	}
}
