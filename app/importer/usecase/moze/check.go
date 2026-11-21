package moze

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// check compares, for each account and instrument of the plan, what the ledger holds
// at the end of the month with what the export says. The ledger counts what is
// confirmed and dated up to that day, which is how the export's own balance is made
// up, so that what the import recorded and confirmed is what is compared; what the
// plan does not record is Unrecorded, and is what a difference can be explained by.
func (a *Importer) check(ctx context.Context, plan model.Plan, st *state, month string) (usecase.MonthCheck, error) {
	end, err := model.MonthEnd(month)
	if err != nil {
		return usecase.MonthCheck{}, fmt.Errorf("month %q: %w", month, err)
	}
	balances, err := a.ledger.GetBalances.GetBalances(ctx, finance.GetBalancesInput{AsOf: &end})
	if err != nil {
		return usecase.MonthCheck{}, fmt.Errorf("get the balances at %s: %w", end.Format("2006-01-02"), err)
	}

	mine := map[string]bool{}
	for _, name := range plan.Accounts() {
		mine[name] = true
	}
	ledger := model.Balances{}
	for _, b := range balances.Balances {
		name := st.accountNames[b.AccountID]
		if !mine[name] {
			continue
		}
		ledger[model.Pair{Account: name, Instrument: st.instrumentCodes[b.InstrumentID]}] = b.Quantity
	}

	expected := plan.ExpectedAt(end)
	unrecorded := plan.UnrecordedAt(end)
	all := model.Balances{}
	for _, set := range []model.Balances{ledger, expected, unrecorded} {
		for pair := range set {
			all[pair] = decimal.Zero
		}
	}
	check := usecase.MonthCheck{Month: month, End: end, Decimals: map[string]int{}}
	for _, pair := range all.Pairs() {
		line := usecase.BalanceLine{
			Account:    pair.Account,
			Instrument: pair.Instrument,
			Ledger:     ledger[pair],
			Expected:   expected[pair],
			Unrecorded: unrecorded[pair],
		}
		if line.Ledger.IsZero() && line.Expected.IsZero() && line.Unrecorded.IsZero() {
			continue
		}
		check.Lines = append(check.Lines, line)
		check.Decimals[pair.Instrument] = plan.Decimals(pair.Instrument)
	}
	return check, nil
}
