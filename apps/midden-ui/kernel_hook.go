package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// runKernelHook is `midden-ui kernel-hook`, the kernel's process hook. It
// speaks the kernel's JSON-RPC over its standard input and output, one message
// per line: it answers hello itself and hands every other message to the
// midden-ui that started the kernel, through the relay address and secret in
// its environment. Notifications go in order; requests are answered as their
// answers come. It ends when the kernel closes its input.
func runKernelHook(input io.Reader, output io.Writer) error {
	relay, secret := os.Getenv(envKernelRelay), os.Getenv(envKernelRelaySecret)
	if relay == "" || secret == "" {
		return errors.New("kernel-hook runs only as the Midden kernel's process hook")
	}
	client := &http.Client{}
	post := func(ctx context.Context, line []byte) ([]byte, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, relay, bytes.NewReader(line))
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Bearer "+secret)
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err == nil && response.StatusCode != http.StatusOK {
			err = fmt.Errorf("midden-ui answered %s", response.Status)
		}
		return body, err
	}
	var writeMu sync.Mutex
	reply := func(id json.RawMessage, result []byte) {
		writeMu.Lock()
		defer writeMu.Unlock()
		fmt.Fprintf(output, "{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":%s}\n", id, result)
	}
	notes := make(chan []byte, 256)
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		for line := range notes {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if _, err := post(ctx, line); err != nil {
				fmt.Fprintln(os.Stderr, "midden kernel-hook:", err)
			}
			cancel()
		}
	}()
	var requests sync.WaitGroup
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(line, &message) != nil {
			continue
		}
		if len(message.ID) == 0 || string(message.ID) == "null" {
			select {
			case notes <- line:
			default: // midden-ui is behind; the kernel drops events the same way
			}
			continue
		}
		if message.Method == "hook.hello" {
			reply(message.ID, []byte("{}"))
			continue
		}
		requests.Add(1)
		go func() {
			defer requests.Done()
			body, err := post(context.Background(), line)
			if err != nil || !json.Valid(body) {
				body = []byte("{}")
				if message.Method == "hook.approve_tool" {
					body = []byte(`{"approved":false,"reason":"Midden is not reachable"}`)
				}
			}
			reply(message.ID, bytes.TrimSpace(body))
		}()
	}
	close(notes)
	finished := make(chan struct{})
	go func() { <-sent; requests.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
	}
	return scanner.Err()
}
