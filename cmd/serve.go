package cmd

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/pkg/cfaccess"
	"github.com/maxjkfc/maxos/pkg/config"
	"github.com/maxjkfc/maxos/pkg/mcpserver"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve MCP over HTTP at /mcp; every request must carry a valid Cloudflare Access token",
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
				AccessModule(),
				fx.Invoke(serveHTTP),
			))
		},
	}
}

// serveHTTP mounts the MCP endpoint behind the Access verifier and ties the
// HTTP server to the app: it listens when the app starts and drains in-flight
// requests when it stops.
func serveHTTP(
	lc fx.Lifecycle,
	sd fx.Shutdowner,
	cfg config.Config,
	logger *slog.Logger,
	registry *mcpserver.Registry,
	verifier *cfaccess.Verifier,
) {
	mux := http.NewServeMux()
	mux.Handle("/mcp", verifier.Middleware(registry.HTTPHandler(callerFromAccess)))
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
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
