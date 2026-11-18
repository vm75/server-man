package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"

	"server-man/internal/auth"
	"server-man/internal/config"
	"server-man/internal/core"
	"server-man/internal/mcp"
	"server-man/internal/webui"
)

func Handler(authentication *auth.Auth, registry *core.Registry) http.Handler {
	return handler(authentication, registry, webui.Handler())
}

// DevelopmentHandler serves the UI and HMR WebSocket through the configured
// application listener, so remote browsers use the same origin as the APIs.
func DevelopmentHandler(authentication *auth.Auth, registry *core.Registry) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(&url.URL{Scheme: "http", Host: "127.0.0.1:5173"})
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "Development UI is unavailable; start make dev", http.StatusServiceUnavailable)
		},
	}
	ui := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authentication.ValidOrigin(r) {
			respondError(w, &core.Error{Code: "forbidden_origin", Message: "Request origin or host is not allowed.", Status: 403})
			return
		}
		proxy.ServeHTTP(w, r)
	})
	return handler(authentication, registry, ui)
}

func handler(authentication *auth.Auth, registry *core.Registry, ui http.Handler) http.Handler {
	private := http.NewServeMux()
	private.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) { invoke(w, r, registry, "system_status", nil) })
	private.HandleFunc("GET /api/tools", func(w http.ResponseWriter, r *http.Request) {
		JSON(w, http.StatusOK, map[string]any{"tools": registry.Tools(), "modules": registry.Modules()})
	})
	private.HandleFunc("POST /api/tools/{name}", func(w http.ResponseWriter, r *http.Request) {
		var arguments json.RawMessage
		if !decode(w, r, &arguments, 1<<20) {
			return
		}
		invoke(w, r, registry, r.PathValue("name"), arguments)
	})
	private.HandleFunc("DELETE /api/session", func(w http.ResponseWriter, r *http.Request) {
		if err := authentication.Logout(w, r); err != nil {
			respondError(w, &core.Error{Code: "operation_failed", Message: "Could not revoke browser session.", Status: 500})
			return
		}
		JSON(w, http.StatusOK, map[string]string{"status": "signed out"})
	})
	private.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		respondError(w, &core.Error{Code: "not_found", Message: "endpoint not found", Status: 404})
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/auth/mode", func(w http.ResponseWriter, r *http.Request) {
		if !authentication.ValidOrigin(r) {
			respondError(w, &core.Error{Code: "forbidden_origin", Message: "Request origin or host is not allowed.", Status: 403})
			return
		}
		JSON(w, http.StatusOK, map[string]bool{"adminConfigured": authentication.AdminConfigured()})
	})
	mux.HandleFunc("POST /api/admin/setup", func(w http.ResponseWriter, r *http.Request) {
		if !authentication.ValidOrigin(r) {
			respondError(w, &core.Error{Code: "forbidden_origin", Message: "Request origin or host is not allowed.", Status: 403})
			return
		}
		var credentials struct {
			Token    string `json:"token"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decode(w, r, &credentials, 4096) {
			return
		}
		if !config.ValidAdminUsername(credentials.Username) || len(credentials.Password) < 12 || len(credentials.Password) > 256 {
			respondError(w, core.Invalid("Username must start with a letter (maximum 64 characters); password must be 12 to 256 characters."))
			return
		}
		if err := authentication.SetupAdmin(w, credentials.Token, credentials.Username, credentials.Password, r.RemoteAddr); err != nil {
			if errors.Is(err, auth.ErrInvalidCredentials) {
				respondError(w, &core.Error{Code: "unauthorized", Message: "Invalid access token.", Status: 401})
			} else if errors.Is(err, auth.ErrRateLimited) {
				respondError(w, &core.Error{Code: "rate_limited", Message: err.Error(), Status: 429})
			} else if errors.Is(err, config.ErrAdminConfigured) || errors.Is(err, config.ErrSetupConflict) {
				respondError(w, &core.Error{Code: "conflict", Message: "Administrator is already configured or configuration changed. Reload the page.", Status: 409})
			} else {
				respondError(w, &core.Error{Code: "operation_failed", Message: "Could not save administrator credentials. Check configuration permissions and retry.", Status: 500})
			}
			return
		}
		JSON(w, http.StatusOK, map[string]string{"status": "administrator configured"})
	})
	mux.HandleFunc("POST /api/session", func(w http.ResponseWriter, r *http.Request) {
		if !authentication.ValidOrigin(r) {
			respondError(w, &core.Error{Code: "forbidden_origin", Message: "Request origin or host is not allowed.", Status: 403})
			return
		}
		var credentials struct {
			Token    string `json:"token"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decode(w, r, &credentials, 4096) {
			return
		}
		var err error
		if authentication.AdminConfigured() {
			err = authentication.LoginPassword(w, credentials.Username, credentials.Password, r.RemoteAddr)
		} else {
			err = authentication.Login(w, credentials.Token, r.RemoteAddr)
		}
		if err != nil {
			if errors.Is(err, auth.ErrInvalidCredentials) {
				respondError(w, &core.Error{Code: "unauthorized", Message: "Invalid credentials.", Status: 401})
			} else if errors.Is(err, auth.ErrRateLimited) {
				respondError(w, &core.Error{Code: "rate_limited", Message: err.Error(), Status: 429})
			} else if errors.Is(err, auth.ErrTooManySessions) {
				respondError(w, &core.Error{Code: "too_many_sessions", Message: err.Error(), Status: 429})
			} else {
				respondError(w, &core.Error{Code: "operation_failed", Message: "Could not create browser session.", Status: 500})
			}
			return
		}
		JSON(w, http.StatusOK, map[string]string{"status": "signed in"})
	})
	mux.Handle("/api/", RequireAuth(authentication, private))
	mux.Handle("/mcp", RequireAuth(authentication, mcp.New(registry).HTTP()))
	mux.Handle("/", ui)
	return SecurityHeaders(mux)
}

func RequireAuth(authentication *auth.Auth, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authentication.ValidOrigin(r) {
			respondError(w, &core.Error{Code: "forbidden_origin", Message: "Request origin or host is not allowed.", Status: 403})
			return
		}
		if !authentication.Authorized(r) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			respondError(w, &core.Error{Code: "unauthorized", Message: "Sign in to your administrator account.", Status: 401})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func invoke(w http.ResponseWriter, r *http.Request, registry *core.Registry, name string, arguments json.RawMessage) {
	result, err := registry.Invoke(r.Context(), name, arguments, "http")
	if err != nil {
		respondError(w, err)
		return
	}
	JSON(w, http.StatusOK, result)
}

func decode(w http.ResponseWriter, r *http.Request, target any, limit int64) bool {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		respondError(w, &core.Error{Code: "unsupported_media_type", Message: "Content-Type must be application/json", Status: 415})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		respondError(w, core.Invalid("invalid or oversized JSON body"))
		return false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		respondError(w, core.Invalid("expected one JSON value"))
		return false
	}
	return true
}

func respondError(w http.ResponseWriter, err error) {
	var problem *core.Error
	if !errors.As(err, &problem) {
		problem = &core.Error{Code: "operation_failed", Message: err.Error(), Status: 500}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			problem.Code, problem.Status = "timeout", 504
		}
	}
	JSON(w, problem.Status, map[string]any{"error": problem})
}

func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
