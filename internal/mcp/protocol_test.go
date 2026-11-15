package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"server-man/internal/core"
)

func testServer(t *testing.T, allowWrites bool) (*Server, *core.Jobs) {
	t.Helper()
	jobs, err := core.NewJobs(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(jobs.Close)
	registry := core.NewRegistry(allowWrites, jobs)
	if err := registry.AddModule(core.Module{ID: "fixture", Available: true},
		core.Tool{Name: "read_fixture", Title: "Read fixture", ReadOnly: true, Run: func(context.Context, core.Input, io.Writer) (any, error) {
			return map[string]string{"value": "ok"}, nil
		}},
		core.Tool{Name: "change_fixture", Title: "Change fixture", Destructive: true, Run: func(ctx context.Context, input core.Input, out io.Writer) (any, error) {
			_, _ = io.WriteString(out, "fixture only")
			return "changed", nil
		}},
	); err != nil {
		t.Fatal(err)
	}
	return New(registry), jobs
}

func rpc(t *testing.T, server *Server, method, params string) *response {
	t.Helper()
	message, problem := parse([]byte(`{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":` + params + `}`))
	if problem != nil {
		t.Fatal(problem)
	}
	return server.dispatch(context.Background(), message)
}

func TestProtocolValidationNegotiationAndToolErrors(t *testing.T) {
	s, jobs := testServer(t, false)
	for _, body := range []string{`{`, `[]`, `null`, `{}`, `{"jsonrpc":"1.0","method":"ping","id":1}`, `{"jsonrpc":"2.0","method":"ping","id":null}`, `{"jsonrpc":"2.0","method":"ping","id":1.5}`, `{"jsonrpc":"2.0","method":"ping","id":true}`} {
		if _, problem := parse([]byte(body)); problem == nil {
			t.Errorf("accepted invalid message: %s", body)
		}
	}
	result := rpc(t, s, "initialize", `{"protocolVersion":"future","capabilities":{},"clientInfo":{"name":"test","version":"dev"}}`)
	if result.Error != nil || result.Result.(map[string]any)["protocolVersion"] != ProtocolVersion {
		t.Fatal("version negotiation failed")
	}
	if rpc(t, s, "initialize", `{}`).Error == nil {
		t.Fatal("incomplete initialize accepted")
	}
	if rpc(t, s, "tools/call", `{"name":"unknown"}`).Error == nil {
		t.Fatal("unknown tool accepted")
	}
	if result := rpc(t, s, "tools/call", `{"name":"change_fixture","arguments":{"confirm":true}}`); result.Error != nil || !result.Result.(map[string]any)["isError"].(bool) {
		t.Fatal("denied write did not produce a tool error")
	}
	if len(jobs.List()) != 0 {
		t.Fatal("denied MCP write launched a job")
	}
	result = rpc(t, s, "tools/list", `{}`)
	tools := result.Result.(map[string]any)["tools"].([]map[string]any)
	if !tools[0]["annotations"].(map[string]bool)["destructiveHint"] {
		t.Fatal("destructive annotation missing")
	}
}

func TestAllowedWriteUsesSharedJobsAndAcknowledgement(t *testing.T) {
	s, jobs := testServer(t, true)
	if result := rpc(t, s, "tools/call", `{"name":"change_fixture","arguments":{}}`); !result.Result.(map[string]any)["isError"].(bool) {
		t.Fatal("destructive write lacked acknowledgement")
	}
	if len(jobs.List()) != 0 {
		t.Fatal("invalid acknowledgement launched a job")
	}
	result := rpc(t, s, "tools/call", `{"name":"change_fixture","arguments":{"confirm":true}}`)
	if result.Error != nil || result.Result.(map[string]any)["isError"].(bool) {
		t.Fatal("valid fixture write failed")
	}
	var payload struct {
		Job core.Job `json:"job"`
	}
	if err := json.Unmarshal(result.Result.(map[string]any)["structuredContent"].(json.RawMessage), &payload); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	for {
		job, err := jobs.Get(payload.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != "running" {
			if job.State != "succeeded" || job.Output != "fixture only" {
				t.Fatalf("job=%+v", job)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("fixture job did not finish")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestStreamableHTTPMethodsVersionsAndNotifications(t *testing.T) {
	s, _ := testServer(t, false)
	handler := s.HTTP()
	for _, tc := range []struct {
		name, method, version, contentType, accept, body string
		status                                           int
	}{
		{"ping", "POST", ProtocolVersion, "application/json", "application/json, text/event-stream", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, 200},
		{"default legacy version", "POST", "", "application/json", "application/json, text/event-stream", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, 200},
		{"notification", "POST", ProtocolVersion, "application/json", "application/json, text/event-stream", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, 202},
		{"unsupported", "POST", "2099-01-01", "application/json", "application/json, text/event-stream", `{}`, 400},
		{"get", "GET", ProtocolVersion, "", "text/event-stream", ``, 405},
		{"delete", "DELETE", ProtocolVersion, "", "", ``, 405},
		{"media type", "POST", ProtocolVersion, "text/plain", "application/json, text/event-stream", `{}`, 415},
		{"accept", "POST", ProtocolVersion, "application/json", "application/json", `{}`, 406},
		{"invalid JSON", "POST", ProtocolVersion, "application/json", "application/json, text/event-stream", `{`, 400},
		{"oversize", "POST", ProtocolVersion, "application/json", "application/json, text/event-stream", strings.Repeat("x", maxMessage+1), 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://127.0.0.1:8080/mcp", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			r.Header.Set("Accept", tc.accept)
			r.Header.Set("MCP-Protocol-Version", tc.version)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			if tc.status == 202 && w.Body.Len() != 0 {
				t.Fatal("notification has a response body")
			}
		})
	}
}

func TestStdioLifecycleAndCancellation(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	registry := core.NewRegistry(false, nil)
	if err := registry.AddModule(core.Module{ID: "fixture", Available: true}, core.Tool{Name: "block", ReadOnly: true, Run: func(ctx context.Context, input core.Input, out io.Writer) (any, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}}); err != nil {
		t.Fatal(err)
	}
	in, writer := io.Pipe()
	out, serverOutput := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- New(registry).Stdio(ctx, in, serverOutput) }()
	defer writer.Close()
	defer out.Close()
	decoder := json.NewDecoder(out)
	send := func(message string) {
		t.Helper()
		if _, err := io.WriteString(writer, message+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	receive := func() response {
		t.Helper()
		var value response
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	send(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if receive().Error == nil {
		t.Fatal("operation before initialize accepted")
	}
	send(`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"dev"}}}`)
	if receive().Error != nil {
		t.Fatal("initialize failed")
	}
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"block"}}`)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`)
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("request was not cancelled")
	}
	send(`{"jsonrpc":"2.0","id":4,"method":"ping"}`)
	if message := receive(); !bytes.Equal(message.ID, []byte(`4`)) || message.Error != nil {
		t.Fatal("cancelled request emitted a reply or ping failed")
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stdio did not stop on cancellation")
	}
}
