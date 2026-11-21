package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/app/importer/adapter/in/cli"
	"github.com/maxjkfc/maxos/app/importer/adapter/out/file"
	"github.com/maxjkfc/maxos/app/importer/usecase/moze"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/mapping"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/rates"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/source"
	"github.com/maxjkfc/maxos/pkg/config"
)

// ImporterModule wires the importer context: the adapters that read the MOZE export,
// the mapping and the rates, and its use cases. The use cases that work on the
// ledger need the finance and catalog modules, the pool, and the *time.Location of
// the application; planning needs none of them, so `import moze plan` runs with no
// database at all.
func ImporterModule() fx.Option {
	return fx.Module("importer",
		fx.Provide(
			fx.Annotate(file.NewMozeCSV, fx.As(new(source.Source))),
			fx.Annotate(file.NewMappingYAML, fx.As(new(mapping.Store))),
			fx.Annotate(file.NewRatesCSV, fx.As(new(rates.Store))),
		),
		moze.Module(),
	)
}

// importDirEnv names the directory that holds the files of an import. It defaults
// to ~/.config/maxos/moze; the export, the mapping and the rates are looked for in it
// unless a flag says where they are.
const importDirEnv = "MAXOS_MOZE_DIR"

func newImportCmd() *cobra.Command {
	moze := &cobra.Command{
		Use:   "moze",
		Short: "Import a MOZE export: plan, apply, confirm month by month, verify",
	}
	moze.AddCommand(newImportPlanCmd(), newImportApplyCmd(), newImportConfirmCmd(), newImportVerifyCmd(), newImportStatusCmd())

	root := &cobra.Command{
		Use:   "import",
		Short: "Bring the data of another app into the ledger, as drafts",
	}
	root.AddCommand(moze)
	return root
}

// importFiles are the flags that say where the files of an import are.
type importFiles struct {
	export, mapping, rates string
}

func (f *importFiles) bind(c *cobra.Command) {
	c.Flags().StringVar(&f.export, "file", "", "the MOZE CSV export (default: the newest MOZE_*.csv of the import directory)")
	c.Flags().StringVar(&f.mapping, "mapping", "", "the mapping file (default: mapping.yaml of the import directory)")
	c.Flags().StringVar(&f.rates, "rates", "", "the rates file (default: rates.csv of the import directory)")
}

// importDir is where the files of an import are by default.
func importDir() (string, error) {
	if dir := os.Getenv(importDirEnv); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("找不到匯入資料夾，請設定 %s：%w", importDirEnv, err)
	}
	return filepath.Join(home, ".config", "maxos", "moze"), nil
}

// resolve fills in what the flags leave out: the mapping and the rates are in the
// import directory, and so is the export, the newest MOZE_*.csv of it.
func (f importFiles) resolve() (usecase.Files, error) {
	files := usecase.Files{Export: f.export, Mapping: f.mapping, Rates: f.rates}
	dir, err := importDir()
	if err != nil {
		return files, err
	}
	if files.Mapping == "" {
		files.Mapping = filepath.Join(dir, "mapping.yaml")
	}
	if files.Rates == "" {
		files.Rates = filepath.Join(dir, "rates.csv")
	}
	if files.Export == "" {
		exports, err := filepath.Glob(filepath.Join(dir, "MOZE_*.csv"))
		if err != nil || len(exports) == 0 {
			return files, fmt.Errorf("沒有給 --file，%s 裡也沒有 MOZE_*.csv（要換資料夾，設定 %s）", dir, importDirEnv)
		}
		sort.Strings(exports)
		files.Export = exports[len(exports)-1]
	}
	return files, nil
}

func newImportPlanCmd() *cobra.Command {
	var files importFiles
	var report string
	c := &cobra.Command{
		Use:   "plan",
		Short: "Work out what the export comes to in the ledger; writes no data, adds guesses to the mapping",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := files.resolve()
			if err != nil {
				return err
			}
			var plan usecase.Plan
			if err := fx.New(fx.NopLogger, ImporterModule(), fx.Populate(&plan)).Err(); err != nil {
				return err
			}
			out, err := plan.Plan(cmd.Context(), usecase.PlanInput{Files: resolved})
			if err != nil {
				return err
			}
			if report == "" {
				return cli.RenderPlan(cmd.OutOrStdout(), out)
			}
			f, err := os.OpenFile(report, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
			if err != nil {
				return fmt.Errorf("寫報告失敗：%w", err)
			}
			if err := cli.RenderPlan(f, out); err != nil {
				_ = f.Close()
				return fmt.Errorf("寫報告失敗：%w", err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("寫報告失敗：%w", err)
			}
			return cli.RenderPlanSummary(cmd.OutOrStdout(), out, report)
		},
	}
	files.bind(c)
	c.Flags().StringVar(&report, "out", "", "write the report to this file and print only its summary")
	return c
}

// importDeps are the use cases of the commands that work on the ledger.
type importDeps struct {
	fx.In

	Apply   usecase.Apply
	Confirm usecase.Confirm
	Verify  usecase.Verify
	Status  usecase.Status
}

// runImport runs one of the commands that work on the ledger: it puts the app together
// with the database, and runs work once the database has been reached. The error the
// work returns is the error of the command, as it is, not one that Fx wraps.
func runImport(cmd *cobra.Command, work func(ctx context.Context, w io.Writer, uc importDeps) error) error {
	cfg, err := config.Load(false)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var workErr error
	app := fx.New(
		fx.Supply(cfg),
		InfraModule(),
		CatalogModule(),
		FinanceModule(),
		ImporterModule(),
		fx.Provide(func(cfg config.Config) *time.Location { return cfg.Timezone }),
		fx.Invoke(func(lc fx.Lifecycle, uc importDeps) {
			lc.Append(fx.Hook{OnStart: func(ctx context.Context) error {
				workErr = work(ctx, cmd.OutOrStdout(), uc)
				return nil
			}})
		}),
	)
	if err := runOnce(ctx, app); err != nil {
		return err
	}
	return workErr
}

// refused prints what a refusal of the use cases says in plain terms, and returns the
// error, for the command to fail with, in words the user reads: what the use cases
// refuse with is English, as every error of the ledger is.
func refused(w io.Writer, err error) error {
	if _, werr := cli.Problem(w, err); werr != nil {
		return werr
	}
	var blocked *usecase.BlockedError
	var order *usecase.MonthOrderError
	switch {
	case errors.As(err, &blocked):
		return errors.New("對照檔還有要處理的項目，什麼都沒有寫入")
	case errors.As(err, &order):
		return fmt.Errorf("%s 之前的月份還沒確認完，什麼都沒有確認", order.Month)
	case errors.Is(err, usecase.ErrUnknownMonth):
		return errors.New("匯出裡沒有這個月份的資料")
	case errors.Is(err, usecase.ErrNotApplied):
		return errors.New("這個月有列還沒有 apply 進帳本，請先執行 apply")
	}
	return err
}

func newImportApplyCmd() *cobra.Command {
	var files importFiles
	c := &cobra.Command{
		Use:   "apply",
		Short: "Make the reference data and record every row that can be as a draft; running it again adds nothing",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := files.resolve()
			if err != nil {
				return err
			}
			return runImport(cmd, func(ctx context.Context, w io.Writer, uc importDeps) error {
				out, err := uc.Apply.Apply(ctx, usecase.ApplyInput{Files: resolved})
				if err != nil {
					return refused(w, err)
				}
				if err := cli.RenderApply(w, out); err != nil {
					return err
				}
				if len(out.Failed) > 0 {
					return fmt.Errorf("有 %d 個單位被帳本拒收", len(out.Failed))
				}
				return nil
			})
		},
	}
	files.bind(c)
	return c
}

func newImportConfirmCmd() *cobra.Command {
	var files importFiles
	var month string
	c := &cobra.Command{
		Use:   "confirm",
		Short: "Confirm the drafts of one month in date order, apply its balance adjustments, and compare the balances",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := time.Parse("2006-01", month); err != nil {
				return fmt.Errorf("--month 要是一個月份，例如 2025-03：%w", err)
			}
			resolved, err := files.resolve()
			if err != nil {
				return err
			}
			return runImport(cmd, func(ctx context.Context, w io.Writer, uc importDeps) error {
				out, err := uc.Confirm.Confirm(ctx, usecase.ConfirmInput{Files: resolved, Month: month})
				if err != nil {
					return refused(w, err)
				}
				if err := cli.RenderConfirm(w, out); err != nil {
					return err
				}
				blocked := 0
				for _, a := range out.Adjustments {
					if a.State == usecase.AdjustmentBlocked {
						blocked++
					}
				}
				switch {
				case blocked > 0:
					return fmt.Errorf("有 %d 筆餘額調整被擋下：先確認或捨棄那些帳戶上其他的草稿，再執行一次 confirm", blocked)
				case out.Check.Unexplained() > 0:
					return fmt.Errorf("有 %d 個帳戶與標的的餘額與 CSV 不符，而且沒有東西能說明", out.Check.Unexplained())
				}
				return nil
			})
		},
	}
	files.bind(c)
	c.Flags().StringVar(&month, "month", "", "the month to confirm, like 2025-03")
	_ = c.MarkFlagRequired("month")
	return c
}

func newImportVerifyCmd() *cobra.Command {
	var files importFiles
	c := &cobra.Command{
		Use:   "verify",
		Short: "Compare the balances of every confirmed month with the export; fails on a difference nothing explains",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := files.resolve()
			if err != nil {
				return err
			}
			return runImport(cmd, func(ctx context.Context, w io.Writer, uc importDeps) error {
				out, err := uc.Verify.Verify(ctx, usecase.VerifyInput{Files: resolved})
				if err != nil {
					return refused(w, err)
				}
				if err := cli.RenderVerify(w, out); err != nil {
					return err
				}
				if out.Unexplained > 0 {
					return fmt.Errorf("有 %d 個帳戶與標的的餘額與 CSV 不符，而且沒有東西能說明", out.Unexplained)
				}
				return nil
			})
		},
	}
	files.bind(c)
	return c
}

func newImportStatusCmd() *cobra.Command {
	var files importFiles
	c := &cobra.Command{
		Use:   "status",
		Short: "Show, for each month, the drafts, the confirmed, the rows left out and the adjustments still to make",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := files.resolve()
			if err != nil {
				return err
			}
			return runImport(cmd, func(ctx context.Context, w io.Writer, uc importDeps) error {
				out, err := uc.Status.Status(ctx, usecase.StatusInput{Files: resolved})
				if err != nil {
					return refused(w, err)
				}
				return cli.RenderStatus(w, out)
			})
		},
	}
	files.bind(c)
	return c
}
