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
// PostgreSQL: what the preview says is what GetBalances says once every draft is confirmed, and
// what the drafts add to it is what GetDraftSummary says they change.

// previewBalances reads what the accounts would hold today, by the clock, for each account and
// instrument.
func (w *draftsWorld) previewBalances() map[balanceKey]usecase.PreviewBalance {
	w.t.Helper()
	out, err := w.GetPreviewBalances.GetPreviewBalances(context.Background(), usecase.GetPreviewBalancesInput{})
	if err != nil {
		w.t.Fatalf("GetPreviewBalances: %v", err)
	}
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
// "cash TWD 980 = confirmed 800 + drafts 180".
func (w *draftsWorld) previewLines() []string {
	w.t.Helper()
	out, err := w.GetPreviewBalances.GetPreviewBalances(context.Background(), usecase.GetPreviewBalancesInput{})
	if err != nil {
		w.t.Fatalf("GetPreviewBalances: %v", err)
	}
	lines := make([]string, len(out.Balances))
	for i, b := range out.Balances {
		lines[i] = fmt.Sprintf("%s %s %s = confirmed %s + drafts %s", w.names[b.AccountID], w.names[b.InstrumentID], b.Quantity, b.Confirmed, b.Drafts)
		if !b.UnbilledInstalment.IsZero() {
			lines[i] += fmt.Sprintf(" unbilled %s", b.UnbilledInstalment)
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
// account by account; and once every draft is confirmed the balances of that day are the preview
// there was, which is what makes it a preview.
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
	// the exchange; the wallet holds nothing confirmed, and the card has a fee of the transfer, and
	// nothing of the plan, whose periods are not due, nor of the purchase of the 20th of December.
	wantStatus(t, "the preview on the 14th of November", w.previewLines(),
		"bank TWD 50785 = confirmed 5000 + drafts 45785",
		"card TWD -10 = confirmed 0 + drafts -10",
		"cash TWD 980 = confirmed 800 + drafts 180",
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
			// The quantity is what is confirmed and what the drafts add; what is confirmed is what
			// the balances say, and what the drafts add is what the summary of the drafts says.
			if !p.Quantity.Equal(p.Confirmed.Add(p.Drafts)) {
				t.Errorf("%s, %s: the quantity %s is not the %s confirmed and the %s of the drafts", day, name, p.Quantity, p.Confirmed, p.Drafts)
			}
			if !p.Confirmed.Equal(held[k].Quantity) || !p.UnbilledInstalment.Equal(held[k].UnbilledInstalment) {
				t.Errorf("%s, %s: %s confirmed and %s unbilled in the preview, and the balances say %s and %s", day, name, p.Confirmed, p.UnbilledInstalment, held[k].Quantity, held[k].UnbilledInstalment)
			}
			if !p.Drafts.Equal(byAccount[k]) {
				t.Errorf("%s, %s: the drafts add %s, and the summary of the drafts says a net of %s", day, name, p.Drafts, byAccount[k])
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
		// in no total, so the accounts add up to what the summary says the drafts change.
		sum := map[uuid.UUID]decimal.Decimal{}
		for _, p := range previews[i] {
			sum[p.InstrumentID] = sum[p.InstrumentID].Add(p.Drafts)
		}
		for instrument, net := range sum {
			if !net.Equal(together[instrument]) {
				t.Errorf("%s, %s: the drafts add %s to the accounts all together, and the summary of the drafts says %s", day, w.names[instrument], net, together[instrument])
			}
		}
	}

	// Confirming every draft there is: the balances of each of the days are the preview that was of
	// it, and nothing is waiting, so the preview is the balances. The plans have their unbilled
	// instalment now.
	w.confirmDrafts()
	for i, d := range days {
		w.clk.setDay(d)
		held := w.heldBalances()
		day := d.Format(time.DateOnly)
		for k, p := range previews[i] {
			if !held[k].Quantity.Equal(p.Quantity) {
				t.Errorf("%s, %s %s: the preview said %s, and the balance is %s once every draft is confirmed", day, w.names[k.account], w.names[k.instrument], p.Quantity, held[k].Quantity)
			}
		}
		for k, b := range held {
			if _, found := previews[i][k]; !found && !b.Quantity.IsZero() {
				t.Errorf("%s, %s %s: the balance is %s once every draft is confirmed, and the preview had none", day, w.names[k.account], w.names[k.instrument], b.Quantity)
			}
		}
		for k, p := range w.previewBalances() {
			if !p.Drafts.IsZero() || !p.Confirmed.Equal(held[k].Quantity) || !p.Quantity.Equal(held[k].Quantity) {
				t.Errorf("%s, %s %s: %s confirmed and %s of drafts in the preview with every draft confirmed, and the balance is %s", day, w.names[k.account], w.names[k.instrument], p.Confirmed, p.Drafts, held[k].Quantity)
			}
		}
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

	held, previews := w.heldBalances(), w.previewBalances()

	if w.summary().Count != 0 || len(held) == 0 || len(previews) != len(held) {
		t.Fatalf("%d drafts, %d balances and %d preview balances, want no draft and as many of each", w.summary().Count, len(held), len(previews))
	}
	for k, b := range held {
		p := previews[k]
		if !p.Quantity.Equal(b.Quantity) || !p.Confirmed.Equal(b.Quantity) || !p.Drafts.IsZero() || !p.UnbilledInstalment.Equal(b.UnbilledInstalment) {
			t.Errorf("%s %s: preview %+v, balance %+v, want the same, and no draft", w.names[k.account], w.names[k.instrument], p, b)
		}
	}
	// The card owes the principal that is due and what is not.
	if got := previews[balanceKey{w.card, model.BaseCurrencyID}]; !got.Quantity.Equal(dec("-2000")) || !got.UnbilledInstalment.Equal(dec("-4000")) {
		t.Errorf("the card = %+v, want -2000 held and -4000 unbilled", got)
	}
}
