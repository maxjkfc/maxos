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

// What a figure is made of is said in the words of the port, which are typed and are its own: a caller
// compares a Basis with usecase.PreviewBasisConfirmed, PreviewBasisBoth and PreviewBasisDrafts, and the
// use case gives each of the three of the model the one of that name.
func TestGetPreviewBalancesNamesWhatAFigureIsMadeOfInTheWordsOfThePort(t *testing.T) {
	e := newEnv(t)
	e.txns.preview = model.BalancePreview{Drafts: 2, Balances: []model.PreviewBalance{
		previewRow(e.cash, e.twd, figure("800", "0"), figure("0", "-300")), // nothing changes it; drafts make it all
		previewRow(e.bank, e.twd, figure("800", "-120"), figure("0", "0")), // confirmed in part and changed; nothing
	}}

	got := e.previews().Balances

	want := []struct{ held, unbilled usecase.PreviewBasis }{
		{usecase.PreviewBasisConfirmed, usecase.PreviewBasisDrafts},
		{usecase.PreviewBasisBoth, usecase.PreviewBasisConfirmed},
	}
	if len(got) != len(want) {
		t.Fatalf("%d balances, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Held.Basis != w.held || got[i].Unbilled.Basis != w.unbilled {
			t.Errorf("balance %d is made of %q held and %q unbilled, want %q and %q", i, got[i].Held.Basis, got[i].Unbilled.Basis, w.held, w.unbilled)
		}
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

// totalLines writes the totals as "TWD held 880 = confirmed 1000 + drafts -120 (both) | unbilled ...",
// one to a line, in the order they come.
func (e *env) totalLines(totals []usecase.PreviewTotal) []string {
	names := e.names()
	lines := make([]string, len(totals))
	for i, t := range totals {
		lines[i] = fmt.Sprintf("%s held %s | unbilled %s", names[t.InstrumentID], figureText(t.Held), figureText(t.Unbilled))
	}
	return lines
}

// setKind makes the account of the kind: the fake repo makes them all cash.
func (e *env) setKind(a model.Account, kind model.AccountKind) {
	a.Kind = kind
	e.accounts.accounts[a.ID] = a
}

// A page that writes one figure of many accounts does not add them up, so the use case does, an
// instrument at a time: for all the accounts and for those of each kind. A total is the sums of
// what is confirmed and of what the drafts add, and says what it is made of as any figure does.
func TestGetPreviewBalancesAddsTheBalancesUpForAllTheAccountsAndForEachKindAnInstrumentAtATime(t *testing.T) {
	e := newEnv(t)
	e.setKind(e.bank, model.AccountKindBank)
	// The instruments first appear as TWD, USD and BTC, and the totals are by code, as the balances
	// of an account are. The card owes in dollars that only drafts make, and in TWD both what is
	// confirmed and what drafts add, with instalments that are not yet due.
	e.txns.preview = model.BalancePreview{
		Drafts: 5,
		Balances: []model.PreviewBalance{
			previewRow(e.bank, e.twd, figure("1000", "-120"), figure("0", "0")),
			previewRow(e.bank, e.usd, figure("25.5", "0"), figure("0", "0")),
			previewRow(e.card, e.twd, figure("-500", "-2000"), figure("-11000", "-6000")),
			previewRow(e.card, e.usd, figure("0", "-30"), figure("0", "0")),
			previewRow(e.cash, e.btc, figure("0.5", "0"), figure("0", "0")),
			previewRow(e.cash, e.twd, figure("300", "0"), figure("0", "0")),
		},
	}

	got := e.previews()

	wantLines(t, e.totalLines(got.Totals),
		"BTC held 0.5 = confirmed 0.5 + drafts 0 (confirmed) | unbilled 0 = confirmed 0 + drafts 0 (confirmed)",
		"TWD held -1320 = confirmed 800 + drafts -2120 (both) | unbilled -17000 = confirmed -11000 + drafts -6000 (both)",
		"USD held -4.5 = confirmed 25.5 + drafts -30 (both) | unbilled 0 = confirmed 0 + drafts 0 (confirmed)",
	)
	// By kind, in the order of the kinds, and by code in each.
	if len(got.Kinds) != 3 {
		t.Fatalf("%d kinds %v, want bank, card and cash", len(got.Kinds), got.Kinds)
	}
	for i, want := range []struct {
		kind  string
		lines []string
	}{
		{"bank", []string{
			"TWD held 880 = confirmed 1000 + drafts -120 (both) | unbilled 0 = confirmed 0 + drafts 0 (confirmed)",
			"USD held 25.5 = confirmed 25.5 + drafts 0 (confirmed) | unbilled 0 = confirmed 0 + drafts 0 (confirmed)",
		}},
		{"card", []string{
			"TWD held -2500 = confirmed -500 + drafts -2000 (both) | unbilled -17000 = confirmed -11000 + drafts -6000 (both)",
			"USD held -30 = confirmed 0 + drafts -30 (drafts) | unbilled 0 = confirmed 0 + drafts 0 (confirmed)",
		}},
		{"cash", []string{
			"BTC held 0.5 = confirmed 0.5 + drafts 0 (confirmed) | unbilled 0 = confirmed 0 + drafts 0 (confirmed)",
			"TWD held 300 = confirmed 300 + drafts 0 (confirmed) | unbilled 0 = confirmed 0 + drafts 0 (confirmed)",
		}},
	} {
		if got.Kinds[i].Kind != want.kind {
			t.Errorf("kind %d is %q, want %q", i, got.Kinds[i].Kind, want.kind)
			continue
		}
		wantLines(t, e.totalLines(got.Kinds[i].Totals), want.lines...)
	}
	// The kinds are a division of the accounts: of each instrument, they add up to its total.
	for _, total := range got.Totals {
		held, unbilled := dec("0"), dec("0")
		for _, kind := range got.Kinds {
			for _, k := range kind.Totals {
				if k.InstrumentID == total.InstrumentID {
					held, unbilled = held.Add(k.Held.Total), unbilled.Add(k.Unbilled.Total)
				}
			}
		}
		if !held.Equal(total.Held.Total) || !unbilled.Equal(total.Unbilled.Total) {
			t.Errorf("%s: the kinds add up to %s held and %s unbilled, and the total says %s and %s",
				e.names()[total.InstrumentID], held, unbilled, total.Held.Total, total.Unbilled.Total)
		}
	}
	// The balances themselves are what they were.
	if len(got.Balances) != 6 || got.DraftCount != 5 || !got.HasConfirmed {
		t.Errorf("%d balances, DraftCount %d, HasConfirmed %v, want 6, 5 and true", len(got.Balances), got.DraftCount, got.HasConfirmed)
	}
	// Each list is read once, however many balances there are.
	if e.accounts.lists != 1 || e.instruments.lists != 1 {
		t.Errorf("the accounts were listed %d times and the instruments %d times, want once each", e.accounts.lists, e.instruments.lists)
	}
}

// An archived account that holds something is in the totals, as it is in the balances: the sums are
// of every balance, and a total that left it out would not add up the balances under it. It is of
// its kind, and an archived instrument is in the order of the codes where its code puts it, not
// after the others as one the catalog did not have would be.
func TestGetPreviewBalancesCountsTheAccountsThatAreArchivedAndTheInstrumentsToo(t *testing.T) {
	e := newEnv(t)
	e.setKind(e.bank, model.AccountKindBank)
	e.accounts.archive(e.cash.ID)
	e.instruments.archive(e.btc.ID)
	e.txns.preview = model.BalancePreview{Balances: []model.PreviewBalance{
		previewRow(e.bank, e.twd, figure("1", "0"), figure("0", "0")),
		previewRow(e.bank, e.usd, figure("10", "0"), figure("0", "0")),
		previewRow(e.cash, e.btc, figure("0.5", "0"), figure("0", "0")),
		previewRow(e.cash, e.usd, figure("5", "0"), figure("0", "0")),
	}}

	got := e.previews()

	wantLines(t, e.totalLines(got.Totals),
		"BTC held 0.5 = confirmed 0.5 + drafts 0 (confirmed) | unbilled 0 = confirmed 0 + drafts 0 (confirmed)",
		"TWD held 1 = confirmed 1 + drafts 0 (confirmed) | unbilled 0 = confirmed 0 + drafts 0 (confirmed)",
		"USD held 15 = confirmed 15 + drafts 0 (confirmed) | unbilled 0 = confirmed 0 + drafts 0 (confirmed)",
	)
	if len(got.Kinds) != 2 || got.Kinds[0].Kind != "bank" || got.Kinds[1].Kind != "cash" {
		t.Fatalf("kinds %v, want bank and cash, of which the cash account is archived", got.Kinds)
	}
	wantLines(t, e.totalLines(got.Kinds[1].Totals),
		"BTC held 0.5 = confirmed 0.5 + drafts 0 (confirmed) | unbilled 0 = confirmed 0 + drafts 0 (confirmed)",
		"USD held 5 = confirmed 5 + drafts 0 (confirmed) | unbilled 0 = confirmed 0 + drafts 0 (confirmed)",
	)
}

// With no balance there is nothing to add up and nothing to look up: the lists are not read.
func TestGetPreviewBalancesWithNoBalanceHasNoTotalsAndReadsNoList(t *testing.T) {
	e := newEnv(t)
	e.txns.preview = model.BalancePreview{Drafts: 2}

	got := e.previews()

	if len(got.Totals) != 0 || len(got.Kinds) != 0 || e.accounts.lists != 0 || e.instruments.lists != 0 {
		t.Errorf("totals %v and kinds %v with %d and %d lists read, want none of them", got.Totals, got.Kinds, e.accounts.lists, e.instruments.lists)
	}
}

func TestWhenTheListsThatTheTotalsNeedCannotBeReadTheErrorGoesOn(t *testing.T) {
	boom := errors.New("the database is down")
	tests := []struct {
		name string
		fail func(e *env)
	}{
		{"the accounts", func(e *env) { e.accounts.listErr = boom }},
		{"the instruments", func(e *env) { e.instruments.listErr = boom }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.txns.preview = model.BalancePreview{Balances: []model.PreviewBalance{previewRow(e.cash, e.twd, figure("1", "0"), figure("0", "0"))}}
			tt.fail(e)

			_, err := e.svc.GetPreviewBalances(context.Background(), usecase.GetPreviewBalancesInput{})

			if !errors.Is(err, boom) || !strings.Contains(err.Error(), "preview balances") {
				t.Errorf("err = %v, want what the list returned, with what was being done", err)
			}
		})
	}
}
