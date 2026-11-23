package cmd_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// The balances of a ledger that has drafts waiting, through the real use cases on a real
// PostgreSQL: what the preview says, in both figures of a balance, is what GetBalances says once
// every draft is confirmed, and what the drafts add to what an account holds is what
// GetDraftSummary says they change.

// previewOut reads what the accounts would hold today, by the clock.
func (w *draftsWorld) previewOut() usecase.GetPreviewBalancesOutput {
	w.t.Helper()
	out, err := w.GetPreviewBalances.GetPreviewBalances(context.Background(), usecase.GetPreviewBalancesInput{})
	if err != nil {
		w.t.Fatalf("GetPreviewBalances: %v", err)
	}
	return out
}

// previewBalances reads what the accounts would hold today, by the clock, for each account and
// instrument.
func (w *draftsWorld) previewBalances() map[balanceKey]usecase.PreviewBalance {
	w.t.Helper()
	out := w.previewOut()
	got := make(map[balanceKey]usecase.PreviewBalance, len(out.Balances))
	for _, b := range out.Balances {
		k := balanceKey{b.AccountID, b.InstrumentID}
		if _, again := got[k]; again {
			w.t.Errorf("%s %s is twice in the preview balances", w.names[k.account], w.names[k.instrument])
		}
		got[k] = b
	}
	return got
}

// previewLines reads the preview balances as GetPreviewBalances orders them, and writes each as
// "cash TWD 980 = confirmed 800 + drafts 180", with " | unbilled -6000 = confirmed 0 + drafts
// -6000" when something is unbilled.
func (w *draftsWorld) previewLines() []string {
	w.t.Helper()
	out := w.previewOut()
	lines := make([]string, len(out.Balances))
	for i, b := range out.Balances {
		lines[i] = fmt.Sprintf("%s %s %s = confirmed %s + drafts %s", w.names[b.AccountID], w.names[b.InstrumentID], b.Held.Total, b.Held.Confirmed, b.Held.Drafts)
		if !b.Unbilled.Confirmed.IsZero() || !b.Unbilled.Drafts.IsZero() {
			lines[i] += fmt.Sprintf(" | unbilled %s = confirmed %s + drafts %s", b.Unbilled.Total, b.Unbilled.Confirmed, b.Unbilled.Drafts)
		}
	}
	return lines
}

// heldBalances reads what the accounts hold today, by the clock, for each account and instrument.
func (w *draftsWorld) heldBalances() map[balanceKey]usecase.Balance {
	w.t.Helper()
	out, err := w.GetBalances.GetBalances(context.Background(), usecase.GetBalancesInput{})
	if err != nil {
		w.t.Fatalf("GetBalances: %v", err)
	}
	held := make(map[balanceKey]usecase.Balance, len(out.Balances))
	for _, b := range out.Balances {
		held[balanceKey{b.AccountID, b.InstrumentID}] = b
	}
	return held
}

// netOfTheSummary is what the summary of the drafts says they change: for each account and
// instrument, the net of its amounts of all the months added up, and for each instrument the net
// of the amounts of all the drafts, which leave out what moves between my own accounts.
func netOfTheSummary(s usecase.GetDraftSummaryOutput) (byAccount map[balanceKey]decimal.Decimal, together map[uuid.UUID]decimal.Decimal) {
	byAccount, together = map[balanceKey]decimal.Decimal{}, map[uuid.UUID]decimal.Decimal{}
	for _, month := range s.Months {
		for _, account := range month.Accounts {
			if account.AccountID == nil {
				continue
			}
			for _, a := range account.Amounts {
				k := balanceKey{*account.AccountID, a.InstrumentID}
				byAccount[k] = byAccount[k].Add(a.Net)
			}
		}
	}
	for _, a := range s.Amounts {
		together[a.InstrumentID] = a.Net
	}
	return byAccount, together
}

// A ledger that has drafts of every shape in it, and what is confirmed, void and dated later:
// on each of several days, the preview is the balances and the net of the summary of the drafts,
// account by account; and once every draft is confirmed the balances of that day, the instalment
// plans' principal that is not due among them, are the preview there was, which is what makes it
// a preview.
func TestThePreviewBalancesAreTheBalancesOnceEveryDraftIsConfirmedAndTheNetOfTheSummaryThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	w := newDraftsWorld(t)

	// What is confirmed: the cash holds 1,000 less a lunch of 200, and the bank 5,000. A lunch
	// of 2,000 was confirmed and voided: it is not anywhere.
	opening := func(account uuid.UUID, amount string) {
		t.Helper()
		d := w.recorded(dateOn(2026, time.November, 1))
		out, err := w.SetOpeningBalance.SetOpeningBalance(ctx, usecase.SetOpeningBalanceInput{
			RecordDetails: d, AccountID: account, InstrumentID: model.BaseCurrencyID, Amount: dec(amount),
		})
		if err != nil {
			t.Fatalf("SetOpeningBalance: %v", err)
		}
		w.confirm(t, out.Transaction)
	}
	opening(w.cash, "1000")
	opening(w.bank, "5000")
	w.confirm(t, w.expense(dateOn(2026, time.November, 5), w.cash, "200"))
	voided := w.expense(dateOn(2026, time.November, 6), w.cash, "2000")
	w.confirm(t, voided)
	if _, err := w.Void.VoidTransaction(ctx, voided.ID); err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}

	// What waits: a lunch today (the 14th) and one on the 20th, a salary into the bank, a transfer
	// from the bank to the wallet with a fee that the bank pays, and one from the wallet to the cash
	// whose fee the card pays, an exchange of 3,200 TWD into 100 USD, a purchase of 6,000 on the
	// card in three periods and a bill of the bank converted into three, an expense that a party
	// paid for me, and a purchase on the card dated the 20th of December.
	w.expense(dateOn(2026, time.November, 14), w.cash, "120")
	w.expense(dateOn(2026, time.November, 20), w.cash, "80")
	w.income(dateOn(2026, time.November, 3), w.bank, "50000")
	w.transfer(dateOn(2026, time.November, 10), w.bank, w.wallet, "1000", w.fee(w.bank, "15"))
	w.transfer(dateOn(2026, time.November, 11), w.wallet, w.cash, "300", w.fee(w.card, "10"))
	w.exchange(dateOn(2026, time.November, 12), w.bank, w.wallet)
	w.plan(dateOn(2026, time.November, 10), w.card, "6000", dateOn(2026, time.December, 5), dateOn(2027, time.January, 5), dateOn(2027, time.February, 5))
	w.billInstalment(dateOn(2026, time.November, 16), w.bank, dateOn(2026, time.November, 20), dateOn(2026, time.December, 20), dateOn(2027, time.January, 20))
	w.paidByParty(dateOn(2026, time.November, 8), w.amy, "90")
	w.expense(dateOn(2026, time.December, 20), w.card, "60")

	// On the 14th: the cash holds 800 and the lunch of today takes 120 away, and the transfer from
	// the wallet brings 300; the bank holds 5,000 and drafts add a salary, a transfer, its fee and
	// the exchange, and no conversion yet, which is of the 16th; the wallet holds nothing confirmed;
	// and the card has a fee of the transfer, and the whole principal of the plan to pay, whose
	// periods are not due, and nothing of the purchase of the 20th of December.
	wantStatus(t, "the preview on the 14th of November", w.previewLines(),
		"bank TWD 50785 = confirmed 5000 + drafts 45785",
		"card TWD -10 = confirmed 0 + drafts -10 | unbilled -6000 = confirmed 0 + drafts -6000",
		"cash TWD 980 = confirmed 800 + drafts 180",
		"wallet TWD 700 = confirmed 0 + drafts 700",
		"wallet USD 100 = confirmed 0 + drafts 100",
	)
	// Ten drafts wait, of which the lunch of the 20th, the conversion of the 16th, the purchase of the
	// 20th of December and the expense that a party paid are in no figure on the 14th.
	if got := w.previewOut().DraftCount; got != 10 || got != w.summary().Count {
		t.Errorf("%d drafts wait in the preview, the summary counts %d, want 10 in both", got, w.summary().Count)
	}
	// On the 20th the conversion is in: the bank holds 3,000 more than it owed, less the 1,000 of the
	// first period that falls due that day, and 2,000 is still to pay of it; the lunch of the 20th is
	// in too.
	w.clk.setDay(dateOn(2026, time.November, 20))
	wantStatus(t, "the preview on the 20th of November", w.previewLines(),
		"bank TWD 52785 = confirmed 5000 + drafts 47785 | unbilled -2000 = confirmed 0 + drafts -2000",
		"card TWD -10 = confirmed 0 + drafts -10 | unbilled -6000 = confirmed 0 + drafts -6000",
		"cash TWD 900 = confirmed 800 + drafts 100",
		"wallet TWD 700 = confirmed 0 + drafts 700",
		"wallet USD 100 = confirmed 0 + drafts 100",
	)

	days := []time.Time{
		dateOn(2026, time.November, 14), dateOn(2026, time.November, 16), dateOn(2026, time.November, 20), dateOn(2026, time.December, 5),
		dateOn(2026, time.December, 20), dateOn(2027, time.January, 20), dateOn(2027, time.February, 5),
	}
	previews := make([]map[balanceKey]usecase.PreviewBalance, len(days))
	for i, d := range days {
		w.clk.setDay(d)
		previews[i] = w.previewBalances()
		held := w.heldBalances()
		byAccount, together := netOfTheSummary(w.summary())
		day := d.Format(time.DateOnly)

		for k, p := range previews[i] {
			name := w.names[k.account] + " " + w.names[k.instrument]
			// A figure is what is confirmed and what the drafts add; what is confirmed is what the
			// balances say, in both figures, and what the drafts add to what the account holds is what
			// the summary of the drafts says.
			if !p.Held.Total.Equal(p.Held.Confirmed.Add(p.Held.Drafts)) || !p.Unbilled.Total.Equal(p.Unbilled.Confirmed.Add(p.Unbilled.Drafts)) {
				t.Errorf("%s, %s: the totals %s and %s are not the %s and %s confirmed and the %s and %s of the drafts", day, name,
					p.Held.Total, p.Unbilled.Total, p.Held.Confirmed, p.Unbilled.Confirmed, p.Held.Drafts, p.Unbilled.Drafts)
			}
			if !p.Held.Confirmed.Equal(held[k].Quantity) || !p.Unbilled.Confirmed.Equal(held[k].UnbilledInstalment) {
				t.Errorf("%s, %s: %s held and %s unbilled confirmed in the preview, and the balances say %s and %s", day, name,
					p.Held.Confirmed, p.Unbilled.Confirmed, held[k].Quantity, held[k].UnbilledInstalment)
			}
			if !p.Held.Drafts.Equal(byAccount[k]) {
				t.Errorf("%s, %s: the drafts add %s, and the summary of the drafts says a net of %s", day, name, p.Held.Drafts, byAccount[k])
			}
		}
		for k, b := range held {
			if _, found := previews[i][k]; !found {
				t.Errorf("%s, %s %s holds %s and has no preview balance", day, w.names[k.account], w.names[k.instrument], b.Quantity)
			}
		}
		for k, net := range byAccount {
			if _, found := previews[i][k]; !found && !net.IsZero() {
				t.Errorf("%s, %s %s: the summary of the drafts says a net of %s, and it has no preview balance", day, w.names[k.account], w.names[k.instrument], net)
			}
		}
		// All the accounts together: the transfers between my own accounts are in two accounts and
		// in no total, so the accounts add up to what the summary says the drafts change. The
		// principal of the plans is not an account posting, and is not in the summary: it is in
		// the figure of what is unbilled, which moves from the one to the other with the plan.
		sum := map[uuid.UUID]decimal.Decimal{}
		for _, p := range previews[i] {
			sum[p.InstrumentID] = sum[p.InstrumentID].Add(p.Held.Drafts)
		}
		for instrument, net := range sum {
			if !net.Equal(together[instrument]) {
				t.Errorf("%s, %s: the drafts add %s to the accounts all together, and the summary of the drafts says %s", day, w.names[instrument], net, together[instrument])
			}
		}
	}

	// Confirming every draft there is: the balances of each of the days are the preview that was of
	// it, the principal of the plans that is not due included, and nothing is waiting, so the preview
	// is the balances.
	w.confirmDrafts()
	for i, d := range days {
		w.clk.setDay(d)
		held := w.heldBalances()
		day := d.Format(time.DateOnly)
		for k, p := range previews[i] {
			name := w.names[k.account] + " " + w.names[k.instrument]
			if !held[k].Quantity.Equal(p.Held.Total) || !held[k].UnbilledInstalment.Equal(p.Unbilled.Total) {
				t.Errorf("%s, %s: the preview said %s held and %s unbilled, and the balance is %s and %s once every draft is confirmed", day, name,
					p.Held.Total, p.Unbilled.Total, held[k].Quantity, held[k].UnbilledInstalment)
			}
		}
		for k, b := range held {
			if _, found := previews[i][k]; !found && (!b.Quantity.IsZero() || !b.UnbilledInstalment.IsZero()) {
				t.Errorf("%s, %s %s: the balance is %s and %s unbilled once every draft is confirmed, and the preview had none", day, w.names[k.account], w.names[k.instrument], b.Quantity, b.UnbilledInstalment)
			}
		}
		after := w.previewOut()
		if after.DraftCount != 0 || !after.HasConfirmed {
			t.Errorf("%s: %d drafts wait and HasConfirmed is %v once every draft is confirmed, want none, and something confirmed", day, after.DraftCount, after.HasConfirmed)
		}
		for _, p := range after.Balances {
			k := balanceKey{p.AccountID, p.InstrumentID}
			if !p.Held.Drafts.IsZero() || !p.Unbilled.Drafts.IsZero() || !p.Held.Confirmed.Equal(held[k].Quantity) || !p.Unbilled.Confirmed.Equal(held[k].UnbilledInstalment) {
				t.Errorf("%s, %s %s: %s and %s of drafts in the preview with every draft confirmed, and %s and %s confirmed, the balance is %s and %s unbilled", day, w.names[k.account], w.names[k.instrument],
					p.Held.Drafts, p.Unbilled.Drafts, p.Held.Confirmed, p.Unbilled.Confirmed, held[k].Quantity, held[k].UnbilledInstalment)
			}
		}
	}
}

// A ledger whose transactions are all drafts has nothing confirmed, and the preview says so once, with
// how many drafts wait: it is what a page needs to say that every number on it is a draft's. The
// figures that drafts make say that nothing of them is confirmed; and once one transaction is
// confirmed, something is.
func TestThePreviewBalancesOfALedgerOfDraftsOnlyHaveNothingConfirmedAndSayWhichFiguresAreDraftsThroughTheRealUseCases(t *testing.T) {
	w := newDraftsWorld(t)
	lunch := w.expense(dateOn(2026, time.November, 5), w.cash, "750")
	w.plan(dateOn(2026, time.November, 10), w.card, "6000", dateOn(2026, time.December, 5), dateOn(2027, time.January, 5), dateOn(2027, time.February, 5))
	w.clk.setDay(dateOn(2026, time.December, 5))

	out := w.previewOut()

	if out.HasConfirmed || out.DraftCount != 2 || len(out.Balances) != 2 {
		t.Fatalf("HasConfirmed = %v, DraftCount = %d, %d balances, want nothing confirmed, 2 drafts and the 2 accounts the drafts move", out.HasConfirmed, out.DraftCount, len(out.Balances))
	}
	for _, b := range out.Balances {
		for name, f := range map[string]usecase.PreviewFigure{"held": b.Held, "unbilled": b.Unbilled} {
			if !f.Confirmed.IsZero() || (!f.Drafts.IsZero() && f.Basis != usecase.PreviewBasisDrafts) || (f.Drafts.IsZero() && f.Basis != usecase.PreviewBasisConfirmed) {
				t.Errorf("%s %s: %s figure %+v, want nothing confirmed, and the basis of drafts where they change it", w.names[b.AccountID], w.names[b.InstrumentID], name, f)
			}
		}
	}

	// One confirmed, and what it moves is confirmed: the cash is now both, or all confirmed; and the
	// plan, which waits still, is the other draft.
	w.confirm(t, lunch)
	out = w.previewOut()
	if !out.HasConfirmed || out.DraftCount != 1 {
		t.Errorf("HasConfirmed = %v and DraftCount = %d once the expense is confirmed, want something confirmed and 1 draft", out.HasConfirmed, out.DraftCount)
	}
}

// A ledger that has no draft has a preview that is its balances: the same accounts and instruments,
// the same quantities and unbilled instalment, and nothing that the drafts add.
func TestThePreviewBalancesOfALedgerWithNoDraftAreItsBalancesThroughTheRealUseCases(t *testing.T) {
	w := newDraftsWorld(t)
	// An income and a purchase paid in instalments, which are confirmed, and a card that nothing
	// has happened on.
	w.confirm(t, w.income(dateOn(2026, time.November, 3), w.bank, "50000"), w.plan(dateOn(2026, time.November, 10), w.card, "6000",
		dateOn(2026, time.December, 5), dateOn(2027, time.January, 5), dateOn(2027, time.February, 5)))
	w.clk.setDay(dateOn(2026, time.December, 5))

	held, out := w.heldBalances(), w.previewOut()
	previews := w.previewBalances()

	if w.summary().Count != 0 || len(held) == 0 || len(previews) != len(held) || out.DraftCount != 0 || !out.HasConfirmed {
		t.Fatalf("%d drafts, %d balances and %d preview balances, DraftCount = %d, HasConfirmed = %v, want no draft, as many of each, and something confirmed",
			w.summary().Count, len(held), len(previews), out.DraftCount, out.HasConfirmed)
	}
	for k, b := range held {
		p := previews[k]
		if !p.Held.Total.Equal(b.Quantity) || !p.Held.Confirmed.Equal(b.Quantity) || !p.Held.Drafts.IsZero() ||
			!p.Unbilled.Total.Equal(b.UnbilledInstalment) || !p.Unbilled.Confirmed.Equal(b.UnbilledInstalment) || !p.Unbilled.Drafts.IsZero() ||
			p.Held.Basis != usecase.PreviewBasisConfirmed || p.Unbilled.Basis != usecase.PreviewBasisConfirmed {
			t.Errorf("%s %s: preview %+v, balance %+v, want the same, and no draft", w.names[k.account], w.names[k.instrument], p, b)
		}
	}
	// The card owes the principal that is due and what is not.
	if got := previews[balanceKey{w.card, model.BaseCurrencyID}]; !got.Held.Total.Equal(dec("-2000")) || !got.Unbilled.Total.Equal(dec("-4000")) {
		t.Errorf("the card = %+v, want -2000 held and -4000 unbilled", got)
	}
}
