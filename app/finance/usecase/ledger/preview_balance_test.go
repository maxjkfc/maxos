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
// each balance as its confirmed part and its drafts, and the two added up.

// previewRow is a row as the repo reads it: of the account and the instrument, with what is
// confirmed, what the drafts add, and what is unbilled.
func previewRow(a model.Account, in model.Instrument, confirmed, drafts, unbilled string) model.PreviewBalance {
	return model.PreviewBalance{
		AccountID: a.ID, InstrumentID: in.ID,
		Confirmed: dec(confirmed), Drafts: dec(drafts), UnbilledInstalment: dec(unbilled),
	}
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

// previewLines writes the balances as "cash TWD 880 = confirmed 1000 + drafts -120 unbilled 0",
// one to a line, in the order they come.
func (e *env) previewLines(out usecase.GetPreviewBalancesOutput) []string {
	names := e.names()
	lines := make([]string, len(out.Balances))
	for i, b := range out.Balances {
		lines[i] = fmt.Sprintf("%s %s %s = confirmed %s + drafts %s unbilled %s",
			names[b.AccountID], names[b.InstrumentID], b.Quantity, b.Confirmed, b.Drafts, b.UnbilledInstalment)
	}
	return lines
}

func TestGetPreviewBalancesSaysWhatIsConfirmedAndWhatTheDraftsAddAndTheTwoAddedUp(t *testing.T) {
	e := newEnv(t)
	// The cash holds 1,000 and a lunch of 120 is waiting, and it holds 25.50 of dollars that
	// drafts take all away; the bank holds nothing confirmed and a transfer's leg of 3,000 is
	// waiting; the card owes 500 and 11,000 more is unbilled, and nothing waits for it.
	e.txns.previewRows = []model.PreviewBalance{
		previewRow(e.bank, e.twd, "0", "3000", "0"),
		previewRow(e.card, e.twd, "-500", "0", "-11000"),
		previewRow(e.cash, e.twd, "1000", "-120", "0"),
		previewRow(e.cash, e.usd, "25.5", "-25.5", "0"),
	}
	opened := e.tx.calls

	got := e.previews()

	// What is confirmed is what GetBalances says, and the drafts are on their own beside it: the
	// quantity is the two, and a balance that no draft moves is its confirmed part.
	wantLines(t, e.previewLines(got),
		"bank TWD 3000 = confirmed 0 + drafts 3000 unbilled 0",
		"card TWD -500 = confirmed -500 + drafts 0 unbilled -11000",
		"cash TWD 880 = confirmed 1000 + drafts -120 unbilled 0",
		"cash USD 0 = confirmed 25.5 + drafts -25.5 unbilled 0",
	)
	// The use case opens no database transaction and reads once.
	if e.tx.calls != opened || len(e.txns.previewAsked) != 1 {
		t.Errorf("%d database transactions opened and the repo asked %d times, want 0 and 1", e.tx.calls-opened, len(e.txns.previewAsked))
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

func TestGetPreviewBalancesOfALedgerWithNothingInItIsEmptyAndNotAnError(t *testing.T) {
	e := newEnv(t)

	got, err := e.svc.GetPreviewBalances(context.Background(), usecase.GetPreviewBalancesInput{})

	if err != nil || len(got.Balances) != 0 {
		t.Errorf("GetPreviewBalances = %+v, %v, want no balance and no error", got, err)
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
