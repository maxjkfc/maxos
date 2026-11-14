package cmd

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	catalogpg "github.com/maxjkfc/maxos/app/catalog/adapter/out/persistence/postgres"
	catalogarea "github.com/maxjkfc/maxos/app/catalog/usecase/area"
	catalogparty "github.com/maxjkfc/maxos/app/catalog/usecase/party"
	catalogrepo "github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
	catalogproject "github.com/maxjkfc/maxos/app/catalog/usecase/project"
	catalogreferences "github.com/maxjkfc/maxos/app/catalog/usecase/references"
	catalogtag "github.com/maxjkfc/maxos/app/catalog/usecase/tag"
	financepg "github.com/maxjkfc/maxos/app/finance/adapter/out/persistence/postgres"
	financeaccount "github.com/maxjkfc/maxos/app/finance/usecase/account"
	financecategory "github.com/maxjkfc/maxos/app/finance/usecase/category"
	financeinstitution "github.com/maxjkfc/maxos/app/finance/usecase/institution"
	financeinstrument "github.com/maxjkfc/maxos/app/finance/usecase/instrument"
	financeledger "github.com/maxjkfc/maxos/app/finance/usecase/ledger"
	financeclock "github.com/maxjkfc/maxos/app/finance/usecase/port/out/clock"
	financerepo "github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
	financetx "github.com/maxjkfc/maxos/app/finance/usecase/port/out/tx"
	inboxmcp "github.com/maxjkfc/maxos/app/inbox/adapter/in/mcp"
	inboxpg "github.com/maxjkfc/maxos/app/inbox/adapter/out/persistence/postgres"
	"github.com/maxjkfc/maxos/app/inbox/usecase/capture"
	"github.com/maxjkfc/maxos/app/inbox/usecase/listrecent"
	inboxrepo "github.com/maxjkfc/maxos/app/inbox/usecase/port/out/repo"
	"github.com/maxjkfc/maxos/pkg/cfaccess"
	"github.com/maxjkfc/maxos/pkg/clock"
	"github.com/maxjkfc/maxos/pkg/config"
	"github.com/maxjkfc/maxos/pkg/log"
	"github.com/maxjkfc/maxos/pkg/mcpserver"
	"github.com/maxjkfc/maxos/pkg/postgres"
)

// registrarGroup is the Fx value group that collects the MCP tool registrar of
// every context. Adding a context's tools to the server means providing its
// registrar into this group.
const registrarGroup = `group:"mcp.registrars"`

// InfraModule provides what every command needs: the logger, the Postgres pool
// and the clock, which tells the time in the timezone of the config. The command
// supplies config.Config itself (fx.Supply), so that a configuration mistake
// reaches the user as a plain error.
func InfraModule() fx.Option {
	return fx.Options(
		// Failures come back to the command as errors; Fx's own event log would
		// only repeat them.
		fx.NopLogger,
		fx.Provide(log.New, newPool, newClock),
	)
}

// newClock is the clock the finance use cases tell today by: the wall clock in
// the timezone the application is configured with (MAXOS_TIMEZONE).
func newClock(cfg config.Config) financeclock.Clock {
	return clock.New(cfg.Timezone)
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

// CatalogModule wires the catalog context: its Postgres repos and its use cases.
// The finance ledger calls VerifyReferences, ResolveProjectDefaults,
// ResolveProjectArea and ListParties; the MCP tools will use the rest.
func CatalogModule() fx.Option {
	return fx.Module("catalog",
		fx.Provide(
			fx.Annotate(catalogpg.NewAreaRepo, fx.As(new(catalogrepo.AreaRepo))),
			fx.Annotate(catalogpg.NewProjectRepo, fx.As(new(catalogrepo.ProjectRepo))),
			fx.Annotate(catalogpg.NewPartyRepo, fx.As(new(catalogrepo.PartyRepo))),
			fx.Annotate(catalogpg.NewTagRepo, fx.As(new(catalogrepo.TagRepo))),
		),
		catalogarea.Module(),
		catalogproject.Module(),
		catalogparty.Module(),
		catalogtag.Module(),
		catalogreferences.Module(),
	)
}

// FinanceModule wires the finance context: its Postgres repos and its use cases.
// That is the reference data (instruments, institutions, accounts, categories) and
// the ledger. The ledger opens its database transactions through the same pool as
// the repos, and calls the catalog, so the app must include CatalogModule too. It
// tells today by a financeclock.Clock, which InfraModule provides and a test
// supplies.
func FinanceModule() fx.Option {
	return fx.Module("finance",
		fx.Provide(
			fx.Annotate(financepg.NewInstrumentRepo, fx.As(new(financerepo.InstrumentRepo))),
			fx.Annotate(financepg.NewInstitutionRepo, fx.As(new(financerepo.InstitutionRepo))),
			fx.Annotate(financepg.NewAccountRepo, fx.As(new(financerepo.AccountRepo))),
			fx.Annotate(financepg.NewCategoryRepo, fx.As(new(financerepo.CategoryRepo))),
			fx.Annotate(financepg.NewTransactionRepo, fx.As(new(financerepo.TransactionRepo))),
			fx.Annotate(postgres.NewDB, fx.As(new(financetx.Transactor))),
		),
		financeinstrument.Module(),
		financeinstitution.Module(),
		financeaccount.Module(),
		financecategory.Module(),
		financeledger.Module(),
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

func newAccessVerifier(cfg config.Config, logger *slog.Logger) (*cfaccess.Verifier, error) {
	return cfaccess.NewVerifier(cfg.AccessTeamDomain, cfg.AccessAUD, logger)
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
