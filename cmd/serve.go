package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/pkg/cfaccess"
	"github.com/maxjkfc/maxos/pkg/config"
	"github.com/maxjkfc/maxos/pkg/mcpserver"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve MCP over HTTP at /mcp and the read-only pages under /app/; every request must carry a valid Cloudflare Access token",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			cfg, err := config.Load(true)
			if err != nil {
				return err
			}
			return runUntilStopped(fx.New(
				fx.Supply(cfg),
				InfraModule(),
				InboxModule(),
				CatalogModule(),
				FinanceModule(),
				MCPModule(),
				WebModule(),
				AccessModule(),
				HTTPModule(),
				fx.Invoke(serveHTTP),
			))
		},
	}
}

// HTTPModule provides what the HTTP server serves (see newHTTPHandler): the MCP
// endpoint and the pages, behind the Access verifier. It needs the registries of
// both (MCPModule, WebModule) and a verifier (AccessModule; a test supplies one that
// trusts a key of its own).
func HTTPModule() fx.Option {
	return fx.Provide(newHTTPHandler)
}

// serveHTTP ties the HTTP server to the app: it listens when the app starts and
// drains in-flight requests when it stops.
func serveHTTP(
	lc fx.Lifecycle,
	sd fx.Shutdowner,
	cfg config.Config,
	logger *slog.Logger,
	handler http.Handler,
) {
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			// Listening here, not in the goroutine, makes a busy port fail the start.
			ln, err := net.Listen("tcp", srv.Addr)
			if err != nil {
				return err
			}
			logger.Info("listening", "addr", srv.Addr)
			go func() {
				if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
					logger.Error("http server failed", "err", err)
					_ = sd.Shutdown(fx.ExitCode(1))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info("shutting down")
			return srv.Shutdown(ctx)
		},
	})
}

// newHTTPHandler is what the server serves: the MCP endpoint at /mcp and the pages
// under /app/, with the Access verifier and the security headers in front of the
// whole of it. What the mux answers on its own is behind them too: the redirect to
// the clean form of a path (//app, /app/./x, /app/../mcp) and the 404 of a path that
// nothing serves (/, /APP/, /mcp/) are what an unauthenticated request must not be
// told, and every response, a 401 as much as a page, carries the headers. The
// pages are also built with the verifier as their guard, which they cannot be built
// without, so that they do not rely on being mounted behind it: for a page that is a
// second check of a token that the first has let through. The pages are mounted at
// /app too, which they send on to /app/.
func newHTTPHandler(registry *mcpserver.Registry, pages *webserver.Registry, verifier *cfaccess.Verifier) (http.Handler, error) {
	web, err := pages.Pages(userFromAccess, verifier.Middleware)
	if err != nil {
		return nil, fmt.Errorf("build the pages: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", registry.HTTPHandler(callerFromAccess))
	mux.Handle(webserver.Prefix+"/", web)
	mux.Handle(webserver.Prefix, web)
	return webserver.Secure(verifier.Middleware(mux)), nil
}

// callerFromAccess attributes a request to the identity the Access middleware
// verified. The middleware guarantees one; a missing identity is a wiring bug,
// so the request is refused rather than served under an anonymous actor.
func callerFromAccess(r *http.Request) (mcpserver.Caller, bool) {
	id, ok := cfaccess.IdentityFrom(r.Context())
	if !ok {
		return mcpserver.Caller{}, false
	}
	return mcpserver.Caller{Source: "mcp-http", Actor: id.Actor()}, true
}

// userFromAccess names the user of a page by the identity the Access middleware
// verified: the email of the token, or the identity of a service token. As for the
// MCP endpoint, a missing identity is a wiring bug and the request is refused.
func userFromAccess(r *http.Request) (webserver.User, bool) {
	id, ok := cfaccess.IdentityFrom(r.Context())
	if !ok {
		return webserver.User{}, false
	}
	return webserver.User{Name: id.Actor()}, true
}
