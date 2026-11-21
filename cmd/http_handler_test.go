package cmd_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/cmd"
	"github.com/maxjkfc/maxos/internal/webtest"
	"github.com/maxjkfc/maxos/pkg/cfaccess"
	"github.com/maxjkfc/maxos/pkg/mcpserver"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// httpHandler is what the server serves, made by the module that serve uses, with the
// registries of the MCP tools and of the pages empty: what is under test is what
// stands in front of them. The Access verifier is the stand-in that trusts a key of
// the test.
func httpHandler(t *testing.T, access *webtest.Access) http.Handler {
	t.Helper()
	var handler http.Handler
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			func() *cfaccess.Verifier { return access.Verifier() },
			func() *mcpserver.Registry { return mcpserver.NewRegistry("maxos", nil) },
			func() *webserver.Registry { return webserver.NewRegistry(slog.New(slog.DiscardHandler), nil) },
		),
		cmd.HTTPModule(),
		fx.Populate(&handler),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("assemble the handler: %v", err)
	}
	return handler
}

// securityHeaders are what every response carries.
var securityHeaders = map[string]string{
	"X-Content-Type-Options": "nosniff",
	"Referrer-Policy":        "no-referrer",
	"Cache-Control":          "no-store",
	"X-Frame-Options":        "DENY",
}

func wantSecurityHeaders(t *testing.T, what string, h http.Header) {
	t.Helper()
	for name, want := range securityHeaders {
		if got := h.Get(name); got != want {
			t.Errorf("%s: %s = %q, want %q", what, name, got, want)
		}
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("%s: Content-Security-Policy = %q, want default-src 'self'", what, csp)
	}
}

// TestNothingIsAnsweredBeforeTheVerifierHasLooked goes over the paths that the mux of
// net/http answers on its own, a redirect for a path that is not clean and a 404 for a
// path that nothing serves, and the paths that are served: with no token every one of
// them is refused and told nothing else, and every response has the security headers.
func TestNothingIsAnsweredBeforeTheVerifierHasLooked(t *testing.T) {
	access := webtest.NewAccess(t)
	handler := httpHandler(t, access)
	paths := []string{
		// The mux redirects these to the clean form of the path.
		"//app", "/app/./x", "/app/../mcp", "//mcp", "/app//transactions", "/mcp/../app/",
		// The mux has no route for these, and answers 404.
		"/", "/APP/", "/APP", "/mcp/", "/app%2fx", "/nowhere", "/favicon.ico", "/app.css",
		// And these are served.
		"/mcp", "/app", "/app/", "/app/transactions", "/app/static/app.css",
	}
	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodOptions} {
			name := method + " " + path
			t.Run(name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))

				if rec.Code != http.StatusUnauthorized {
					t.Errorf("%s without a token = %d, want 401: nothing but the refusal is said without one\n%s", name, rec.Code, rec.Body)
				}
				if loc := rec.Header().Get("Location"); loc != "" {
					t.Errorf("%s without a token redirects to %q: which paths exist is not for an unknown caller to learn", name, loc)
				}
				wantSecurityHeaders(t, name+" without a token", rec.Header())
			})
		}
	}
}

// TestWhatTheMuxAnswersIsAnsweredToWhoIsSignedInWithTheHeaders: a request with a token
// that is accepted gets what the mux says, and that has the headers as well.
func TestWhatTheMuxAnswersIsAnsweredToWhoIsSignedInWithTheHeaders(t *testing.T) {
	access := webtest.NewAccess(t)
	handler := httpHandler(t, access)
	tests := []struct {
		path       string
		wantStatus int
	}{
		{"//app", http.StatusTemporaryRedirect}, // the clean form of the path
		{"/app/./x", http.StatusTemporaryRedirect},
		{"/", http.StatusNotFound},
		{"/APP/", http.StatusNotFound},
		{"/mcp/", http.StatusNotFound},
		{"/app", http.StatusMovedPermanently}, // the pages send it on to /app/
		{"/app/nowhere", http.StatusNotFound},
		{"/app/static/app.css", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, access.Request(t, http.MethodGet, tt.path, "max@example.com"))

			if rec.Code != tt.wantStatus {
				t.Errorf("GET %s signed in = %d, want %d\n%s", tt.path, rec.Code, tt.wantStatus, rec.Body)
			}
			wantSecurityHeaders(t, "GET "+tt.path+" signed in", rec.Header())
		})
	}
}
