package cmd

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/migrations"
	"github.com/maxjkfc/maxos/pkg/config"
	"github.com/maxjkfc/maxos/pkg/postgres"
)

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply pending database migrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(false)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return runOnce(ctx, fx.New(
				fx.Supply(cfg),
				InfraModule(),
				fx.Invoke(applyMigrations),
			))
		},
	}
}

// applyMigrations makes the migrations the work of the app: they run when it
// starts, after the database has been reached.
func applyMigrations(lc fx.Lifecycle, pool *pgxpool.Pool, logger *slog.Logger) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := postgres.Migrate(ctx, pool, migrations.FS); err != nil {
				return err
			}
			logger.Info("migrations applied")
			return nil
		},
	})
}
