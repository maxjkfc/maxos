package moze

import (
	"context"
	"fmt"
	"time"

	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// occurredAt is the moment of a unit: its day and its time of day, in the timezone
// of the application. A unit with no time of day has none.
func (a *Importer) occurredAt(day time.Time, clock string) *time.Time {
	if clock == "" {
		return nil
	}
	parsed, err := time.Parse("15:04", clock)
	if err != nil {
		return nil
	}
	at := time.Date(day.Year(), day.Month(), day.Day(), parsed.Hour(), parsed.Minute(), 0, 0, a.loc)
	return &at
}

// details are the facts every transaction of a unit has: its key, its day, its text
// and the project, area and tags it belongs to.
func (a *Importer) details(r refs, u model.Unit, key, description string) finance.RecordDetails {
	return a.detailsOf(r, key, u.Date, u.Clock, description, u.Merchant, u.Area, u.Project, u.Tags)
}

// detailsOf makes the details of a transaction from what the plan says of it.
func (a *Importer) detailsOf(r refs, key string, day time.Time, clock, description, merchant, area, project string, tags []string) finance.RecordDetails {
	d := finance.RecordDetails{
		IdempotencyKey: key,
		OccurredOn:     day,
		OccurredAt:     a.occurredAt(day, clock),
		Description:    description,
		Merchant:       merchant,
	}
	if project != "" {
		id := r.projects[project]
		d.ProjectID = &id
	}
	if area != "" {
		id := r.areas[area]
		d.AreaID = &id
	}
	for _, t := range tags {
		d.TagIDs = append(d.TagIDs, r.tags[t])
	}
	return d
}

func (r refs) lines(income bool, lines []model.Line) []finance.CategoryLine {
	out := make([]finance.CategoryLine, len(lines))
	for i, l := range lines {
		cat := r.expense[l.Category]
		if income {
			cat = r.income[l.Category]
		}
		out[i] = finance.CategoryLine{CategoryID: cat, Amount: l.Amount, BaseAmount: l.Base, Discount: l.Discount, Memo: l.Memo}
	}
	return out
}

func (r refs) fees(fees []model.Fee) []finance.Fee {
	var out []finance.Fee
	for _, f := range fees {
		out = append(out, finance.Fee{
			AccountID:    r.accounts[f.Account],
			InstrumentID: r.instruments[f.Instrument],
			CategoryID:   r.expense[f.Category],
			Amount:       f.Amount,
			BaseAmount:   f.Base,
			Memo:         f.Memo,
		})
	}
	return out
}

// recorded is what recording a unit did: how many of its transactions were new and
// how many the ledger already had.
type recorded struct {
	New       int
	Duplicate int
}

func (c *recorded) count(duplicate bool) {
	if duplicate {
		c.Duplicate++
		return
	}
	c.New++
}

// record records a unit with the use case of its kind, as a draft.
func (a *Importer) record(ctx context.Context, r refs, u model.Unit) (recorded, error) {
	var c recorded
	acct := r.accounts[u.Main.Account]
	inst := r.instruments[u.Main.Instrument]
	switch u.Kind {
	case model.KindExpense, model.KindExpenseReward:
		out, err := a.ledger.RecordExpense.RecordExpense(ctx, finance.RecordExpenseInput{
			RecordDetails: a.details(r, u, u.Key(), u.Description),
			AccountID:     &acct,
			InstrumentID:  inst,
			Lines:         r.lines(false, u.Lines),
		})
		if err != nil {
			return c, err
		}
		c.count(out.Duplicate)
		if u.Reward != nil {
			reward, err := a.ledger.RecordReward.RecordReward(ctx, finance.RecordRewardInput{
				RecordDetails: a.details(r, u, u.Key()+model.RewardKeySuffix, u.Reward.Description),
				AccountID:     acct,
				InstrumentID:  inst,
				Amount:        u.Reward.Amount,
				BaseAmount:    u.Reward.Base,
			})
			if err != nil {
				return c, err
			}
			c.count(reward.Duplicate)
		}
	case model.KindIncome:
		out, err := a.ledger.RecordIncome.RecordIncome(ctx, finance.RecordIncomeInput{
			RecordDetails: a.details(r, u, u.Key(), u.Description),
			AccountID:     acct,
			InstrumentID:  inst,
			Lines:         r.lines(true, u.Lines),
			Fees:          r.fees(u.Fees),
		})
		if err != nil {
			return c, err
		}
		c.count(out.Duplicate)
	case model.KindReward:
		out, err := a.ledger.RecordReward.RecordReward(ctx, finance.RecordRewardInput{
			RecordDetails: a.details(r, u, u.Key(), u.Description),
			AccountID:     acct,
			InstrumentID:  inst,
			Amount:        u.Main.Amount,
			BaseAmount:    u.Base,
			Fees:          r.fees(u.Fees),
		})
		if err != nil {
			return c, err
		}
		c.count(out.Duplicate)
	case model.KindDealing:
		out, err := a.ledger.RecordDealing.RecordDealing(ctx, finance.RecordDealingInput{
			RecordDetails: a.details(r, u, u.Key(), u.Description),
			AccountID:     acct,
			PartyID:       r.parties[u.Party],
			InstrumentID:  inst,
			Amount:        u.Main.Amount,
			Direction:     u.Direction,
			Fees:          r.fees(u.Fees),
		})
		if err != nil {
			return c, err
		}
		c.count(out.Duplicate)
	case model.KindTransfer:
		out, err := a.ledger.RecordTransfer.RecordTransfer(ctx, finance.RecordTransferInput{
			RecordDetails: a.details(r, u, u.Key(), u.Description),
			FromAccountID: acct,
			ToAccountID:   r.accounts[u.Other.Account],
			InstrumentID:  inst,
			Amount:        u.Main.Amount,
			Fees:          r.fees(u.Fees),
		})
		if err != nil {
			return c, err
		}
		c.count(out.Duplicate)
	case model.KindExchange:
		out, err := a.ledger.RecordExchange.RecordExchange(ctx, finance.RecordExchangeInput{
			RecordDetails:    a.details(r, u, u.Key(), u.Description),
			FromAccountID:    acct,
			ToAccountID:      r.accounts[u.Other.Account],
			FromInstrumentID: inst,
			ToInstrumentID:   r.instruments[u.Other.Instrument],
			FromAmount:       u.Main.Amount,
			ToAmount:         u.Other.Amount,
			Fees:             r.fees(u.Fees),
		})
		if err != nil {
			return c, err
		}
		c.count(out.Duplicate)
	case model.KindTrade:
		out, err := a.ledger.RecordTrade.RecordTrade(ctx, finance.RecordTradeInput{
			RecordDetails: a.details(r, u, u.Key(), u.Description),
			Direction:     u.Direction,
			AccountID:     acct,
			InstrumentID:  inst,
			Quantity:      u.Main.Amount,
			CashAccountID: r.accounts[u.Other.Account],
			CurrencyID:    r.instruments[u.Other.Instrument],
			Amount:        u.Other.Amount,
			Fees:          r.fees(u.Fees),
		})
		if err != nil {
			return c, err
		}
		c.count(out.Duplicate)
	case model.KindOpening:
		out, err := a.ledger.SetOpeningBalance.SetOpeningBalance(ctx, finance.SetOpeningBalanceInput{
			RecordDetails: a.details(r, u, u.Key(), u.Description),
			AccountID:     acct,
			InstrumentID:  inst,
			Amount:        u.Main.Amount,
		})
		if err != nil {
			return c, err
		}
		c.count(out.Duplicate)
	case model.KindBillInstalment:
		schedule := finance.InstalmentSchedule{}
		for _, p := range u.Periods {
			schedule.Periods = append(schedule.Periods, finance.InstalmentPeriod{DueOn: p.DueOn, Principal: p.Principal, Interest: p.Interest})
		}
		if u.InterestCategory != "" {
			id := r.expense[u.InterestCategory]
			schedule.InterestCategoryID = &id
		}
		out, err := a.ledger.RecordBillInstalment.RecordBillInstalment(ctx, finance.RecordBillInstalmentInput{
			RecordDetails: a.details(r, u, u.Key(), u.Description),
			AccountID:     acct,
			InstrumentID:  inst,
			Principal:     u.Main.Amount,
			Schedule:      schedule,
			Fees:          r.fees(u.Fees),
		})
		if err != nil {
			return c, err
		}
		c.count(out.Duplicate)
	default:
		return c, fmt.Errorf("unit kind %q is not recorded by apply", u.Kind)
	}
	return c, nil
}
