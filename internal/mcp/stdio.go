package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

func encode(output io.Writer, value *response) error { return json.NewEncoder(output).Encode(value) }

// Stdio serves a single client. Input/output are closed on cancellation when
// supported, interrupting blocked pipe operations. Output is protocol JSON only.
func (s *Server) Stdio(ctx context.Context, input io.ReadCloser, output io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = input.Close()
			if closer, ok := output.(io.Closer); ok {
				_ = closer.Close()
			}
		case <-stop:
		}
	}()
	var writeMu sync.Mutex
	var writeErr error
	send := func(value *response) {
		writeMu.Lock()
		defer writeMu.Unlock()
		if writeErr == nil {
			writeErr = encode(output, value)
			if writeErr != nil {
				cancel()
			}
		}
	}
	var requestsMu sync.Mutex
	active := map[string]context.CancelFunc{}
	var wait sync.WaitGroup
	defer func() { cancel(); wait.Wait() }()
	phase := 0
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), maxMessage+1)
	for scanner.Scan() {
		if ctx.Err() != nil {
			break
		}
		message, problem := parse(scanner.Bytes())
		if problem != nil {
			send(problem)
			continue
		}
		if message.Method == "" {
			continue
		}
		if len(message.ID) == 0 {
			switch message.Method {
			case "notifications/initialized":
				if phase == 1 {
					phase = 2
				}
			case "notifications/cancelled":
				var params struct {
					RequestID json.RawMessage `json:"requestId"`
				}
				if json.Unmarshal(message.Params, &params) == nil {
					if key, ok := idKey(params.RequestID); ok {
						requestsMu.Lock()
						if cancel := active[key]; cancel != nil {
							cancel()
						}
						requestsMu.Unlock()
					}
				}
			}
			continue
		}
		if message.Method == "initialize" {
			if phase != 0 {
				send(failure(message.ID, -32600, "Already initialized"))
				continue
			}
			reply := s.dispatch(ctx, message)
			if reply.Error == nil {
				phase = 1
			}
			send(reply)
			continue
		}
		if phase != 2 && message.Method != "ping" {
			send(failure(message.ID, -32000, "Complete initialization first"))
			continue
		}
		if message.Method != "tools/call" {
			send(s.dispatch(ctx, message))
			continue
		}
		key, _ := idKey(message.ID)
		requestsMu.Lock()
		if active[key] != nil || len(active) >= 4 {
			requestsMu.Unlock()
			send(failure(message.ID, -32000, "Duplicate request ID or too many in-flight calls"))
			continue
		}
		requestContext, stopRequest := context.WithCancel(ctx)
		active[key] = stopRequest
		requestsMu.Unlock()
		wait.Go(func() {
			defer stopRequest()
			defer func() { requestsMu.Lock(); delete(active, key); requestsMu.Unlock() }()
			reply := s.dispatch(requestContext, message)
			if requestContext.Err() == nil {
				send(reply)
			}
		})
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("read MCP stdin: %w", err)
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	return writeErr
}
