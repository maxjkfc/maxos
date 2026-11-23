package ledger_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// The repo's tests show which postings a preview balance adds up, and the tests of cmd/ show
// that it is the balance there is once the drafts are confirmed. What these tests show is what
// the use case does with what the repo reads: it asks once, for the day the clock says, and says
// each figure of a balance as the total, what is confirmed of it, what the drafts add and what it
// is made of, and says of the balances together how many drafts wait and whether any of them is
// confirmed: the things that a page writes and has no business deciding.

// previewRow is a row as the repo reads it: of the account and the instrument, with what is
// confirmed and what the drafts add of what the account holds, and of what is unbilled.
func previewRow(a model.Account, in model.Instrument, held, unbilled model.PreviewFigure) model.PreviewBalance {
	return model.PreviewBalance{AccountID: a.ID, InstrumentID: in.ID, Held: held, Unbilled: unbilled}
}

// figure is what is confirmed of a figure, and what the drafts add to it.
func figure(confirmed, drafts string) model.PreviewFigure {
	return model.PreviewFigure{Confirmed: dec(confirmed), Drafts: dec(drafts)}
}

// previews reads the preview balances, and fails the test when it cannot.
func (e *env) previews() usecase.GetPreviewBalancesOutput {
	e.t.Helper()
	out, err := e.svc.GetPreviewBalances(context.Background(), usecase.GetPreviewBalancesInput{})
	if err != nil {
		e.t.Fatalf("GetPreviewBalances: %v", err)
	}
	return out
}

// figureText writes a figure as "880 = confirmed 1000 + drafts -120 (both)".
func figureText(f usecase.PreviewFigure) string {
	return fmt.Sprintf("%s = confirmed %s + drafts %s (%s)", f.Total, f.Confirmed, f.Drafts, f.Basis)
}

// previewLines writes the balances as "cash TWD held 880 = confirmed 1000 + drafts -120 (both) |
// unbilled 0 = confirmed 0 + drafts 0 (confirmed)", one to a line, in the order they come.
func (e *env) previewLines(out usecase.GetPreviewBalancesOutput) []string {
	names := e.names()
	lines := make([]string, len(out.Balances))
	for i, b := range out.Balances {
		lines[i] = fmt.Sprintf("%s %s held %s | unbilled %s", names[b.AccountID], names[b.InstrumentID], figureText(b.Held), figureText(b.Unbilled))
	}
	return lines
}

func TestGetPreviewBalancesSaysEachFigureAsWhatIsConfirmedAndWhatTheDraftsAddAndWhatItIsMadeOf(t *testing.T) {
	e := newEnv(t)
	// The cash holds 1,000 and a lunch of 120 is waiting, and it holds 25.50 of dollars that drafts
	// take all away; the bank holds nothing confirmed, and a bill that it owes is converted into
	// instalments in a draft (3,000 is paid off and owed again, 1,000 of it falling due already, so
	// that 2,000 is still to come); the card owes 500, 11,000 more is unbilled, and nothing waits for it.
	e.txns.preview = model.BalancePreview{
		Drafts: 7,
		Balances: []model.PreviewBalance{
			previewRow(e.bank, e.twd, figure("0", "2000"), figure("0", "-2000")),
			previewRow(e.card, e.twd, figure("-500", "0"), figure("-11000", "0")),
			previewRow(e.cash, e.twd, figure("1000", "-120"), figure("0", "0")),
			previewRow(e.cash, e.usd, figure("25.5", "-25.5"), figure("0", "0")),
		},
	}
	opened := e.tx.calls

	got := e.previews()

	// Each figure is what it will be, with what is confirmed of it and what the drafts add apart,
	// and says what it is made of: a figure that no draft changes is what is confirmed, one that
	// drafts make the whole of has nothing confirmed, and one that they change has both.
	wantLines(t, e.previewLines(got),
		"bank TWD held 2000 = confirmed 0 + drafts 2000 (drafts) | unbilled -2000 = confirmed 0 + drafts -2000 (drafts)",
		"card TWD held -500 = confirmed -500 + drafts 0 (confirmed) | unbilled -11000 = confirmed -11000 + drafts 0 (confirmed)",
		"cash TWD held 880 = confirmed 1000 + drafts -120 (both) | unbilled 0 = confirmed 0 + drafts 0 (confirmed)",
		"cash USD held 0 = confirmed 25.5 + drafts -25.5 (both) | unbilled 0 = confirmed 0 + drafts 0 (confirmed)",
	)
	// How many drafts wait comes with the balances, and is not what any figure is made of: 7 wait,
	// of which some are not in any figure.
	if got.DraftCount != 7 || !got.HasConfirmed {
		t.Errorf("DraftCount = %d and HasConfirmed = %v, want 7 and true: the cash holds something", got.DraftCount, got.HasConfirmed)
	}
	// The use case opens no database transaction and reads once.
	if e.tx.calls != opened || len(e.txns.previewAsked) != 1 {
		t.Errorf("%d database transactions opened and the repo asked %d times, want 0 and 1", e.tx.calls-opened, len(e.txns.previewAsked))
	}
}

// Nothing is confirmed when every figure is the drafts', and that is said once, of all of them: a
// figure the drafts cancel to nothing has what it was confirmed, and is not of drafts alone.
func TestGetPreviewBalancesSaysWhetherAnythingIsConfirmedAtAll(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name string
		rows []model.PreviewBalance
		want bool
	}{
		{"no balance", nil, false},
		{"drafts only", []model.PreviewBalance{
			previewRow(e.cash, e.twd, figure("0", "-900"), figure("0", "0")),
			previewRow(e.card, e.twd, figure("0", "-2000"), figure("0", "-3000")),
		}, false},
		{"what is held is confirmed, and the drafts take all of it", []model.PreviewBalance{
			previewRow(e.cash, e.twd, figure("0", "-900"), figure("0", "0")),
			previewRow(e.bank, e.twd, figure("500", "-500"), figure("0", "0")),
		}, true},
		{"only a plan that is confirmed", []model.PreviewBalance{
			previewRow(e.cash, e.twd, figure("0", "-900"), figure("0", "0")),
			previewRow(e.card, e.twd, figure("0", "0"), figure("-3000", "0")),
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.txns.preview = model.BalancePreview{Balances: tt.rows, Drafts: 3}

			if got := e.previews().HasConfirmed; got != tt.want {
				t.Errorf("HasConfirmed = %v, want %v", got, tt.want)
			}
		})
	}
}

// What a preview counts is what has happened by today, and today is what the clock says in the
// timezone of the application: the day it falls on as its own location reads it, not the day it
// is in UTC, which is what GetBalances and GetDraftSummary do.
func TestGetPreviewBalancesIsOfTodayAsTheClockReadsItInItsOwnTimezone(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"the afternoon of the 14th in Taipei", time.Date(2026, 11, 14, 15, 30, 0, 0, taipei), on(2026, time.November, 14)},
		{"a minute before the 15th in Taipei", time.Date(2026, 11, 14, 23, 59, 0, 0, taipei), on(2026, time.November, 14)},
		// It is still the 14th in UTC then: the clock's own location decides.
		{"half past midnight on the 15th in Taipei", time.Date(2026, 11, 15, 0, 30, 0, 0, taipei), on(2026, time.November, 15)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.clock.now = tt.now
			e.previews()
			if got := e.txns.previewAsked[len(e.txns.previewAsked)-1]; !got.Equal(tt.want) {
				t.Errorf("the repo was asked about %s, want today as the clock reads it, %s", got.Format(time.DateOnly), tt.want.Format(time.DateOnly))
			}
		})
	}
}

// Drafts that move no balance today (dated later, or of no account) are waiting all the same: the
// preview has no balance, and says how many drafts wait.
func TestGetPreviewBalancesOfDraftsThatMoveNothingTodayIsNoBalanceAndTheCountOfTheDrafts(t *testing.T) {
	e := newEnv(t)
	e.txns.preview = model.BalancePreview{Drafts: 2}

	got, err := e.svc.GetPreviewBalances(context.Background(), usecase.GetPreviewBalancesInput{})

	if err != nil || len(got.Balances) != 0 || got.DraftCount != 2 || got.HasConfirmed {
		t.Errorf("GetPreviewBalances = %+v, %v, want no balance, 2 drafts, nothing confirmed, and no error", got, err)
	}
}

func TestGetPreviewBalancesOfALedgerWithNothingInItIsEmptyAndNotAnError(t *testing.T) {
	e := newEnv(t)

	got, err := e.svc.GetPreviewBalances(context.Background(), usecase.GetPreviewBalancesInput{})

	if err != nil || len(got.Balances) != 0 || got.DraftCount != 0 || got.HasConfirmed {
		t.Errorf("GetPreviewBalances = %+v, %v, want nothing and no error", got, err)
	}
}

func TestWhenTheRepoCannotReadThePreviewBalancesTheErrorGoesOn(t *testing.T) {
	e := newEnv(t)
	boom := errors.New("the database is down")
	e.txns.readErr = boom

	_, err := e.svc.GetPreviewBalances(context.Background(), usecase.GetPreviewBalancesInput{})

	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "preview balances") {
		t.Errorf("err = %v, want what the repo returned, with what was being done", err)
	}
}
