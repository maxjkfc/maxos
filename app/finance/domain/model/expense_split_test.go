package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// unrecordedCategory returns a system category of the direction, as the model
// knows it.
func unrecordedCategory(direction model.Direction) model.Category {
	id := model.UnrecordedExpenseCategoryID
	if direction == model.DirectionIncome {
		id = model.UnrecordedIncomeCategoryID
	}
	return model.Category{ID: id, Direction: direction, Name: "未記錄"}
}

func TestNewExpenseSplitsAPaymentWithOthers(t *testing.T) {
	card := newCash(t, "信用卡")
	food := topLevel(t, model.DirectionExpense, "飲食")
	twd := newTWD(t)
	a, b := newParty(), newParty()
	names := map[uuid.UUID]string{card.ID: "卡", food.ID: "飲食", twd.ID: "TWD", a: "A", b: "B"}

	// I pay 900 from the card: my own share is 300, and A and B owe 300 each.
	txn, err := model.NewExpense(model.ExpenseParams{
		Header:     header(testKey),
		Account:    &card,
		Instrument: twd,
		Lines:      []model.CategoryLine{{Category: food, Amount: dec("300")}},
		Shares: []model.Share{
			{PartyID: a, Amount: dec("300"), Memo: " A 的份 "},
			{PartyID: b, Amount: dec("300")},
		},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}

	wantPostings(t, txn, names,
		"account(卡) -900 TWD",
		"expense(飲食) +300 TWD, base +300",
		"dealing(A) +300 TWD",
		"dealing(B) +300 TWD",
	)
	if txn.Kind != model.KindExpense {
		t.Errorf("kind = %q, want an expense: only my own share is spent", txn.Kind)
	}
	if txn.Postings[2].Memo != "A 的份" {
		t.Errorf("memo of the share = %q, want it trimmed", txn.Postings[2].Memo)
	}
}

func TestNewExpenseSplitInAForeignCurrencyKeepsTheSharesInThatCurrency(t *testing.T) {
	card := newCash(t, "Ether.fi 卡")
	food := topLevel(t, model.DirectionExpense, "飲食")
	usd := newCurrency(t, "USD", 2)
	twd := newTWD(t)
	a := newParty()
	names := map[uuid.UUID]string{card.ID: "卡", food.ID: "飲食", usd.ID: "USD", twd.ID: "TWD", a: "A"}

	// 100 USD paid; 40 of it is mine, worth 1,280 TWD that day; A owes 60.
	txn, err := model.NewExpense(model.ExpenseParams{
		Header:       header(testKey),
		Account:      &card,
		Instrument:   usd,
		BaseCurrency: twd,
		Lines:        []model.CategoryLine{{Category: food, Amount: dec("40"), BaseAmount: decPtr("1280")}},
		Shares:       []model.Share{{PartyID: a, Amount: dec("60")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}

	// What A owes is not an expense and has no base amount: only my 40 does.
	wantPostings(t, txn, names,
		"account(卡) -100 USD",
		"expense(飲食) +40 USD, base +1280",
		"dealing(A) +60 USD",
	)
}

func TestNewExpensePaidByAPartyIsWhatIOweThem(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	household := topLevel(t, model.DirectionExpense, "日用品")
	twd := newTWD(t)
	friend := newParty()
	names := map[uuid.UUID]string{food.ID: "飲食", household.ID: "日用品", twd.ID: "TWD", friend: "F"}

	// Friend F pays for my 300 dinner: no account of mine moves.
	txn, err := model.NewExpense(model.ExpenseParams{
		Header:     header(testKey),
		PaidBy:     &friend,
		Instrument: twd,
		Lines:      []model.CategoryLine{{Category: food, Amount: dec("300")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	wantPostings(t, txn, names,
		"dealing(F) -300 TWD",
		"expense(飲食) +300 TWD, base +300",
	)
	for _, p := range txn.Postings {
		if p.AccountID != nil {
			t.Errorf("%s posting is of an account, want no account: nobody used one", p.Book)
		}
	}

	// What I owe the friend is all the lines.
	txn, err = model.NewExpense(model.ExpenseParams{
		Header:     header(testKey),
		PaidBy:     &friend,
		Instrument: twd,
		Lines: []model.CategoryLine{
			{Category: food, Amount: dec("300")},
			{Category: household, Amount: dec("120")},
		},
	})
	if err != nil {
		t.Fatalf("NewExpense with two lines: %v", err)
	}
	wantPostings(t, txn, names,
		"dealing(F) -420 TWD",
		"expense(飲食) +300 TWD, base +300",
		"expense(日用品) +120 TWD, base +120",
	)
}

func TestNewExpenseKeepsTheDiscountAsARecordOnly(t *testing.T) {
	cash := newCash(t, "現金")
	food := topLevel(t, model.DirectionExpense, "飲食")
	twd := newTWD(t)
	names := map[uuid.UUID]string{cash.ID: "現金", food.ID: "飲食", twd.ID: "TWD"}

	// Paid 450 after a 50 discount of the merchant.
	txn, err := model.NewExpense(model.ExpenseParams{
		Header:     header(testKey),
		Account:    &cash,
		Instrument: twd,
		Lines:      []model.CategoryLine{{Category: food, Amount: dec("450"), Discount: decPtr("50")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}

	// The account loses what was paid, and the expense is what was paid: the 50
	// is neither a balance nor a posting of its own.
	wantPostings(t, txn, names,
		"account(現金) -450 TWD",
		"expense(飲食) +450 TWD, base +450, discount 50",
	)
}

// Only what a party owes, or is owed, is counted in a currency. Paying with an
// instrument that is not one is an expense like any other, while nobody owes
// anything.
func TestNewExpenseInAnInstrumentThatIsNotACurrencyIsAllowedWhileNobodyOwesAnything(t *testing.T) {
	wallet := newCash(t, "錢包")
	food := topLevel(t, model.DirectionExpense, "飲食")
	btc := newNonCurrency(t, "BTC", model.InstrumentKindCrypto, 8)
	twd := newTWD(t)
	names := map[uuid.UUID]string{wallet.ID: "錢包", food.ID: "飲食", btc.ID: "BTC", twd.ID: "TWD"}

	txn, err := model.NewExpense(model.ExpenseParams{
		Header:       header(testKey),
		Account:      &wallet,
		Instrument:   btc,
		BaseCurrency: twd,
		Lines:        []model.CategoryLine{{Category: food, Amount: dec("0.0005"), BaseAmount: decPtr("1500")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v, want an expense paid with an instrument that is not a currency", err)
	}

	wantPostings(t, txn, names,
		"account(錢包) -0.0005 BTC",
		"expense(飲食) +0.0005 BTC, base +1500",
	)
}

func TestNewExpenseSplitRefuses(t *testing.T) {
	cash := newCash(t, "現金")
	food := topLevel(t, model.DirectionExpense, "飲食")
	twd := newTWD(t)
	btc := newNonCurrency(t, "BTC", model.InstrumentKindCrypto, 8)
	a, b, friend := newParty(), newParty(), newParty()
	valid := func() model.ExpenseParams {
		return model.ExpenseParams{
			Header:     header(testKey),
			Account:    &cash,
			Instrument: twd,
			Lines:      []model.CategoryLine{{Category: food, Amount: dec("300")}},
			Shares:     []model.Share{{PartyID: a, Amount: dec("300")}, {PartyID: b, Amount: dec("300")}},
		}
	}

	tests := []struct {
		name   string
		mutate func(p *model.ExpenseParams)
		want   error
	}{
		{"a party in two shares", func(p *model.ExpenseParams) {
			p.Shares = append(p.Shares, model.Share{PartyID: a, Amount: dec("10")})
		}, model.ErrDuplicateParty},
		{"an account and a party that both paid", func(p *model.ExpenseParams) {
			p.PaidBy = &friend
			p.Shares = nil
		}, model.ErrTwoPayers},
		{"neither an account nor a party that paid", func(p *model.ExpenseParams) { p.Account = nil }, model.ErrNoPayer},
		{"shares in an expense that a party paid", func(p *model.ExpenseParams) {
			p.Account = nil
			p.PaidBy = &friend
		}, model.ErrSharesWithPaidBy},
		{"no line of my own, only shares", func(p *model.ExpenseParams) { p.Lines = nil }, model.ErrNoLines},
		{"a zero share", func(p *model.ExpenseParams) { p.Shares[1].Amount = dec("0") }, model.ErrAmountNotPositive},
		{"a negative share", func(p *model.ExpenseParams) { p.Shares[0].Amount = dec("-300") }, model.ErrAmountNotPositive},
		{"a share finer than the instrument", func(p *model.ExpenseParams) { p.Shares[0].Amount = dec("300.001") }, model.ErrTooManyDecimals},
		{"a zero discount", func(p *model.ExpenseParams) { p.Lines[0].Discount = decPtr("0") }, model.ErrAmountNotPositive},
		{"a negative discount", func(p *model.ExpenseParams) { p.Lines[0].Discount = decPtr("-5") }, model.ErrAmountNotPositive},
		{"a discount finer than the instrument", func(p *model.ExpenseParams) { p.Lines[0].Discount = decPtr("5.001") }, model.ErrTooManyDecimals},
		{"a system category in a line", func(p *model.ExpenseParams) {
			p.Lines[0].Category = unrecordedCategory(model.DirectionExpense)
		}, model.ErrSystemCategory},
		{"an archived account that paid", func(p *model.ExpenseParams) {
			archived := archivedAccount(cash)
			p.Account = &archived
		}, model.ErrArchived},
		// Both are valid but for the instrument: a dealing is counted in a currency.
		{"shares in an instrument that is not a currency", func(p *model.ExpenseParams) {
			p.Instrument, p.BaseCurrency = btc, twd
			p.Lines[0].BaseAmount = decPtr("9000000")
		}, model.ErrNotCurrency},
		{"a party that paid, in an instrument that is not a currency", func(p *model.ExpenseParams) {
			p.Account, p.PaidBy, p.Shares = nil, &friend, nil
			p.Instrument, p.BaseCurrency = btc, twd
			p.Lines[0].BaseAmount = decPtr("9000000")
		}, model.ErrNotCurrency},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := valid()
			tt.mutate(&p)

			txn, err := model.NewExpense(p)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if txn.ID != uuid.Nil || len(txn.Postings) != 0 {
				t.Errorf("transaction = %+v, want nothing made", txn)
			}
		})
	}
}

func TestNewExpenseWithOnlySharesPointsToTheDealing(t *testing.T) {
	cash := newCash(t, "現金")
	twd := newTWD(t)

	_, err := model.NewExpense(model.ExpenseParams{
		Header:     header(testKey),
		Account:    &cash,
		Instrument: twd,
		Shares:     []model.Share{{PartyID: newParty(), Amount: dec("300")}},
	})

	if !errors.Is(err, model.ErrNoLines) || !strings.Contains(err.Error(), "dealing") {
		t.Fatalf("err = %v, want ErrNoLines that says it is a dealing", err)
	}
}

func TestNewExpenseNamesTheShareThatIsWrong(t *testing.T) {
	cash := newCash(t, "現金")
	food := topLevel(t, model.DirectionExpense, "飲食")
	twd := newTWD(t)

	_, err := model.NewExpense(model.ExpenseParams{
		Header:     header(testKey),
		Account:    &cash,
		Instrument: twd,
		Lines:      []model.CategoryLine{{Category: food, Amount: dec("300")}},
		Shares: []model.Share{
			{PartyID: newParty(), Amount: dec("100")},
			{PartyID: newParty(), Amount: dec("-5")},
		},
	})

	if err == nil || err.Error() != "share 2: the amount must be greater than zero: got -5" {
		t.Fatalf("err = %v, want it to say which share and what was wrong", err)
	}
}

func TestNewIncomeRefusesADiscountAndASystemCategory(t *testing.T) {
	bank := newCash(t, "薪轉戶")
	salary := topLevel(t, model.DirectionIncome, "薪水")
	twd := newTWD(t)
	valid := func() model.IncomeParams {
		return model.IncomeParams{
			Header: header(testKey), Account: bank, Instrument: twd,
			Lines: []model.CategoryLine{{Category: salary, Amount: dec("50000")}},
		}
	}

	tests := []struct {
		name   string
		mutate func(p *model.IncomeParams)
		want   error
	}{
		{"a discount, which only an expense has", func(p *model.IncomeParams) { p.Lines[0].Discount = decPtr("100") }, model.ErrDiscountNotAllowed},
		{"a system category", func(p *model.IncomeParams) {
			p.Lines[0].Category = unrecordedCategory(model.DirectionIncome)
		}, model.ErrSystemCategory},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := valid()
			tt.mutate(&p)

			if _, err := model.NewIncome(p); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}
