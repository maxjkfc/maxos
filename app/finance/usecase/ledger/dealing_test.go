package ledger_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

func TestRecordExpenseSplitsAPaymentWithOthersAndChecksTheirPartiesWithTheCatalog(t *testing.T) {
	e := newEnv(t)
	a, b := e.refs.addParty("A", false), e.refs.addParty("B", false)
	in := e.lunch("split-000000001")
	in.Lines = []usecase.CategoryLine{{CategoryID: e.food.ID, Amount: dec("300")}}
	in.Shares = []usecase.Share{
		{PartyID: a, Amount: dec("300")},
		{PartyID: b, Amount: dec("300"), Memo: "晚餐"},
	}

	out, err := e.svc.RecordExpense(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	wantLines(t, e.lines(out.Transaction),
		"account(cash) -900 TWD",
		"expense(food) +300 TWD, base +300",
		"dealing(A) +300 TWD",
		"dealing(B) +300 TWD",
	)
	if got := out.Transaction.Postings[3].Memo; got != "晚餐" {
		t.Errorf("memo of the share = %q, want it kept", got)
	}
	if len(e.refs.calls) != 1 || !slices.Equal(e.refs.calls[0].PartyIDs, []uuid.UUID{a, b}) {
		t.Errorf("the catalog was asked to verify %+v, want the two parties of the shares, in one call", e.refs.calls)
	}
	if e.tx.calls != 1 || e.txns.inserts != 1 {
		t.Errorf("%d database transactions and %d inserts, want 1 and 1", e.tx.calls, e.txns.inserts)
	}
}

func TestRecordExpensePaidByAPartyMovesNoAccount(t *testing.T) {
	e := newEnv(t)
	friend := e.refs.addParty("F", false)
	in := e.lunch("paid-by-party-01")
	in.AccountID = nil
	in.PaidByPartyID = &friend
	in.Lines = []usecase.CategoryLine{{CategoryID: e.food.ID, Amount: dec("300")}}

	out, err := e.svc.RecordExpense(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	wantLines(t, e.lines(out.Transaction),
		"dealing(F) -300 TWD",
		"expense(food) +300 TWD, base +300",
	)
	if len(e.refs.calls) != 1 || !slices.Equal(e.refs.calls[0].PartyIDs, []uuid.UUID{friend}) {
		t.Errorf("the catalog was asked to verify %+v, want the party that paid", e.refs.calls)
	}
}

func TestRecordExpenseKeepsTheDiscountOfALine(t *testing.T) {
	e := newEnv(t)
	in := e.lunch("discount-0000001")
	in.Lines = []usecase.CategoryLine{{CategoryID: e.food.ID, Amount: dec("450"), Discount: decPtr("50")}}

	out, err := e.svc.RecordExpense(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	wantLines(t, e.lines(out.Transaction),
		"account(cash) -450 TWD",
		"expense(food) +450 TWD, base +450, discount 50",
	)
	// What comes back is a copy, not a pointer into the store.
	*out.Transaction.Postings[1].Discount = dec("1")
	if got := e.txns.byID[out.Transaction.ID].Postings[1].Discount; got == nil || !got.Equal(dec("50")) {
		t.Errorf("stored discount = %v, want 50 left as it was", got)
	}
}

func TestRecordExpenseRefusesPartiesThatCannotBeUsed(t *testing.T) {
	e := newEnv(t)
	good := e.refs.addParty("A", false)
	archived := e.refs.addParty("B", true)
	unknown := uuid.Must(uuid.NewV7())

	in := e.lunch("bad-parties-0001")
	in.Shares = []usecase.Share{
		{PartyID: good, Amount: dec("100")},
		{PartyID: archived, Amount: dec("100")},
		{PartyID: unknown, Amount: dec("100")},
	}
	_, err := e.svc.RecordExpense(context.Background(), in)

	var refErr *catalogusecase.ReferenceError
	if !errors.As(err, &refErr) {
		t.Fatalf("err = %v, want the catalog's *ReferenceError", err)
	}
	want := []catalogusecase.ReferenceProblem{
		{Kind: catalogusecase.ReferenceParty, ID: archived, Archived: true},
		{Kind: catalogusecase.ReferenceParty, ID: unknown},
	}
	if !slices.Equal(refErr.Problems, want) {
		t.Errorf("problems = %+v, want every party that cannot be used: %+v", refErr.Problems, want)
	}

	// The party that paid is checked the same way.
	in = e.lunch("bad-parties-0002")
	in.AccountID = nil
	in.PaidByPartyID = &archived
	_, err = e.svc.RecordExpense(context.Background(), in)
	if !errors.As(err, &refErr) || len(refErr.Problems) != 1 || refErr.Problems[0].ID != archived {
		t.Errorf("err = %v, want the *ReferenceError of the party that paid", err)
	}
	if e.txns.inserts != 0 {
		t.Errorf("%d transactions stored, want none", e.txns.inserts)
	}
}

func TestRecordExpenseRefusesWhatMakesNoSenseOfWhoPaidAndForWhom(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(e *env, in *usecase.RecordExpenseInput)
		want   error
	}{
		{"an account and a party that both paid", func(e *env, in *usecase.RecordExpenseInput) {
			friend := e.refs.addParty("F", false)
			in.PaidByPartyID = &friend
		}, model.ErrTwoPayers},
		{"neither an account nor a party that paid", func(e *env, in *usecase.RecordExpenseInput) { in.AccountID = nil }, model.ErrNoPayer},
		{"shares in an expense that a party paid", func(e *env, in *usecase.RecordExpenseInput) {
			friend, other := e.refs.addParty("F", false), e.refs.addParty("G", false)
			in.AccountID = nil
			in.PaidByPartyID = &friend
			in.Shares = []usecase.Share{{PartyID: other, Amount: dec("100")}}
		}, model.ErrSharesWithPaidBy},
		{"no line of my own, only shares", func(e *env, in *usecase.RecordExpenseInput) {
			in.Lines = nil
			in.Shares = []usecase.Share{{PartyID: e.refs.addParty("A", false), Amount: dec("120")}}
		}, model.ErrNoLines},
		{"a party in two shares", func(e *env, in *usecase.RecordExpenseInput) {
			a := e.refs.addParty("A", false)
			in.Shares = []usecase.Share{{PartyID: a, Amount: dec("10")}, {PartyID: a, Amount: dec("20")}}
		}, model.ErrDuplicateParty},
		{"a share of nothing", func(e *env, in *usecase.RecordExpenseInput) {
			in.Shares = []usecase.Share{{PartyID: e.refs.addParty("A", false), Amount: dec("0")}}
		}, model.ErrAmountNotPositive},
		{"a share finer than the instrument", func(e *env, in *usecase.RecordExpenseInput) {
			in.Shares = []usecase.Share{{PartyID: e.refs.addParty("A", false), Amount: dec("1.234")}}
		}, model.ErrTooManyDecimals},
		{"a discount of nothing", func(e *env, in *usecase.RecordExpenseInput) { in.Lines[0].Discount = decPtr("0") }, model.ErrAmountNotPositive},
		{"a system category in a line", func(e *env, in *usecase.RecordExpenseInput) {
			in.Lines[0].CategoryID = model.UnrecordedExpenseCategoryID
		}, model.ErrSystemCategory},
		// Valid but for the instrument: a dealing is counted in a currency.
		{"shares in an instrument that is not a currency", func(e *env, in *usecase.RecordExpenseInput) {
			in.InstrumentID = e.btc.ID
			in.Lines[0].BaseAmount = decPtr("120")
			in.Shares = []usecase.Share{{PartyID: e.refs.addParty("A", false), Amount: dec("0.5")}}
		}, model.ErrNotCurrency},
		{"a party that paid, in an instrument that is not a currency", func(e *env, in *usecase.RecordExpenseInput) {
			friend := e.refs.addParty("F", false)
			in.AccountID = nil
			in.PaidByPartyID = &friend
			in.InstrumentID = e.btc.ID
			in.Lines[0].BaseAmount = decPtr("120")
		}, model.ErrNotCurrency},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			in := e.lunch("refused-0000001")
			tt.mutate(e, &in)

			out, err := e.svc.RecordExpense(context.Background(), in)

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if out.Transaction.ID != uuid.Nil || e.txns.inserts != 0 {
				t.Errorf("output = %+v, %d inserts, want nothing stored", out, e.txns.inserts)
			}
		})
	}
}

func TestRecordIncomeRefusesADiscountAndASystemCategory(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(e *env, in *usecase.RecordIncomeInput)
		want   error
	}{
		{"a discount", func(e *env, in *usecase.RecordIncomeInput) { in.Lines[0].Discount = decPtr("10") }, model.ErrDiscountNotAllowed},
		{"a system category", func(e *env, in *usecase.RecordIncomeInput) {
			in.Lines[0].CategoryID = model.UnrecordedIncomeCategoryID
		}, model.ErrSystemCategory},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			in := usecase.RecordIncomeInput{
				RecordDetails: e.details("income-0000001"), AccountID: e.bank.ID, InstrumentID: e.twd.ID,
				Lines: []usecase.CategoryLine{{CategoryID: e.salary.ID, Amount: dec("50000")}},
			}
			tt.mutate(e, &in)

			if _, err := e.svc.RecordIncome(context.Background(), in); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if e.txns.inserts != 0 {
				t.Errorf("%d transactions stored, want none", e.txns.inserts)
			}
		})
	}
}

func TestRecordDealingRecordsMoneyPaidOutAndReceived(t *testing.T) {
	e := newEnv(t)
	company := e.refs.addParty("C", false)

	paidOut, err := e.svc.RecordDealing(context.Background(), usecase.RecordDealingInput{
		RecordDetails: e.details("dealing-out-0001"), AccountID: e.bank.ID, PartyID: company, InstrumentID: e.twd.ID,
		Amount: dec("5000"), Direction: "paid_out",
	})
	if err != nil {
		t.Fatalf("RecordDealing paid out: %v", err)
	}
	received, err := e.svc.RecordDealing(context.Background(), usecase.RecordDealingInput{
		RecordDetails: e.details("dealing-in-00001"), AccountID: e.bank.ID, PartyID: company, InstrumentID: e.twd.ID,
		Amount: dec("5000"), Direction: "received",
	})
	if err != nil {
		t.Fatalf("RecordDealing received: %v", err)
	}

	wantLines(t, e.lines(paidOut.Transaction), "account(bank) -5000 TWD", "dealing(C) +5000 TWD")
	wantLines(t, e.lines(received.Transaction), "account(bank) +5000 TWD", "dealing(C) -5000 TWD")
	for _, out := range []usecase.RecordOutput{paidOut, received} {
		if out.Duplicate || out.Transaction.Kind != "dealing" || out.Transaction.Status != "draft" {
			t.Errorf("output = %+v, want a new draft dealing", out)
		}
	}
}

func TestRecordDealingRefuses(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(e *env, in *usecase.RecordDealingInput)
		want   error
	}{
		{"an instrument that is not a currency", func(e *env, in *usecase.RecordDealingInput) { in.InstrumentID = e.btc.ID }, model.ErrNotCurrency},
		{"a direction that is neither", func(e *env, in *usecase.RecordDealingInput) { in.Direction = "lend" }, model.ErrInvalidDealingDirection},
		{"an account that does not exist", func(e *env, in *usecase.RecordDealingInput) { in.AccountID = uuid.Must(uuid.NewV7()) }, model.ErrNotFound},
		{"an archived account", func(e *env, in *usecase.RecordDealingInput) { e.accounts.archive(e.bank.ID) }, model.ErrArchived},
		{"an amount of nothing", func(e *env, in *usecase.RecordDealingInput) { in.Amount = dec("0") }, model.ErrAmountNotPositive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			in := usecase.RecordDealingInput{
				RecordDetails: e.details("dealing-0000001"), AccountID: e.bank.ID, PartyID: e.refs.addParty("C", false),
				InstrumentID: e.twd.ID, Amount: dec("5000"), Direction: "paid_out",
			}
			tt.mutate(e, &in)

			if _, err := e.svc.RecordDealing(context.Background(), in); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if e.txns.inserts != 0 {
				t.Errorf("%d transactions stored, want none", e.txns.inserts)
			}
		})
	}
}

func TestRecordDealingRefusesAPartyThatCannotBeUsed(t *testing.T) {
	e := newEnv(t)
	archived := e.refs.addParty("C", true)
	unknown := uuid.Must(uuid.NewV7())

	for _, party := range []uuid.UUID{archived, unknown} {
		_, err := e.svc.RecordDealing(context.Background(), usecase.RecordDealingInput{
			RecordDetails: e.details("dealing-0000001"), AccountID: e.bank.ID, PartyID: party, InstrumentID: e.twd.ID,
			Amount: dec("5000"), Direction: "paid_out",
		})

		var refErr *catalogusecase.ReferenceError
		if !errors.As(err, &refErr) || len(refErr.Problems) != 1 || refErr.Problems[0].ID != party {
			t.Errorf("party %s: err = %v, want the *ReferenceError that names it", party, err)
		}
	}
	if e.txns.inserts != 0 {
		t.Errorf("%d transactions stored, want none", e.txns.inserts)
	}
}

func TestRecordRewardRecordsWhatTheCardGaveBack(t *testing.T) {
	e := newEnv(t)

	out, err := e.svc.RecordReward(context.Background(), usecase.RecordRewardInput{
		RecordDetails: e.details("reward-00000001"), AccountID: e.bank.ID, InstrumentID: e.twd.ID, Amount: dec("50"),
	})
	if err != nil {
		t.Fatalf("RecordReward: %v", err)
	}

	wantLines(t, e.lines(out.Transaction), "account(bank) +50 TWD", "reward -50 TWD, base -50")
	if out.Duplicate || out.Transaction.Kind != "reward" || out.Transaction.Status != "draft" {
		t.Errorf("output = %+v, want a new draft reward", out)
	}

	// In a foreign currency the base amount is the caller's, and is required.
	_, err = e.svc.RecordReward(context.Background(), usecase.RecordRewardInput{
		RecordDetails: e.details("reward-00000002"), AccountID: e.bank.ID, InstrumentID: e.usd.ID, Amount: dec("5"),
	})
	if !errors.Is(err, model.ErrBaseAmountRequired) {
		t.Fatalf("err = %v, want ErrBaseAmountRequired", err)
	}
	out, err = e.svc.RecordReward(context.Background(), usecase.RecordRewardInput{
		RecordDetails: e.details("reward-00000003"), AccountID: e.bank.ID, InstrumentID: e.usd.ID, Amount: dec("5"), BaseAmount: decPtr("160"),
	})
	if err != nil {
		t.Fatalf("RecordReward in USD: %v", err)
	}
	wantLines(t, e.lines(out.Transaction), "account(bank) +5 USD", "reward -5 USD, base -160")
}

func TestEveryRecordThatTakesFeesPassesThemOn(t *testing.T) {
	// The fee of each is 15 TWD, paid from cash, which is not the account of the
	// transaction it comes with.
	fee := func(e *env) []usecase.Fee {
		return []usecase.Fee{{AccountID: e.cash.ID, InstrumentID: e.twd.ID, CategoryID: e.fees.ID, Amount: dec("15"), Memo: "手續費"}}
	}
	tests := []struct {
		name     string
		record   func(e *env) (usecase.RecordOutput, error)
		postings []string
	}{
		{
			name: "an income",
			record: func(e *env) (usecase.RecordOutput, error) {
				return e.svc.RecordIncome(context.Background(), usecase.RecordIncomeInput{
					RecordDetails: e.details("fee-income-0001"), AccountID: e.bank.ID, InstrumentID: e.twd.ID,
					Lines: []usecase.CategoryLine{{CategoryID: e.salary.ID, Amount: dec("50000")}}, Fees: fee(e),
				})
			},
			postings: []string{
				"account(bank) +50000 TWD", "income(salary) -50000 TWD, base -50000",
				"account(cash) -15 TWD", "expense(fees) +15 TWD, base +15",
			},
		},
		{
			name: "a transfer",
			record: func(e *env) (usecase.RecordOutput, error) {
				return e.svc.RecordTransfer(context.Background(), usecase.RecordTransferInput{
					RecordDetails: e.details("fee-transfer-01"), FromAccountID: e.bank.ID, ToAccountID: e.cash.ID,
					InstrumentID: e.twd.ID, Amount: dec("1000"), Fees: fee(e),
				})
			},
			postings: []string{
				"account(bank) -1000 TWD", "account(cash) +1000 TWD",
				"account(cash) -15 TWD", "expense(fees) +15 TWD, base +15",
			},
		},
		{
			name: "an exchange",
			record: func(e *env) (usecase.RecordOutput, error) {
				return e.svc.RecordExchange(context.Background(), usecase.RecordExchangeInput{
					RecordDetails: e.details("fee-exchange-01"), FromAccountID: e.bank.ID, ToAccountID: e.cash.ID,
					FromInstrumentID: e.usd.ID, ToInstrumentID: e.twd.ID, FromAmount: dec("3000"), ToAmount: dec("93000"), Fees: fee(e),
				})
			},
			postings: []string{
				"account(bank) -3000 USD", "conversion +3000 USD", "conversion -93000 TWD", "account(cash) +93000 TWD",
				"account(cash) -15 TWD", "expense(fees) +15 TWD, base +15",
			},
		},
		{
			name: "a dealing",
			record: func(e *env) (usecase.RecordOutput, error) {
				return e.svc.RecordDealing(context.Background(), usecase.RecordDealingInput{
					RecordDetails: e.details("fee-dealing-001"), AccountID: e.bank.ID, PartyID: e.refs.addParty("C", false),
					InstrumentID: e.twd.ID, Amount: dec("5000"), Direction: "paid_out", Fees: fee(e),
				})
			},
			postings: []string{
				"account(bank) -5000 TWD", "dealing(C) +5000 TWD",
				"account(cash) -15 TWD", "expense(fees) +15 TWD, base +15",
			},
		},
		{
			name: "a reward",
			record: func(e *env) (usecase.RecordOutput, error) {
				return e.svc.RecordReward(context.Background(), usecase.RecordRewardInput{
					RecordDetails: e.details("fee-reward-0001"), AccountID: e.bank.ID, InstrumentID: e.twd.ID,
					Amount: dec("50"), Fees: fee(e),
				})
			},
			postings: []string{
				"account(bank) +50 TWD", "reward -50 TWD, base -50",
				"account(cash) -15 TWD", "expense(fees) +15 TWD, base +15",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)

			out, err := tt.record(e)
			if err != nil {
				t.Fatalf("record: %v", err)
			}

			wantLines(t, e.lines(out.Transaction), tt.postings...)
			if got := out.Transaction.Postings[len(tt.postings)-1].Memo; got != "手續費" {
				t.Errorf("memo of the fee = %q, want it kept", got)
			}
		})
	}
}

func TestAFeeInAnotherCurrencyThanTheTransactionHasItsOwnBaseAmount(t *testing.T) {
	e := newEnv(t)
	// A transfer of BTC, with the network fee paid in USD from the bank: the base
	// currency, TWD, is not an instrument of the request, and is still the one the
	// base amount of the fee is checked against.
	in := usecase.RecordTransferInput{
		RecordDetails: e.details("fee-btc-0000001"), FromAccountID: e.bank.ID, ToAccountID: e.cash.ID,
		InstrumentID: e.btc.ID, Amount: dec("0.5"),
		Fees: []usecase.Fee{{AccountID: e.bank.ID, InstrumentID: e.usd.ID, CategoryID: e.fees.ID, Amount: dec("1")}},
	}

	_, err := e.svc.RecordTransfer(context.Background(), in)
	if !errors.Is(err, model.ErrBaseAmountRequired) {
		t.Fatalf("err = %v, want ErrBaseAmountRequired: the fee is in USD", err)
	}

	in.IdempotencyKey = "fee-btc-0000002"
	in.Fees[0].BaseAmount = decPtr("32")
	out, err := e.svc.RecordTransfer(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordTransfer: %v", err)
	}
	wantLines(t, e.lines(out.Transaction),
		"account(bank) -0.5 BTC", "account(cash) +0.5 BTC", "account(bank) -1 USD", "expense(fees) +1 USD, base +32",
	)
}

func TestAFeeThatCannotBeUsedIsRefusedAndNamed(t *testing.T) {
	unknown := uuid.Must(uuid.NewV7())
	tests := []struct {
		name   string
		mutate func(e *env, f *usecase.Fee)
		want   error
		names  string
	}{
		{"a category that does not exist", func(e *env, f *usecase.Fee) { f.CategoryID = unknown }, model.ErrNotFound, unknown.String()},
		{"an account that does not exist", func(e *env, f *usecase.Fee) { f.AccountID = unknown }, model.ErrNotFound, unknown.String()},
		{"an instrument that does not exist", func(e *env, f *usecase.Fee) { f.InstrumentID = unknown }, model.ErrNotFound, unknown.String()},
		{"a category of income", func(e *env, f *usecase.Fee) { f.CategoryID = e.salary.ID }, model.ErrWrongCategoryDirection, "salary"},
		{"an archived category", func(e *env, f *usecase.Fee) { e.categories.archive(e.fees.ID) }, model.ErrArchived, "fees"},
		{"a system category", func(e *env, f *usecase.Fee) { f.CategoryID = model.UnrecordedExpenseCategoryID }, model.ErrSystemCategory, "未記錄"},
		{"an archived account", func(e *env, f *usecase.Fee) { e.accounts.archive(e.cash.ID) }, model.ErrArchived, "cash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			// The fee is paid from cash, which is not an account of the transfer: an
			// archived cash is the fee's trouble alone.
			other := e.accounts.add("other")
			fee := usecase.Fee{AccountID: e.cash.ID, InstrumentID: e.twd.ID, CategoryID: e.fees.ID, Amount: dec("15")}
			tt.mutate(e, &fee)

			_, err := e.svc.RecordTransfer(context.Background(), usecase.RecordTransferInput{
				RecordDetails: e.details("fee-refused-001"), FromAccountID: e.bank.ID, ToAccountID: other.ID,
				InstrumentID: e.twd.ID, Amount: dec("1000"), Fees: []usecase.Fee{fee},
			})

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if !strings.Contains(err.Error(), "fee 1") || !strings.Contains(err.Error(), tt.names) {
				t.Errorf("err = %q, want it to say it is fee 1 and name %s", err, tt.names)
			}
			if e.txns.inserts != 0 {
				t.Errorf("%d transactions stored, want none", e.txns.inserts)
			}
		})
	}
}

func TestRecordDealingWithAKeyUsedBeforeReturnsTheDealingRecordedThen(t *testing.T) {
	e := newEnv(t)
	company := e.refs.addParty("C", false)
	in := usecase.RecordDealingInput{
		RecordDetails: e.details("dealing-key-0001"), AccountID: e.bank.ID, PartyID: company, InstrumentID: e.twd.ID,
		Amount: dec("5000"), Direction: "paid_out",
	}
	first, err := e.svc.RecordDealing(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordDealing: %v", err)
	}

	// Whatever the second request says, it is the first one that stands.
	in.Amount = dec("1")
	in.Direction = "received"
	again, err := e.svc.RecordDealing(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordDealing with the key again: %v", err)
	}

	if !again.Duplicate || again.Transaction.ID != first.Transaction.ID {
		t.Fatalf("output = %+v, want the first dealing, marked as a duplicate", again)
	}
	wantLines(t, e.lines(again.Transaction), "account(bank) -5000 TWD", "dealing(C) +5000 TWD")
	if e.txns.inserts != 1 {
		t.Errorf("%d inserts, want only the first", e.txns.inserts)
	}
}

func TestGetDealingsPutsThePartiesInTheOrderOfTheirNames(t *testing.T) {
	e := newEnv(t)
	// The ids are made in this order and the repo lists by id, so it lists Zed
	// before Amy and Mia; the names put them the other way round.
	zed := e.refs.addParty("Zed", false)
	amy := e.refs.addParty("Amy", false)
	mia := e.refs.addParty("Mia", true) // an archived party's dealings still count
	e.txns.dealingRows = []model.Dealing{
		{PartyID: zed, InstrumentID: e.btc.ID, Quantity: dec("1")},
		{PartyID: zed, InstrumentID: e.twd.ID, Quantity: dec("100")},
		{PartyID: amy, InstrumentID: e.twd.ID, Quantity: dec("-50")},
		{PartyID: amy, InstrumentID: e.usd.ID, Quantity: dec("0")},
		{PartyID: mia, InstrumentID: e.twd.ID, Quantity: dec("7")},
	}

	out, err := e.svc.GetDealings(context.Background(), usecase.GetDealingsInput{})
	if err != nil {
		t.Fatalf("GetDealings: %v", err)
	}

	// By party name, and within a party as the repo listed the instruments: by
	// code. A dealing of zero is still listed.
	want := []usecase.Dealing{
		{PartyID: amy, InstrumentID: e.twd.ID, Quantity: dec("-50")},
		{PartyID: amy, InstrumentID: e.usd.ID, Quantity: dec("0")},
		{PartyID: mia, InstrumentID: e.twd.ID, Quantity: dec("7")},
		{PartyID: zed, InstrumentID: e.btc.ID, Quantity: dec("1")},
		{PartyID: zed, InstrumentID: e.twd.ID, Quantity: dec("100")},
	}
	if len(out.Dealings) != len(want) {
		t.Fatalf("dealings = %+v, want %d of them", out.Dealings, len(want))
	}
	for i, w := range want {
		got := out.Dealings[i]
		if got.PartyID != w.PartyID || got.InstrumentID != w.InstrumentID || !got.Quantity.Equal(w.Quantity) {
			t.Errorf("dealing %d = %+v, want %+v", i+1, got, w)
		}
	}
	if !e.refs.listedWith.IncludeArchived {
		t.Error("the catalog was asked for the parties in use only: an archived party's dealings would be lost")
	}
}

// One party has nothing to put in order, so the catalog is asked about that
// party alone, and not for the list of all of them.
func TestGetDealingsOfOnePartyAsksTheCatalogAboutThatPartyAndKeepsTheOrderOfTheRepo(t *testing.T) {
	e := newEnv(t)
	amy := e.refs.addParty("Amy", true) // archived: what an archived party owes still counts
	e.txns.dealingRows = []model.Dealing{
		{PartyID: amy, InstrumentID: e.btc.ID, Quantity: dec("1")},
		{PartyID: amy, InstrumentID: e.twd.ID, Quantity: dec("100")},
	}

	out, err := e.svc.GetDealings(context.Background(), usecase.GetDealingsInput{PartyID: &amy})
	if err != nil {
		t.Fatalf("GetDealings: %v", err)
	}

	if e.txns.dealingsFor == nil || *e.txns.dealingsFor != amy {
		t.Errorf("the repo was asked for %v, want the party", e.txns.dealingsFor)
	}
	if len(e.refs.calls) != 1 || !slices.Equal(e.refs.calls[0].PartyIDs, []uuid.UUID{amy}) || e.refs.listPartiesCalls != 0 {
		t.Errorf("the catalog was asked to verify %+v and for the list %d times, want the party alone and no list", e.refs.calls, e.refs.listPartiesCalls)
	}
	if len(out.Dealings) != 2 || out.Dealings[0].InstrumentID != e.btc.ID || out.Dealings[1].InstrumentID != e.twd.ID {
		t.Errorf("dealings = %+v, want the two of the party, in the order of the repo", out.Dealings)
	}
}

func TestGetDealingsOfOnePartyThatOwesNothingIsEmpty(t *testing.T) {
	e := newEnv(t)
	amy := e.refs.addParty("Amy", false)

	out, err := e.svc.GetDealings(context.Background(), usecase.GetDealingsInput{PartyID: &amy})
	if err != nil {
		t.Fatalf("GetDealings: %v", err)
	}

	if len(out.Dealings) != 0 || e.refs.listPartiesCalls != 0 {
		t.Errorf("dealings = %+v, %d list calls, want none of either for a party with nothing", out.Dealings, e.refs.listPartiesCalls)
	}
}

// A party the catalog does not know has no dealings to show: asking for it is a
// mistake, which is not the same as an answer of none. The error is the one the
// dealings of all the parties give for a party they cannot place, ErrNotFound of
// the finance domain, and not what the catalog says, which the callers of the
// ledger cannot match. The catalog here says what the real one does: a
// *ReferenceError that is not for an archived entry.
func TestGetDealingsOfAPartyTheCatalogDoesNotKnowIsNotFoundAndTheRepoIsNotAsked(t *testing.T) {
	e := newEnv(t)
	stranger := uuid.Must(uuid.NewV7())

	_, err := e.svc.GetDealings(context.Background(), usecase.GetDealingsInput{PartyID: &stranger})

	if !errors.Is(err, model.ErrNotFound) || !strings.Contains(err.Error(), stranger.String()) {
		t.Fatalf("err = %v, want ErrNotFound naming the party", err)
	}
	if e.txns.dealingsFor != nil {
		t.Errorf("the repo was asked for %v, want it not to be asked", e.txns.dealingsFor)
	}
}

func TestGetDealingsReportsWhatItCannotRead(t *testing.T) {
	boom := errors.New("connection reset")

	t.Run("the repo fails", func(t *testing.T) {
		e := newEnv(t)
		e.txns.readErr = boom

		if _, err := e.svc.GetDealings(context.Background(), usecase.GetDealingsInput{}); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the failure of the repo", err)
		}
	})
	t.Run("the catalog fails", func(t *testing.T) {
		e := newEnv(t)
		e.txns.dealingRows = []model.Dealing{{PartyID: e.refs.addParty("Amy", false), InstrumentID: e.twd.ID, Quantity: dec("1")}}
		e.refs.listPartiesErr = boom

		if _, err := e.svc.GetDealings(context.Background(), usecase.GetDealingsInput{}); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the failure of the catalog", err)
		}
	})
	t.Run("the catalog cannot check the party", func(t *testing.T) {
		e := newEnv(t)
		party := e.refs.addParty("Amy", false)
		e.refs.verifyErr = boom

		_, err := e.svc.GetDealings(context.Background(), usecase.GetDealingsInput{PartyID: &party})

		// The catalog failed and says nothing of the party: that is not a party that
		// is not found.
		if !errors.Is(err, boom) || errors.Is(err, model.ErrNotFound) {
			t.Fatalf("err = %v, want the failure of the catalog, which is not ErrNotFound", err)
		}
		if e.txns.dealingsFor != nil {
			t.Errorf("the repo was asked for %v, want it not to be asked", e.txns.dealingsFor)
		}
	})
	t.Run("a dealing of a party the catalog does not know", func(t *testing.T) {
		e := newEnv(t)
		stranger := uuid.Must(uuid.NewV7())
		e.txns.dealingRows = []model.Dealing{{PartyID: stranger, InstrumentID: e.twd.ID, Quantity: dec("1")}}
		e.refs.addParty("Amy", false)

		_, err := e.svc.GetDealings(context.Background(), usecase.GetDealingsInput{})

		if !errors.Is(err, model.ErrNotFound) || !strings.Contains(err.Error(), stranger.String()) {
			t.Fatalf("err = %v, want ErrNotFound naming the party: it is a dealing no list can place", err)
		}
	})
}
