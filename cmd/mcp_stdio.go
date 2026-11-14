package cmd

import (
	"context"
	"log/slog"

	"github.com/spf13/cobra"
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/pkg/config"
	"github.com/maxjkfc/maxos/pkg/mcpserver"
)

func newMCPStdioCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp-stdio",
		Short: "Serve MCP over stdin and stdout for local clients (actor local, source mcp-stdio)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			cfg, err := config.Load(false)
			if err != nil {
				return err
			}
			return runUntilStopped(fx.New(
				fx.Supply(cfg),
				InfraModule(),
				InboxModule(),
				MCPModule(),
				fx.Invoke(serveStdio),
			))
		},
	}
}

// serveStdio ties one MCP stdio session to the app. The app ends when the
// client disconnects or fails the session; when the app is stopped instead, the
// session is cancelled and waited for.
func serveStdio(lc fx.Lifecycle, sd fx.Shutdowner, logger *slog.Logger, registry *mcpserver.Registry) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				defer close(done)
				err := registry.RunStdio(ctx, mcpserver.Caller{Source: "mcp-stdio", Actor: "local"})
				switch {
				case ctx.Err() != nil:
					// The app is stopping and cancelled the session: not a failure.
				case err != nil:
					logger.Error("mcp stdio session failed", "err", err)
					_ = sd.Shutdown(fx.ExitCode(1))
				default:
					_ = sd.Shutdown()
				}
			}()
			return nil
		},
		OnStop: func(stopCtx context.Context) error {
			cancel()
			select {
			case <-done:
				return nil
			case <-stopCtx.Done():
				return stopCtx.Err()
			}
		},
	})
}
