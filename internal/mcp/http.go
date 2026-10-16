package mcp

import (
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// HTTP implements stateless Streamable HTTP with JSON responses. The caller must
// wrap it in the same authentication/origin middleware as the web API.
func (s *Server) HTTP() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "This endpoint uses JSON POST responses; there is no standalone SSE stream or HTTP session.", http.StatusMethodNotAllowed)
			return
		}
		if version := r.Header.Get("MCP-Protocol-Version"); version != "" && !SupportedVersion(version) {
			writeHTTP(w, http.StatusBadRequest, failure(nil, -32600, "Unsupported MCP-Protocol-Version"))
			return
		}
		mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mediaType != "application/json" {
			writeHTTP(w, http.StatusUnsupportedMediaType, failure(nil, -32600, "Content-Type must be application/json"))
			return
		}
		if !accepts(r.Header.Get("Accept"), "application/json") || !accepts(r.Header.Get("Accept"), "text/event-stream") {
			writeHTTP(w, http.StatusNotAcceptable, failure(nil, -32600, "Accept must include application/json and text/event-stream"))
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMessage))
		if err != nil {
			writeHTTP(w, http.StatusRequestEntityTooLarge, failure(nil, -32600, "Could not read message within size limit"))
			return
		}
		message, problem := parse(data)
		if problem != nil {
			writeHTTP(w, http.StatusBadRequest, problem)
			return
		}
		if len(message.ID) == 0 || message.Method == "" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeHTTP(w, http.StatusOK, s.dispatch(r.Context(), message))
	})
}

func accepts(header, want string) bool {
	for part := range strings.SplitSeq(header, ",") {
		kind, parameters, err := mime.ParseMediaType(strings.TrimSpace(part))
		quality := 1.0
		if text, ok := parameters["q"]; ok {
			quality, err = strconv.ParseFloat(text, 64)
		}
		if err == nil && quality > 0 && quality <= 1 && (kind == want || kind == "*/*") {
			return true
		}
	}
	return false
}

func writeHTTP(w http.ResponseWriter, status int, value *response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = encode(w, value)
}
