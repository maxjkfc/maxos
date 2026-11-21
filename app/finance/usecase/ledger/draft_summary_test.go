package ledger_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// The repo's tests show which transactions are drafts and what each of them carries,
// and the model's tests what is made of them. What these tests show is what the use
// case does with them: it reads the rows once, as of the day the clock says, and gives
// each amount its net. The repo here returns the rows the test sets.

// draftRow is a draft as the repo reads it: of the kind, on the day, with the postings
// that have an account.
func draftRow(kind model.Kind, d time.Time, postings ...model.DraftPosting) model.DraftTransaction {
	return model.DraftTransaction{ID: uuid.Must(uuid.NewV7()), Kind: kind, OccurredOn: d, Postings: postings}
}

// accountPosting is a posting of the account book to the account of the quantity of the
// instrument, as the repo reads it.
func accountPosting(a model.Account, in model.Instrument, quantity string) model.DraftPosting {
	return model.DraftPosting{AccountID: a.ID, AccountName: a.Name, Book: model.BookAccount, InstrumentID: in.ID, InstrumentCode: in.Code, Quantity: dec(quantity)}
}

// draftSummary reads the summary of the drafts, and fails the test when it cannot.
func (e *env) draftSummary() usecase.GetDraftSummaryOutput {
	e.t.Helper()
	out, err := e.svc.GetDraftSummary(context.Background(), usecase.GetDraftSummaryInput{})
	if err != nil {
		e.t.Fatalf("GetDraftSummary: %v", err)
	}
	return out
}

// amountsText writes amounts as "TWD out 120 in 0 net -120", one for each instrument,
// in the order they come.
func (e *env) amountsText(amounts []usecase.DraftAmount) string {
	names := e.names()
	parts := make([]string, len(amounts))
	for i, a := range amounts {
		parts[i] = fmt.Sprintf("%s out %s in %s net %s", names[a.InstrumentID], a.Outflow, a.Inflow, a.Net)
	}
	return strings.Join(parts, ", ")
}

// summaryLines writes the whole summary: a line for all the drafts, and then the lines
// of each month, the newest first, and under each month a line for each account in the
// order they come, the drafts that have no account as "(none)".
func (e *env) summaryLines(out usecase.GetDraftSummaryOutput) []string {
	names := e.names()
	lines := []string{fmt.Sprintf("all: %d, own transfers %d [%s]", out.Count, out.OwnTransfers, e.amountsText(out.Amounts))}
	for _, m := range out.Months {
		lines = append(lines, fmt.Sprintf("%s: %d, own transfers %d [%s]", m.Month.Format("2006-01"), m.Count, m.OwnTransfers, e.amountsText(m.Amounts)))
		for _, a := range m.Accounts {
			name := "(none)"
			if a.AccountID != nil {
				name = names[*a.AccountID]
			}
			lines = append(lines, fmt.Sprintf("  %s: %d [%s]", name, a.Count, e.amountsText(a.Amounts)))
		}
	}
	return lines
}

func TestGetDraftSummaryIsOfWhatTheRepoReadsAsOfTheDayOfTheClockAndGivesEachAmountItsNet(t *testing.T) {
	e := newEnv(t)
	// A lunch on the 14th, the day of the clock; a transfer from the bank to the cash with
	// a fee of 15 that the bank pays; a lunch that a party paid; and a purchase on the card
	// dated the 20th.
	e.txns.draftRows = []model.DraftTransaction{
		draftRow(model.KindExpense, on(2026, time.November, 14), accountPosting(e.cash, e.twd, "-120")),
		draftRow(model.KindTransfer, on(2026, time.November, 3),
			accountPosting(e.bank, e.twd, "-1000"), accountPosting(e.cash, e.twd, "1000"), accountPosting(e.bank, e.twd, "-15")),
		draftRow(model.KindExpense, on(2026, time.November, 8)),
		draftRow(model.KindExpense, on(2026, time.December, 20), accountPosting(e.card, e.twd, "-60")),
	}
	opened := e.tx.calls

	// The 20th of December has not come: its draft is counted, and changes no balance yet.
	// The transfer is in the accounts and not in the totals, but for its fee.
	wantLines(t, e.summaryLines(e.draftSummary()),
		"all: 4, own transfers 1 [TWD out 135 in 0 net -135]",
		"2026-12: 1, own transfers 0 []",
		"  card: 1 []",
		"2026-11: 3, own transfers 1 [TWD out 135 in 0 net -135]",
		"  bank: 1 [TWD out 1015 in 0 net -1015]",
		"  cash: 2 [TWD out 120 in 1000 net 880]",
		"  (none): 1 []",
	)
	// The use case opens no database transaction and reads once.
	if e.tx.calls != opened || e.txns.draftReads != 1 {
		t.Errorf("%d database transactions opened and the repo asked %d times, want 0 and 1", e.tx.calls-opened, e.txns.draftReads)
	}

	// Early in the morning of the 20th in the timezone of the application, which is still
	// the 19th in UTC: the day is the clock's own, and the draft changes the balance of
	// the card now.
	e.clock.now = time.Date(2026, time.December, 20, 0, 30, 0, 0, taipei)
	wantLines(t, e.summaryLines(e.draftSummary()),
		"all: 4, own transfers 1 [TWD out 195 in 0 net -195]",
		"2026-12: 1, own transfers 0 [TWD out 60 in 0 net -60]",
		"  card: 1 [TWD out 60 in 0 net -60]",
		"2026-11: 3, own transfers 1 [TWD out 135 in 0 net -135]",
		"  bank: 1 [TWD out 1015 in 0 net -1015]",
		"  cash: 2 [TWD out 120 in 1000 net 880]",
		"  (none): 1 []",
	)
}

func TestGetDraftSummaryOfALedgerWithNoDraftIsEmptyAndNotAnError(t *testing.T) {
	e := newEnv(t)

	got, err := e.svc.GetDraftSummary(context.Background(), usecase.GetDraftSummaryInput{})
	if err != nil || got.Count != 0 || got.OwnTransfers != 0 || len(got.Months) != 0 || len(got.Amounts) != 0 {
		t.Errorf("GetDraftSummary = %+v, %v, want a count of 0 and no month, with no error", got, err)
	}
}

func TestWhenTheRepoCannotReadTheDraftsTheErrorGoesOn(t *testing.T) {
	e := newEnv(t)
	boom := errors.New("the database is down")
	e.txns.readErr = boom

	_, err := e.svc.GetDraftSummary(context.Background(), usecase.GetDraftSummaryInput{})

	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "draft summary") {
		t.Errorf("err = %v, want what the repo returned, with what was being done", err)
	}
}

func TestAPeriodWhoseOriginIsNotAmongTheDraftsIsAnErrorAndNotAWrongSummary(t *testing.T) {
	e := newEnv(t)
	plan := uuid.Must(uuid.NewV7())
	period := draftRow(model.KindInstalment, on(2026, time.December, 5), accountPosting(e.card, e.twd, "-1000"))
	period.PlanID, period.PlanSeq = &plan, new(1)
	e.txns.draftRows = []model.DraftTransaction{period}

	_, err := e.svc.GetDraftSummary(context.Background(), usecase.GetDraftSummaryInput{})

	if !errors.Is(err, model.ErrMalformedPlan) || !strings.Contains(err.Error(), "draft summary") {
		t.Errorf("err = %v, want the model's error for a period with no origin, with what was being done", err)
	}
}
