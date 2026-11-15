// This verification helper passes HTTP requests through the production handler
// without a listening socket. It always disables management writes.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"server-man/internal/app"
	"server-man/internal/auth"
	"server-man/internal/config"
	"server-man/internal/core"
	"server-man/internal/httpapi"
	"server-man/internal/modules/monitor"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 || !filepath.IsAbs(os.Args[1]) {
		return fmt.Errorf("provide an absolute temporary state directory")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := os.Args[1]
	authentication, err := auth.Load(filepath.Join(root, "token"), "")
	if err != nil {
		return err
	}
	jobs, err := core.NewJobs(ctx, root)
	if err != nil {
		return err
	}
	defer jobs.Close()
	monitoring := monitor.New(ctx, time.Second)
	defer monitoring.Close()
	registry, err := app.Registry(config.Config{StateDir: root}, jobs, monitoring, core.Runner{})
	if err != nil {
		return err
	}
	handler := httpapi.Handler(authentication, registry)
	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(map[string]bool{"ready": true}); err != nil {
		return err
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		var message struct {
			ID      int               `json:"id"`
			Method  string            `json:"method"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Body    string            `json:"body"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			return err
		}
		request := httptest.NewRequest(message.Method, message.URL, strings.NewReader(message.Body))
		for name, value := range message.Headers {
			request.Header.Set(name, value)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if err := encoder.Encode(map[string]any{"id": message.ID, "status": response.Code, "headers": response.Header(), "body": response.Body.Bytes()}); err != nil {
			return err
		}
	}
	return scanner.Err()
}
