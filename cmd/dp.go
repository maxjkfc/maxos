package cmd

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	inboxmcp "github.com/maxjkfc/maxos/app/inbox/adapter/in/mcp"
	inboxpg "github.com/maxjkfc/maxos/app/inbox/adapter/out/persistence/postgres"
	"github.com/maxjkfc/maxos/app/inbox/usecase/capture"
	"github.com/maxjkfc/maxos/app/inbox/usecase/listrecent"
	inboxrepo "github.com/maxjkfc/maxos/app/inbox/usecase/port/out/repo"
	"github.com/maxjkfc/maxos/pkg/cfaccess"
	"github.com/maxjkfc/maxos/pkg/config"
	"github.com/maxjkfc/maxos/pkg/log"
	"github.com/maxjkfc/maxos/pkg/mcpserver"
	"github.com/maxjkfc/maxos/pkg/postgres"
)

// registrarGroup is the Fx value group that collects the MCP tool registrar of
// every context. Adding a context's tools to the server means providing its
// registrar into this group.
const registrarGroup = `group:"mcp.registrars"`

// InfraModule provides what every command needs: the logger and the Postgres
// pool. The command supplies config.Config itself (fx.Supply), so that a
// configuration mistake reaches the user as a plain error.
func InfraModule() fx.Option {
	return fx.Options(
		// Failures come back to the command as errors; Fx's own event log would
		// only repeat them.
		fx.NopLogger,
		fx.Provide(log.New, newPool),
	)
}

// newPool builds the pool and ties it to the app: the database is checked when
// the app starts and the pool is closed when it stops.
func newPool(lc fx.Lifecycle, cfg config.Config) (*pgxpool.Pool, error) {
	pool, err := postgres.NewPool(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := pool.Ping(ctx); err != nil {
				return fmt.Errorf("ping database: %w", err)
			}
			return nil
		},
		OnStop: func(context.Context) error {
			pool.Close()
			return nil
		},
	})
	return pool, nil
}

// InboxModule wires the inbox context: its Postgres repo, its use cases and its
// MCP tools.
func InboxModule() fx.Option {
	return fx.Module("inbox",
		fx.Provide(
			fx.Annotate(inboxpg.NewInboxRepo, fx.As(new(inboxrepo.InboxRepo))),
			fx.Annotate(inboxmcp.NewRegistrar, fx.ResultTags(registrarGroup)),
		),
		capture.Module(),
		listrecent.Module(),
	)
}

// MCPModule provides the MCP server registry, fed by the registrar of every
// context that is part of the app.
func MCPModule() fx.Option {
	return fx.Provide(fx.Annotate(
		func(registrars []mcpserver.Registrar) *mcpserver.Registry {
			return mcpserver.NewRegistry("maxos", registrars)
		},
		fx.ParamTags(registrarGroup),
	))
}

// AccessModule provides the Cloudflare Access verifier that guards the HTTP
// endpoint. It needs the Access settings in config.Config.
func AccessModule() fx.Option {
	return fx.Provide(newAccessVerifier)
}

func newAccessVerifier(lc fx.Lifecycle, cfg config.Config, logger *slog.Logger) (*cfaccess.Verifier, error) {
	// The verifier keeps fetching signing keys for as long as it lives, so its
	// context must outlast construction: it ends when the app stops.
	ctx, cancel := context.WithCancel(context.Background())
	verifier, err := cfaccess.NewVerifier(ctx, cfg.AccessTeamDomain, cfg.AccessAUD, logger)
	if err != nil {
		cancel()
		return nil, err
	}
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			cancel()
			return nil
		},
	})
	return verifier, nil
}

// runOnce starts app, whose OnStart hooks do the command's work, and then stops
// it.
func runOnce(ctx context.Context, app *fx.App) error {
	if err := app.Start(ctx); err != nil {
		return err
	}
	return stopApp(app)
}

// runUntilStopped starts app and blocks until it is told to stop, by SIGINT or
// SIGTERM or by a component calling fx.Shutdowner, and then stops it.
func runUntilStopped(app *fx.App) error {
	startCtx, cancel := context.WithTimeout(context.Background(), app.StartTimeout())
	defer cancel()
	if err := app.Start(startCtx); err != nil {
		return err
	}

	sig := <-app.Wait()
	if err := stopApp(app); err != nil {
		return err
	}
	if sig.ExitCode != 0 {
		return fmt.Errorf("stopped because a component failed (exit code %d); see the log above", sig.ExitCode)
	}
	return nil
}

func stopApp(app *fx.App) error {
	ctx, cancel := context.WithTimeout(context.Background(), app.StopTimeout())
	defer cancel()
	return app.Stop(ctx)
}
