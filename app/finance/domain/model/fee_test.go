package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

func TestNewTransferWithAFeeHasThePostingsOfTheFeeAfterItsOwn(t *testing.T) {
	a := newCash(t, "A")
	b := newCash(t, "B")
	feeCategory := topLevel(t, model.DirectionExpense, "手續費")
	twd := newTWD(t)
	names := map[uuid.UUID]string{a.ID: "A", b.ID: "B", feeCategory.ID: "手續費", twd.ID: "TWD"}

	// 1,000 from A to B, with a fee of 15 paid from A.
	txn, err := model.NewTransfer(model.TransferParams{
		Header: header(testKey), From: a, To: b, Instrument: twd, BaseCurrency: twd, Amount: dec("1000"),
		Fees: []model.Fee{{Account: a, Instrument: twd, Category: feeCategory, Amount: dec("15"), Memo: " 跨行 "}},
	})
	if err != nil {
		t.Fatalf("NewTransfer: %v", err)
	}

	// The fee is a pair of postings of its own. It is not added to the posting of A
	// that moves the 1,000, though both are of the same account and instrument.
	wantPostings(t, txn, names,
		"account(A) -1000 TWD",
		"account(B) +1000 TWD",
		"account(A) -15 TWD",
		"expense(手續費) +15 TWD, base +15",
	)
	if txn.Kind != model.KindTransfer {
		t.Errorf("kind = %q, want a transfer: the fee is part of it", txn.Kind)
	}
	if txn.Postings[3].Memo != "跨行" {
		t.Errorf("memo of the fee = %q, want it trimmed", txn.Postings[3].Memo)
	}
}

func TestEveryKindThatTakesFeesPutsThemAfterItsOwnPostings(t *testing.T) {
	bank := newCash(t, "銀行")
	broker := newCash(t, "券商")
	feeCategory := topLevel(t, model.DirectionExpense, "手續費")
	salary := topLevel(t, model.DirectionIncome, "薪水")
	twd := newTWD(t)
	usd := newCurrency(t, "USD", 2)
	company := newParty()
	names := map[uuid.UUID]string{
		bank.ID: "銀行", broker.ID: "券商", feeCategory.ID: "手續費", salary.ID: "薪水",
		twd.ID: "TWD", usd.ID: "USD", company: "公司",
	}
	twdFee := model.Fee{Account: bank, Instrument: twd, Category: feeCategory, Amount: dec("15")}

	tests := []struct {
		name     string
		make     func() (model.Transaction, error)
		kind     model.Kind
		postings []string
	}{
		{
			name: "an income",
			make: func() (model.Transaction, error) {
				return model.NewIncome(model.IncomeParams{
					Header: header(testKey), Account: bank, Instrument: twd,
					Lines: []model.CategoryLine{{Category: salary, Amount: dec("50000")}},
					Fees:  []model.Fee{twdFee},
				})
			},
			kind: model.KindIncome,
			postings: []string{
				"account(銀行) +50000 TWD", "income(薪水) -50000 TWD, base -50000",
				"account(銀行) -15 TWD", "expense(手續費) +15 TWD, base +15",
			},
		},
		{
			name: "an exchange, with a fee in the currency given up",
			make: func() (model.Transaction, error) {
				return model.NewExchange(model.ExchangeParams{
					Header: header(testKey), FromAccount: broker, ToAccount: bank, FromInstrument: usd, ToInstrument: twd,
					FromAmount: dec("3000"), ToAmount: dec("93000"), BaseCurrency: twd,
					Fees: []model.Fee{{Account: broker, Instrument: usd, Category: feeCategory, Amount: dec("1"), BaseAmount: decPtr("32")}},
				})
			},
			kind: model.KindExchange,
			postings: []string{
				"account(券商) -3000 USD", "conversion +3000 USD", "conversion -93000 TWD", "account(銀行) +93000 TWD",
				"account(券商) -1 USD", "expense(手續費) +1 USD, base +32",
			},
		},
		{
			name: "a dealing that pays out",
			make: func() (model.Transaction, error) {
				return model.NewDealing(model.DealingParams{
					Header: header(testKey), Account: bank, PartyID: company, Instrument: twd,
					Amount: dec("5000"), Direction: model.DealingPaidOut, Fees: []model.Fee{twdFee},
				})
			},
			kind: model.KindDealing,
			postings: []string{
				"account(銀行) -5000 TWD", "dealing(公司) +5000 TWD",
				"account(銀行) -15 TWD", "expense(手續費) +15 TWD, base +15",
			},
		},
		{
			name: "a dealing that receives",
			make: func() (model.Transaction, error) {
				return model.NewDealing(model.DealingParams{
					Header: header(testKey), Account: bank, PartyID: company, Instrument: twd,
					Amount: dec("5000"), Direction: model.DealingReceived, Fees: []model.Fee{twdFee},
				})
			},
			kind: model.KindDealing,
			postings: []string{
				"account(銀行) +5000 TWD", "dealing(公司) -5000 TWD",
				"account(銀行) -15 TWD", "expense(手續費) +15 TWD, base +15",
			},
		},
		{
			name: "a reward",
			make: func() (model.Transaction, error) {
				return model.NewReward(model.RewardParams{
					Header: header(testKey), Account: bank, Instrument: twd, Amount: dec("50"), Fees: []model.Fee{twdFee},
				})
			},
			kind: model.KindReward,
			postings: []string{
				"account(銀行) +50 TWD", "reward -50 TWD, base -50",
				"account(銀行) -15 TWD", "expense(手續費) +15 TWD, base +15",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txn, err := tt.make()
			if err != nil {
				t.Fatalf("construct: %v", err)
			}

			wantPostings(t, txn, names, tt.postings...)
			if txn.Kind != tt.kind {
				t.Errorf("kind = %q, want %q: a fee does not change the kind", txn.Kind, tt.kind)
			}
		})
	}
}

func TestAFeeNeedNotBeOfTheAccountOrTheInstrumentOfTheTransaction(t *testing.T) {
	bank := newCash(t, "銀行")
	card := newCash(t, "信用卡")
	wallet := newCash(t, "錢包")
	feeCategory := topLevel(t, model.DirectionExpense, "手續費")
	twd := newTWD(t)
	usd := newCurrency(t, "USD", 2)
	names := map[uuid.UUID]string{
		bank.ID: "銀行", card.ID: "卡", wallet.ID: "錢包", feeCategory.ID: "手續費", twd.ID: "TWD", usd.ID: "USD",
	}

	// A transfer in TWD with two fees: one in TWD from the receiving account, one
	// in USD from a wallet, with the base amount of the day.
	txn, err := model.NewTransfer(model.TransferParams{
		Header: header(testKey), From: bank, To: card, Instrument: twd, BaseCurrency: twd, Amount: dec("3000"),
		Fees: []model.Fee{
			{Account: card, Instrument: twd, Category: feeCategory, Amount: dec("10")},
			{Account: wallet, Instrument: usd, Category: feeCategory, Amount: dec("1.5"), BaseAmount: decPtr("48")},
		},
	})
	if err != nil {
		t.Fatalf("NewTransfer: %v", err)
	}

	wantPostings(t, txn, names,
		"account(銀行) -3000 TWD",
		"account(卡) +3000 TWD",
		"account(卡) -10 TWD",
		"expense(手續費) +10 TWD, base +10",
		"account(錢包) -1.5 USD",
		"expense(手續費) +1.5 USD, base +48",
	)
}

// A fee is told from the postings of the transaction by what it is: an expense
// and the account posting that pays it. When the fee is as much as what moves,
// the account postings that could be the payer are alike, and it is the one the
// constructor put last that is.
func TestAFeeAsBigAsTheAmountMovedStillLeavesTheTransferWhole(t *testing.T) {
	a := newCash(t, "A")
	b := newCash(t, "B")
	feeCategory := topLevel(t, model.DirectionExpense, "手續費")
	twd := newTWD(t)
	names := map[uuid.UUID]string{a.ID: "A", b.ID: "B", feeCategory.ID: "手續費", twd.ID: "TWD"}

	tests := []struct {
		name     string
		payer    model.Account
		postings []string
	}{
		{"paid from the receiving account", b, []string{
			"account(A) -15 TWD", "account(B) +15 TWD", "account(B) -15 TWD", "expense(手續費) +15 TWD, base +15",
		}},
		{"paid from the sending account", a, []string{
			"account(A) -15 TWD", "account(B) +15 TWD", "account(A) -15 TWD", "expense(手續費) +15 TWD, base +15",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txn, err := model.NewTransfer(model.TransferParams{
				Header: header(testKey), From: a, To: b, Instrument: twd, Amount: dec("15"),
				Fees: []model.Fee{{Account: tt.payer, Instrument: twd, Category: feeCategory, Amount: dec("15")}},
			})
			if err != nil {
				t.Fatalf("NewTransfer: %v", err)
			}

			wantPostings(t, txn, names, tt.postings...)
		})
	}
}

func TestAFeeIsRefusedWhenItIsNotAnExpenseThatCanBePaid(t *testing.T) {
	a := newCash(t, "A")
	b := newCash(t, "B")
	feeCategory := topLevel(t, model.DirectionExpense, "手續費")
	salary := topLevel(t, model.DirectionIncome, "薪水")
	twd := newTWD(t)
	usd := newCurrency(t, "USD", 2)
	valid := func() model.TransferParams {
		return model.TransferParams{
			Header: header(testKey), From: a, To: b, Instrument: twd, BaseCurrency: twd, Amount: dec("1000"),
			Fees: []model.Fee{{Account: a, Instrument: twd, Category: feeCategory, Amount: dec("15")}},
		}
	}

	tests := []struct {
		name   string
		mutate func(f *model.Fee)
		want   error
	}{
		{"a category of income", func(f *model.Fee) { f.Category = salary }, model.ErrWrongCategoryDirection},
		{"an archived category", func(f *model.Fee) { f.Category = archivedCategory(feeCategory) }, model.ErrArchived},
		{"a system category", func(f *model.Fee) { f.Category = unrecordedCategory(model.DirectionExpense) }, model.ErrSystemCategory},
		{"an archived account", func(f *model.Fee) { f.Account = archivedAccount(a) }, model.ErrArchived},
		{"an archived instrument", func(f *model.Fee) { f.Instrument = archivedInstrument(twd) }, model.ErrArchived},
		{"a zero amount", func(f *model.Fee) { f.Amount = dec("0") }, model.ErrAmountNotPositive},
		{"a negative amount", func(f *model.Fee) { f.Amount = dec("-15") }, model.ErrAmountNotPositive},
		{"more decimals than the instrument has", func(f *model.Fee) { f.Amount = dec("15.005") }, model.ErrTooManyDecimals},
		{"no base amount for a fee in a foreign currency", func(f *model.Fee) { f.Instrument = usd }, model.ErrBaseAmountRequired},
		{"a zero base amount for a fee in a foreign currency", func(f *model.Fee) {
			f.Instrument = usd
			f.BaseAmount = decPtr("0")
		}, model.ErrBaseAmountNotPositive},
		{"a base amount that is not the amount, for the base currency", func(f *model.Fee) { f.BaseAmount = decPtr("16") }, model.ErrBaseAmountMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := valid()
			tt.mutate(&p.Fees[0])

			txn, err := model.NewTransfer(p)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if txn.ID != uuid.Nil || len(txn.Postings) != 0 {
				t.Errorf("transaction = %+v, want nothing made", txn)
			}
		})
	}
}

func TestAFeeOfAnIncomeIsRefusedForItsCategoryToo(t *testing.T) {
	bank := newCash(t, "銀行")
	salary := topLevel(t, model.DirectionIncome, "薪水")
	twd := newTWD(t)

	_, err := model.NewIncome(model.IncomeParams{
		Header: header(testKey), Account: bank, Instrument: twd,
		Lines: []model.CategoryLine{{Category: salary, Amount: dec("50000")}},
		Fees:  []model.Fee{{Account: bank, Instrument: twd, Category: salary, Amount: dec("15")}},
	})

	if !errors.Is(err, model.ErrWrongCategoryDirection) {
		t.Fatalf("err = %v, want ErrWrongCategoryDirection: a fee is an expense", err)
	}
}

func TestAFeeThatIsWrongIsNamed(t *testing.T) {
	a := newCash(t, "A")
	b := newCash(t, "B")
	feeCategory := topLevel(t, model.DirectionExpense, "手續費")
	twd := newTWD(t)

	_, err := model.NewTransfer(model.TransferParams{
		Header: header(testKey), From: a, To: b, Instrument: twd, Amount: dec("1000"),
		Fees: []model.Fee{
			{Account: a, Instrument: twd, Category: feeCategory, Amount: dec("15")},
			{Account: a, Instrument: twd, Category: feeCategory, Amount: dec("-1")},
		},
	})

	if err == nil || !strings.HasPrefix(err.Error(), "fee 2: ") {
		t.Fatalf("err = %v, want it to say it is fee 2", err)
	}
}
