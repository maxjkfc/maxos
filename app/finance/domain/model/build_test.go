package model_test

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// testKey is a valid idempotency key.
const testKey = "lunch-20261114-01"

// taipei is a zone ahead of UTC, so that a time just after midnight there is
// still the day before in UTC.
var taipei = time.FixedZone("UTC+8", 8*60*60)

func dec(s string) decimal.Decimal {
	return decimal.RequireFromString(s)
}

func decPtr(s string) *decimal.Decimal {
	d := dec(s)
	return &d
}

// header returns the header of a transaction made on 14 November 2026.
func header(key string) model.Header {
	return model.Header{
		ID:             uuid.Must(uuid.NewV7()),
		IdempotencyKey: key,
		OccurredOn:     time.Date(2026, 11, 14, 0, 30, 0, 0, taipei),
	}
}

func newCash(t *testing.T, name string) model.Account {
	t.Helper()
	a, err := model.NewAccount(model.NewAccountParams{ID: uuid.Must(uuid.NewV7()), Name: name, Kind: model.AccountKindCash})
	if err != nil {
		t.Fatalf("NewAccount(%q): %v", name, err)
	}
	return a
}

// newCurrency returns a currency in use with the given decimals.
func newCurrency(t *testing.T, code string, decimals int) model.Instrument {
	t.Helper()
	i, err := model.NewInstrument(model.NewInstrumentParams{
		ID:         uuid.Must(uuid.NewV7()),
		Code:       code,
		Name:       code,
		Kind:       model.InstrumentKindCurrency,
		IsCurrency: true,
		Decimals:   decimals,
	})
	if err != nil {
		t.Fatalf("NewInstrument(%q): %v", code, err)
	}
	return i
}

// newTWD returns the base currency as migration 0003 seeds it.
func newTWD(t *testing.T) model.Instrument {
	t.Helper()
	i := newCurrency(t, model.BaseCurrencyCode, 2)
	i.ID = model.BaseCurrencyID
	return i
}

// newNonCurrency returns an instrument that is not a currency.
func newNonCurrency(t *testing.T, code string, kind model.InstrumentKind, decimals int) model.Instrument {
	t.Helper()
	i, err := model.NewInstrument(model.NewInstrumentParams{
		ID: uuid.Must(uuid.NewV7()), Code: code, Name: code, Kind: kind, Decimals: decimals,
	})
	if err != nil {
		t.Fatalf("NewInstrument(%q): %v", code, err)
	}
	return i
}

func archivedAccount(a model.Account) model.Account {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a.ArchivedAt = &at
	return a
}

func archivedInstrument(i model.Instrument) model.Instrument {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	i.ArchivedAt = &at
	return i
}

func archivedCategory(c model.Category) model.Category {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.ArchivedAt = &at
	return c
}

func signed(d decimal.Decimal) string {
	if d.IsPositive() {
		return "+" + d.String()
	}
	return d.String()
}

// describe writes the postings of a transaction the way the design table does:
// book, what it is made to, quantity and instrument, and the base amount when
// there is one. names says what to call each id.
func describe(txn model.Transaction, names map[uuid.UUID]string) []string {
	out := make([]string, 0, len(txn.Postings))
	for _, p := range txn.Postings {
		target := string(p.Book)
		switch {
		case p.AccountID != nil:
			target += "(" + names[*p.AccountID] + ")"
		case p.CategoryID != nil:
			target += "(" + names[*p.CategoryID] + ")"
		}
		line := fmt.Sprintf("%s %s %s", target, signed(p.Quantity), names[p.InstrumentID])
		if p.BaseAmount != nil {
			line += ", base " + signed(*p.BaseAmount)
		}
		out = append(out, line)
	}
	return out
}

func wantPostings(t *testing.T, txn model.Transaction, names map[uuid.UUID]string, want ...string) {
	t.Helper()
	got := describe(txn, names)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("postings:\n got %q\nwant %q", got, want)
	}
	for i, p := range txn.Postings {
		if p.Seq != i+1 {
			t.Errorf("posting %d has seq %d, want %d", i+1, p.Seq, i+1)
		}
	}
	if err := txn.Validate(); err != nil {
		t.Errorf("Validate: %v, want the constructor's result to be valid", err)
	}
}

func TestNewExpenseInTheBaseCurrency(t *testing.T) {
	cash := newCash(t, "現金")
	food := topLevel(t, model.DirectionExpense, "飲食")
	twd := newTWD(t)
	names := map[uuid.UUID]string{cash.ID: "現金", food.ID: "飲食", twd.ID: "TWD"}

	txn, err := model.NewExpense(model.CategorizedParams{
		Header:     header(testKey),
		Account:    cash,
		Instrument: twd,
		Lines:      []model.CategoryLine{{Category: food, Amount: dec("120")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}

	wantPostings(t, txn, names,
		"account(現金) -120 TWD",
		"expense(飲食) +120 TWD, base +120",
	)
	if txn.Kind != model.KindExpense || txn.Status != model.StatusDraft {
		t.Errorf("kind %q, status %q, want an expense that is a draft", txn.Kind, txn.Status)
	}
}

func TestNewExpenseInAForeignCurrencyKeepsTheBaseAmountOfTheCaller(t *testing.T) {
	card := newCash(t, "Ether.fi 卡")
	food := topLevel(t, model.DirectionExpense, "飲食")
	usd := newCurrency(t, "USD", 2)
	twd := newTWD(t)
	names := map[uuid.UUID]string{card.ID: "卡", food.ID: "飲食", usd.ID: "USD", twd.ID: "TWD"}

	txn, err := model.NewExpense(model.CategorizedParams{
		Header:       header(testKey),
		Account:      card,
		Instrument:   usd,
		BaseCurrency: twd,
		Lines:        []model.CategoryLine{{Category: food, Amount: dec("100"), BaseAmount: decPtr("3200")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}

	wantPostings(t, txn, names,
		"account(卡) -100 USD",
		"expense(飲食) +100 USD, base +3200",
	)
}

func TestNewExpenseSplitsAPaymentAmongCategories(t *testing.T) {
	cash := newCash(t, "現金")
	groceries := topLevel(t, model.DirectionExpense, "食材")
	household := topLevel(t, model.DirectionExpense, "日用品")
	twd := newTWD(t)
	names := map[uuid.UUID]string{cash.ID: "現金", groceries.ID: "食材", household.ID: "日用品", twd.ID: "TWD"}

	txn, err := model.NewExpense(model.CategorizedParams{
		Header:     header(testKey),
		Account:    cash,
		Instrument: twd,
		Lines: []model.CategoryLine{
			{Category: groceries, Amount: dec("300"), Memo: " 蔬菜 "},
			{Category: household, Amount: dec("200")},
		},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}

	wantPostings(t, txn, names,
		"account(現金) -500 TWD",
		"expense(食材) +300 TWD, base +300",
		"expense(日用品) +200 TWD, base +200",
	)
	if txn.Postings[1].Memo != "蔬菜" {
		t.Errorf("memo = %q, want it trimmed", txn.Postings[1].Memo)
	}
}

func TestNewExpenseSplitInAForeignCurrencyNeedsABaseAmountPerLine(t *testing.T) {
	card := newCash(t, "卡")
	groceries := topLevel(t, model.DirectionExpense, "食材")
	household := topLevel(t, model.DirectionExpense, "日用品")
	usd := newCurrency(t, "USD", 2)
	twd := newTWD(t)
	names := map[uuid.UUID]string{card.ID: "卡", groceries.ID: "食材", household.ID: "日用品", usd.ID: "USD", twd.ID: "TWD"}
	params := model.CategorizedParams{
		Header:       header(testKey),
		Account:      card,
		Instrument:   usd,
		BaseCurrency: twd,
		Lines: []model.CategoryLine{
			{Category: groceries, Amount: dec("60"), BaseAmount: decPtr("1920")},
			{Category: household, Amount: dec("40"), BaseAmount: decPtr("1280")},
		},
	}

	txn, err := model.NewExpense(params)
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	wantPostings(t, txn, names,
		"account(卡) -100 USD",
		"expense(食材) +60 USD, base +1920",
		"expense(日用品) +40 USD, base +1280",
	)

	params.Lines[1].BaseAmount = nil
	_, err = model.NewExpense(params)
	if !errors.Is(err, model.ErrBaseAmountRequired) {
		t.Fatalf("err = %v, want ErrBaseAmountRequired for the line without one", err)
	}
}

func TestNewIncome(t *testing.T) {
	bank := newCash(t, "薪轉戶")
	salary := topLevel(t, model.DirectionIncome, "薪水")
	twd := newTWD(t)
	names := map[uuid.UUID]string{bank.ID: "薪轉戶", salary.ID: "薪水", twd.ID: "TWD"}

	txn, err := model.NewIncome(model.CategorizedParams{
		Header:     header(testKey),
		Account:    bank,
		Instrument: twd,
		Lines:      []model.CategoryLine{{Category: salary, Amount: dec("50000")}},
	})
	if err != nil {
		t.Fatalf("NewIncome: %v", err)
	}

	wantPostings(t, txn, names,
		"account(薪轉戶) +50000 TWD",
		"income(薪水) -50000 TWD, base -50000",
	)
	if txn.Kind != model.KindIncome {
		t.Errorf("kind = %q, want income", txn.Kind)
	}
}

func TestNewIncomeInAForeignCurrency(t *testing.T) {
	wallet := newCash(t, "錢包")
	salary := topLevel(t, model.DirectionIncome, "薪水")
	usd := newCurrency(t, "USD", 2)
	twd := newTWD(t)
	names := map[uuid.UUID]string{wallet.ID: "錢包", salary.ID: "薪水", usd.ID: "USD", twd.ID: "TWD"}

	txn, err := model.NewIncome(model.CategorizedParams{
		Header:       header(testKey),
		Account:      wallet,
		Instrument:   usd,
		BaseCurrency: twd,
		Lines:        []model.CategoryLine{{Category: salary, Amount: dec("1000"), BaseAmount: decPtr("32000")}},
	})
	if err != nil {
		t.Fatalf("NewIncome: %v", err)
	}

	// The base amount is given as a positive amount and takes the sign of its posting.
	wantPostings(t, txn, names,
		"account(錢包) +1000 USD",
		"income(薪水) -1000 USD, base -32000",
	)
}

func TestNewTransfer(t *testing.T) {
	bank := newCash(t, "銀行")
	card := newCash(t, "信用卡")
	twd := newTWD(t)
	names := map[uuid.UUID]string{bank.ID: "銀行", card.ID: "卡", twd.ID: "TWD"}

	txn, err := model.NewTransfer(model.TransferParams{
		Header: header(testKey), From: bank, To: card, Instrument: twd, Amount: dec("3000"),
	})
	if err != nil {
		t.Fatalf("NewTransfer: %v", err)
	}

	wantPostings(t, txn, names,
		"account(銀行) -3000 TWD",
		"account(卡) +3000 TWD",
	)
	if txn.Kind != model.KindTransfer {
		t.Errorf("kind = %q, want transfer", txn.Kind)
	}
}

func TestNewExchangeGivesEachInstrumentItsOwnConversionPosting(t *testing.T) {
	bank := newCash(t, "銀行")
	broker := newCash(t, "券商")
	usd := newCurrency(t, "USD", 2)
	twd := newTWD(t)
	names := map[uuid.UUID]string{bank.ID: "銀行", broker.ID: "券商", usd.ID: "USD", twd.ID: "TWD"}

	txn, err := model.NewExchange(model.ExchangeParams{
		Header:         header(testKey),
		FromAccount:    broker,
		ToAccount:      bank,
		FromInstrument: usd,
		ToInstrument:   twd,
		FromAmount:     dec("3000"),
		ToAmount:       dec("93000"),
	})
	if err != nil {
		t.Fatalf("NewExchange: %v", err)
	}

	wantPostings(t, txn, names,
		"account(券商) -3000 USD",
		"conversion +3000 USD",
		"conversion -93000 TWD",
		"account(銀行) +93000 TWD",
	)
	if txn.Kind != model.KindExchange {
		t.Errorf("kind = %q, want exchange", txn.Kind)
	}
}

func TestNewExchangeWithinOneAccount(t *testing.T) {
	bank := newCash(t, "銀行")
	usd := newCurrency(t, "USD", 2)
	twd := newTWD(t)

	_, err := model.NewExchange(model.ExchangeParams{
		Header:         header(testKey),
		FromAccount:    bank,
		ToAccount:      bank,
		FromInstrument: twd,
		ToInstrument:   usd,
		FromAmount:     dec("32000"),
		ToAmount:       dec("1000"),
	})
	if err != nil {
		t.Fatalf("NewExchange within one account: %v, want it allowed: an account holds many instruments", err)
	}
}

func TestNewExchangeAcceptsACryptoCurrencyAndRefusesWhatIsNotACurrency(t *testing.T) {
	bank := newCash(t, "交易所")
	twd := newTWD(t)
	usdt, err := model.NewInstrument(model.NewInstrumentParams{
		ID: uuid.Must(uuid.NewV7()), Code: "USDT", Name: "Tether", Kind: model.InstrumentKindCrypto, IsCurrency: true, Decimals: 6,
	})
	if err != nil {
		t.Fatalf("NewInstrument: %v", err)
	}
	btc := newNonCurrency(t, "BTC", model.InstrumentKindCrypto, 8)
	stock := newNonCurrency(t, "2330.TW", model.InstrumentKindStock, 0)

	exchange := func(from, to model.Instrument, fromAmount, toAmount string) error {
		_, err := model.NewExchange(model.ExchangeParams{
			Header: header(testKey), FromAccount: bank, ToAccount: bank,
			FromInstrument: from, ToInstrument: to, FromAmount: dec(fromAmount), ToAmount: dec(toAmount),
		})
		return err
	}

	if err := exchange(twd, usdt, "3200", "100"); err != nil {
		t.Errorf("TWD to USDT: %v, want a crypto currency accepted", err)
	}
	for _, tt := range []struct {
		name     string
		from, to model.Instrument
	}{
		{"a crypto that is not a currency, received", twd, btc},
		{"a crypto that is not a currency, given up", btc, twd},
		{"a stock", twd, stock},
	} {
		if err := exchange(tt.from, tt.to, "1", "1"); !errors.Is(err, model.ErrNotCurrency) {
			t.Errorf("%s: err = %v, want ErrNotCurrency", tt.name, err)
		}
	}
}

func TestNewOpeningBalance(t *testing.T) {
	cash := newCash(t, "現金")
	card := newCash(t, "信用卡")
	twd := newTWD(t)
	names := map[uuid.UUID]string{cash.ID: "現金", card.ID: "卡", twd.ID: "TWD"}

	held, err := model.NewOpeningBalance(model.OpeningBalanceParams{
		Header: header(testKey), Account: cash, Instrument: twd, Amount: dec("1000"),
	})
	if err != nil {
		t.Fatalf("NewOpeningBalance: %v", err)
	}
	wantPostings(t, held, names,
		"account(現金) +1000 TWD",
		"opening -1000 TWD",
	)
	if held.Kind != model.KindOpening {
		t.Errorf("kind = %q, want opening", held.Kind)
	}

	owed, err := model.NewOpeningBalance(model.OpeningBalanceParams{
		Header: header(testKey), Account: card, Instrument: twd, Amount: dec("-5000"),
	})
	if err != nil {
		t.Fatalf("NewOpeningBalance of an account that owes: %v", err)
	}
	wantPostings(t, owed, names,
		"account(卡) -5000 TWD",
		"opening +5000 TWD",
	)
}

func TestConstructorsKeepTheHeaderTidy(t *testing.T) {
	cash := newCash(t, "現金")
	food := topLevel(t, model.DirectionExpense, "飲食")
	twd := newTWD(t)
	first, second := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if bytes.Compare(first[:], second[:]) > 0 {
		first, second = second, first
	}
	project, area, inbox := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	at := time.Date(2026, 11, 14, 0, 31, 7, 0, taipei)

	h := header("  " + testKey + "  ")
	h.Description = "  午餐  "
	h.Merchant = " 便當店\n"
	h.OccurredAt = &at
	h.ProjectID, h.AreaID, h.InboxItemID = &project, &area, &inbox
	h.TagIDs = []uuid.UUID{second, first, second}
	txn, err := model.NewExpense(model.CategorizedParams{
		Header:     h,
		Account:    cash,
		Instrument: twd,
		Lines:      []model.CategoryLine{{Category: food, Amount: dec("120")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}

	wantDay := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	if txn.ID != h.ID || txn.IdempotencyKey != testKey || txn.Description != "午餐" || txn.Merchant != "便當店" {
		t.Errorf("transaction = %+v, want the id, the trimmed key, description and merchant", txn)
	}
	if !txn.OccurredOn.Equal(wantDay) {
		t.Errorf("OccurredOn = %v, want the calendar day the time shows, %v", txn.OccurredOn, wantDay)
	}
	if txn.OccurredAt == nil || !txn.OccurredAt.Equal(at) {
		t.Errorf("OccurredAt = %v, want %v", txn.OccurredAt, at)
	}
	if txn.ProjectID == nil || *txn.ProjectID != project || txn.AreaID == nil || *txn.AreaID != area || txn.InboxItemID == nil || *txn.InboxItemID != inbox {
		t.Errorf("project %v, area %v, inbox item %v, want %s, %s, %s", txn.ProjectID, txn.AreaID, txn.InboxItemID, project, area, inbox)
	}
	if fmt.Sprint(txn.TagIDs) != fmt.Sprint([]uuid.UUID{first, second}) {
		t.Errorf("tags = %v, want %v, once each and in order", txn.TagIDs, []uuid.UUID{first, second})
	}
}

func TestConstructorsRefuseABadHeader(t *testing.T) {
	cash := newCash(t, "現金")
	food := topLevel(t, model.DirectionExpense, "飲食")
	twd := newTWD(t)
	expense := func(h model.Header) error {
		_, err := model.NewExpense(model.CategorizedParams{
			Header: h, Account: cash, Instrument: twd, Lines: []model.CategoryLine{{Category: food, Amount: dec("1")}},
		})
		return err
	}

	for _, key := range []string{"", "   ", "short", " 1234567 "} {
		var keyErr *model.IdempotencyKeyLengthError
		if err := expense(header(key)); !errors.As(err, &keyErr) {
			t.Errorf("key %q: err = %v, want an *IdempotencyKeyLengthError", key, err)
		}
	}
	var keyErr *model.IdempotencyKeyLengthError
	long := make([]byte, model.MaxKeyRunes+1)
	for i := range long {
		long[i] = 'k'
	}
	if err := expense(header(string(long))); !errors.As(err, &keyErr) || keyErr.Runes != model.MaxKeyRunes+1 {
		t.Errorf("a key of %d characters: err = %v, want an *IdempotencyKeyLengthError that counts them", len(long), err)
	}
	if err := expense(header(string(long[:model.MaxKeyRunes]))); err != nil {
		t.Errorf("a key of %d characters: %v, want it accepted", model.MaxKeyRunes, err)
	}

	noDate := header(testKey)
	noDate.OccurredOn = time.Time{}
	if err := expense(noDate); !errors.Is(err, model.ErrDateRequired) {
		t.Errorf("no date: err = %v, want ErrDateRequired", err)
	}
}

func TestNewExpenseRefuses(t *testing.T) {
	cash := newCash(t, "現金")
	food := topLevel(t, model.DirectionExpense, "飲食")
	salary := topLevel(t, model.DirectionIncome, "薪水")
	usd := newCurrency(t, "USD", 2)
	jpy := newCurrency(t, "JPY", 0)
	twd := newTWD(t)
	valid := func() model.CategorizedParams {
		return model.CategorizedParams{
			Header:       header(testKey),
			Account:      cash,
			Instrument:   twd,
			BaseCurrency: twd,
			Lines:        []model.CategoryLine{{Category: food, Amount: dec("120")}},
		}
	}

	tests := []struct {
		name   string
		mutate func(p *model.CategorizedParams)
		want   error
	}{
		{"no lines", func(p *model.CategorizedParams) { p.Lines = nil }, model.ErrNoLines},
		{"a zero amount", func(p *model.CategorizedParams) { p.Lines[0].Amount = dec("0") }, model.ErrAmountNotPositive},
		{"a negative amount", func(p *model.CategorizedParams) { p.Lines[0].Amount = dec("-120") }, model.ErrAmountNotPositive},
		{"a second line that is zero", func(p *model.CategorizedParams) {
			p.Lines = append(p.Lines, model.CategoryLine{Category: food, Amount: dec("0")})
		}, model.ErrAmountNotPositive},
		{"more decimals than TWD has", func(p *model.CategorizedParams) { p.Lines[0].Amount = dec("120.005") }, model.ErrTooManyDecimals},
		{"more decimals than JPY has", func(p *model.CategorizedParams) {
			p.Instrument = jpy
			p.Lines[0].Amount = dec("100.5")
			p.Lines[0].BaseAmount = decPtr("30")
		}, model.ErrTooManyDecimals},
		{"a base amount that is not the amount, for the base currency", func(p *model.CategorizedParams) {
			p.Lines[0].BaseAmount = decPtr("121")
		}, model.ErrBaseAmountMismatch},
		{"no base amount for a foreign currency", func(p *model.CategorizedParams) { p.Instrument = usd }, model.ErrBaseAmountRequired},
		{"a zero base amount", func(p *model.CategorizedParams) {
			p.Instrument = usd
			p.Lines[0].BaseAmount = decPtr("0")
		}, model.ErrBaseAmountNotPositive},
		{"a negative base amount", func(p *model.CategorizedParams) {
			p.Instrument = usd
			p.Lines[0].BaseAmount = decPtr("-3200")
		}, model.ErrBaseAmountNotPositive},
		{"a base amount finer than TWD", func(p *model.CategorizedParams) {
			p.Instrument = usd
			p.Lines[0].BaseAmount = decPtr("3200.123")
		}, model.ErrTooManyDecimals},
		{"a category of the other direction", func(p *model.CategorizedParams) { p.Lines[0].Category = salary }, model.ErrWrongCategoryDirection},
		{"an archived category", func(p *model.CategorizedParams) { p.Lines[0].Category = archivedCategory(food) }, model.ErrArchived},
		{"an archived account", func(p *model.CategorizedParams) { p.Account = archivedAccount(cash) }, model.ErrArchived},
		{"an archived instrument", func(p *model.CategorizedParams) { p.Instrument = archivedInstrument(twd) }, model.ErrArchived},
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

func TestNewExpenseNamesTheLineThatIsWrong(t *testing.T) {
	cash := newCash(t, "現金")
	food := topLevel(t, model.DirectionExpense, "飲食")
	twd := newTWD(t)

	_, err := model.NewExpense(model.CategorizedParams{
		Header:     header(testKey),
		Account:    cash,
		Instrument: twd,
		Lines: []model.CategoryLine{
			{Category: food, Amount: dec("10")},
			{Category: food, Amount: dec("-5")},
		},
	})
	if err == nil || err.Error() != "line 2: the amount must be greater than zero: got -5" {
		t.Fatalf("err = %v, want it to say which line and what was wrong", err)
	}
}

func TestNewIncomeRefuses(t *testing.T) {
	bank := newCash(t, "薪轉戶")
	salary := topLevel(t, model.DirectionIncome, "薪水")
	food := topLevel(t, model.DirectionExpense, "飲食")
	twd := newTWD(t)
	valid := func() model.CategorizedParams {
		return model.CategorizedParams{
			Header:     header(testKey),
			Account:    bank,
			Instrument: twd,
			Lines:      []model.CategoryLine{{Category: salary, Amount: dec("50000")}},
		}
	}

	tests := []struct {
		name   string
		mutate func(p *model.CategorizedParams)
		want   error
	}{
		{"a category of expenses", func(p *model.CategorizedParams) { p.Lines[0].Category = food }, model.ErrWrongCategoryDirection},
		{"a negative amount", func(p *model.CategorizedParams) { p.Lines[0].Amount = dec("-50000") }, model.ErrAmountNotPositive},
		{"no lines", func(p *model.CategorizedParams) { p.Lines = nil }, model.ErrNoLines},
		{"an archived account", func(p *model.CategorizedParams) { p.Account = archivedAccount(bank) }, model.ErrArchived},
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

func TestNewTransferRefuses(t *testing.T) {
	bank := newCash(t, "銀行")
	card := newCash(t, "信用卡")
	twd := newTWD(t)
	valid := func() model.TransferParams {
		return model.TransferParams{Header: header(testKey), From: bank, To: card, Instrument: twd, Amount: dec("3000")}
	}

	tests := []struct {
		name   string
		mutate func(p *model.TransferParams)
		want   error
	}{
		{"the same account on both sides", func(p *model.TransferParams) { p.To = bank }, model.ErrSameAccount},
		{"a zero amount", func(p *model.TransferParams) { p.Amount = dec("0") }, model.ErrAmountNotPositive},
		{"a negative amount", func(p *model.TransferParams) { p.Amount = dec("-3000") }, model.ErrAmountNotPositive},
		{"more decimals than the instrument has", func(p *model.TransferParams) { p.Amount = dec("3000.001") }, model.ErrTooManyDecimals},
		{"an archived source", func(p *model.TransferParams) { p.From = archivedAccount(bank) }, model.ErrArchived},
		{"an archived destination", func(p *model.TransferParams) { p.To = archivedAccount(card) }, model.ErrArchived},
		{"an archived instrument", func(p *model.TransferParams) { p.Instrument = archivedInstrument(twd) }, model.ErrArchived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := valid()
			tt.mutate(&p)

			if _, err := model.NewTransfer(p); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNewTransferMovesAnInstrumentThatIsNotACurrency(t *testing.T) {
	wallet := newCash(t, "錢包")
	exchange := newCash(t, "交易所")
	btc := newNonCurrency(t, "BTC", model.InstrumentKindCrypto, 8)
	names := map[uuid.UUID]string{wallet.ID: "錢包", exchange.ID: "交易所", btc.ID: "BTC"}

	txn, err := model.NewTransfer(model.TransferParams{
		Header: header(testKey), From: exchange, To: wallet, Instrument: btc, Amount: dec("0.12345678"),
	})
	if err != nil {
		t.Fatalf("NewTransfer: %v, want any instrument to be movable between accounts", err)
	}
	wantPostings(t, txn, names,
		"account(交易所) -0.12345678 BTC",
		"account(錢包) +0.12345678 BTC",
	)
}

func TestNewExchangeRefuses(t *testing.T) {
	bank := newCash(t, "銀行")
	usd := newCurrency(t, "USD", 2)
	jpy := newCurrency(t, "JPY", 0)
	twd := newTWD(t)
	valid := func() model.ExchangeParams {
		return model.ExchangeParams{
			Header:         header(testKey),
			FromAccount:    bank,
			ToAccount:      bank,
			FromInstrument: usd,
			ToInstrument:   twd,
			FromAmount:     dec("3000"),
			ToAmount:       dec("93000"),
		}
	}

	tests := []struct {
		name   string
		mutate func(p *model.ExchangeParams)
		want   error
	}{
		{"the same currency on both sides", func(p *model.ExchangeParams) { p.ToInstrument = usd }, model.ErrSameInstrument},
		{"a zero amount given up", func(p *model.ExchangeParams) { p.FromAmount = dec("0") }, model.ErrAmountNotPositive},
		{"a negative amount received", func(p *model.ExchangeParams) { p.ToAmount = dec("-93000") }, model.ErrAmountNotPositive},
		{"more decimals than the currency given up has", func(p *model.ExchangeParams) {
			p.FromInstrument = jpy
			p.FromAmount = dec("100.5")
		}, model.ErrTooManyDecimals},
		{"more decimals than the currency received has", func(p *model.ExchangeParams) { p.ToAmount = dec("93000.001") }, model.ErrTooManyDecimals},
		{"an archived account", func(p *model.ExchangeParams) { p.ToAccount = archivedAccount(bank) }, model.ErrArchived},
		{"an archived currency", func(p *model.ExchangeParams) { p.FromInstrument = archivedInstrument(usd) }, model.ErrArchived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := valid()
			tt.mutate(&p)

			if _, err := model.NewExchange(p); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNewOpeningBalanceRefuses(t *testing.T) {
	cash := newCash(t, "現金")
	jpy := newCurrency(t, "JPY", 0)
	twd := newTWD(t)
	valid := func() model.OpeningBalanceParams {
		return model.OpeningBalanceParams{Header: header(testKey), Account: cash, Instrument: twd, Amount: dec("1000")}
	}

	tests := []struct {
		name   string
		mutate func(p *model.OpeningBalanceParams)
		want   error
	}{
		{"nothing", func(p *model.OpeningBalanceParams) { p.Amount = dec("0") }, model.ErrAmountZero},
		{"more decimals than the instrument has", func(p *model.OpeningBalanceParams) {
			p.Instrument = jpy
			p.Amount = dec("1000.5")
		}, model.ErrTooManyDecimals},
		{"an archived account", func(p *model.OpeningBalanceParams) { p.Account = archivedAccount(cash) }, model.ErrArchived},
		{"an archived instrument", func(p *model.OpeningBalanceParams) { p.Instrument = archivedInstrument(twd) }, model.ErrArchived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := valid()
			tt.mutate(&p)

			if _, err := model.NewOpeningBalance(p); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestAFineAmountOfAnInstrumentWithDecimalsIsAccepted(t *testing.T) {
	cash := newCash(t, "現金")
	food := topLevel(t, model.DirectionExpense, "飲食")
	twd := newTWD(t)

	for _, amount := range []string{"120", "120.5", "120.50", "0.01"} {
		_, err := model.NewExpense(model.CategorizedParams{
			Header:     header(testKey),
			Account:    cash,
			Instrument: twd,
			Lines:      []model.CategoryLine{{Category: food, Amount: dec(amount)}},
		})
		if err != nil {
			t.Errorf("amount %s: %v, want it accepted", amount, err)
		}
	}
}
