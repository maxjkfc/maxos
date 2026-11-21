package cmd

import (
	"context"
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
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/config"
)

func newImportCmd() *cobra.Command {
	mozeCmd := &cobra.Command{
		Use:   "moze",
		Short: "Import a MOZE export: plan, apply, confirm month by month, verify",
	}
	mozeCmd.AddCommand(newImportPlanCmd(), newImportApplyCmd(), newImportConfirmCmd(), newImportVerifyCmd(), newImportStatusCmd())

	root := &cobra.Command{
		Use:   "import",
		Short: "Bring the data of another app into the ledger, as drafts",
	}
	root.AddCommand(mozeCmd)
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

// resolve fills in what the flags leave out: the mapping and the rates are in the
// import directory (MAXOS_MOZE_DIR), and so is the export, the newest MOZE_*.csv of it.
func (f importFiles) resolve() (usecase.Files, error) {
	files := usecase.Files{Export: f.export, Mapping: f.mapping, Rates: f.rates}
	dir, err := config.MozeDir()
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
			return files, fmt.Errorf("沒有給 --file，%s 裡也沒有 MOZE_*.csv（要換資料夾，設定 %s）", dir, config.MozeDirVariable)
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
		Short: "Work out what the export comes to in the ledger, add the entries the mapping lacks to it, and write the report; touches no database",
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

func newImportApplyCmd() *cobra.Command {
	var files importFiles
	c := &cobra.Command{
		Use:   "apply",
		Short: "Make the reference data and record every row that can be as a draft; running it again adds nothing, and what the plan says differently since is brought up to date",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := files.resolve()
			if err != nil {
				return err
			}
			return runImport(cmd, func(ctx context.Context, w io.Writer, uc importDeps) error {
				out, err := uc.Apply.Apply(ctx, usecase.ApplyInput{Files: resolved})
				if err != nil {
					return cli.Refusal(w, err)
				}
				if err := cli.RenderApply(w, out); err != nil {
					return err
				}
				switch {
				case len(out.Failed) > 0:
					return fmt.Errorf("有 %d 個單位被帳本拒收", len(out.Failed))
				case len(out.Stale) > 0:
					return fmt.Errorf("有 %d 筆已確認的交易與對照不同：作廢它們，再執行一次 apply", len(out.Stale))
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
		Short: "Confirm the drafts of one month in date order, reconcile its MOZE balance adjustments, and compare the balances",
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
					return cli.Refusal(w, err)
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
					return fmt.Errorf("有 %d 筆對帳被擋下：先確認或捨棄那些帳戶上其他的草稿，再執行一次 confirm", blocked)
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
		Short: "Compare the balances of every confirmed month with the export; fails on a difference nothing explains, and on a confirmed transaction that is not what the plan says",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := files.resolve()
			if err != nil {
				return err
			}
			return runImport(cmd, func(ctx context.Context, w io.Writer, uc importDeps) error {
				out, err := uc.Verify.Verify(ctx, usecase.VerifyInput{Files: resolved})
				if err != nil {
					return cli.Refusal(w, err)
				}
				if err := cli.RenderVerify(w, out); err != nil {
					return err
				}
				switch {
				case len(out.Stale) > 0:
					return fmt.Errorf("有 %d 筆已確認的交易與對照不同：作廢它們，執行 apply，再確認", len(out.Stale))
				case out.Unexplained > 0:
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
		Short: "Show, for each month, the drafts, the confirmed, the rows left out and the balance adjustments still to reconcile",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := files.resolve()
			if err != nil {
				return err
			}
			return runImport(cmd, func(ctx context.Context, w io.Writer, uc importDeps) error {
				out, err := uc.Status.Status(ctx, usecase.StatusInput{Files: resolved})
				if err != nil {
					return cli.Refusal(w, err)
				}
				return cli.RenderStatus(w, out)
			})
		},
	}
	files.bind(c)
	return c
}
