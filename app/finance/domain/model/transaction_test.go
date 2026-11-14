package model_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// The raw postings below skip the constructors on purpose: Validate is the
// check that holds for a transaction put together any other way.

var (
	acctA = uuid.Must(uuid.NewV7())
	acctB = uuid.Must(uuid.NewV7())
	catX  = uuid.Must(uuid.NewV7())
	usdID = uuid.Must(uuid.NewV7())
)

func accountPosting(account uuid.UUID, instrument uuid.UUID, quantity string) model.Posting {
	return model.Posting{Book: model.BookAccount, AccountID: &account, InstrumentID: instrument, Quantity: dec(quantity)}
}

func categoryPosting(book model.Book, instrument uuid.UUID, quantity, base string) model.Posting {
	category := catX
	return model.Posting{Book: book, CategoryID: &category, InstrumentID: instrument, Quantity: dec(quantity), BaseAmount: decPtr(base)}
}

func bookPosting(book model.Book, instrument uuid.UUID, quantity string) model.Posting {
	return model.Posting{Book: book, InstrumentID: instrument, Quantity: dec(quantity)}
}

// raw returns a draft of the kind made of the postings, numbered as the
// constructors do.
func raw(kind model.Kind, postings ...model.Posting) model.Transaction {
	for i := range postings {
		postings[i].Seq = i + 1
	}
	return model.Transaction{ID: uuid.Must(uuid.NewV7()), Kind: kind, Status: model.StatusDraft, Postings: postings}
}

func TestValidateAcceptsEveryKindMadeRight(t *testing.T) {
	twd := model.BaseCurrencyID
	tests := []struct {
		name string
		txn  model.Transaction
	}{
		{"an expense", raw(model.KindExpense,
			accountPosting(acctA, twd, "-120"),
			categoryPosting(model.BookExpense, twd, "120", "120"))},
		{"an expense split in two", raw(model.KindExpense,
			accountPosting(acctA, usdID, "-100"),
			categoryPosting(model.BookExpense, usdID, "60", "1920"),
			categoryPosting(model.BookExpense, usdID, "40", "1280"))},
		{"an income", raw(model.KindIncome,
			accountPosting(acctA, twd, "50000"),
			categoryPosting(model.BookIncome, twd, "-50000", "-50000"))},
		{"a transfer", raw(model.KindTransfer,
			accountPosting(acctA, twd, "-3000"),
			accountPosting(acctB, twd, "3000"))},
		{"an exchange", raw(model.KindExchange,
			accountPosting(acctA, usdID, "-3000"),
			bookPosting(model.BookConversion, usdID, "3000"),
			bookPosting(model.BookConversion, twd, "-93000"),
			accountPosting(acctB, twd, "93000"))},
		{"an exchange in the other order", raw(model.KindExchange,
			bookPosting(model.BookConversion, twd, "-93000"),
			accountPosting(acctB, twd, "93000"),
			accountPosting(acctA, usdID, "-3000"),
			bookPosting(model.BookConversion, usdID, "3000"))},
		{"an opening balance", raw(model.KindOpening,
			accountPosting(acctA, twd, "1000"),
			bookPosting(model.BookOpening, twd, "-1000"))},
		{"an opening balance of an account that owes", raw(model.KindOpening,
			accountPosting(acctA, twd, "-1000"),
			bookPosting(model.BookOpening, twd, "1000"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.txn.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestValidateRefuses(t *testing.T) {
	twd := model.BaseCurrencyID
	tests := []struct {
		name string
		txn  model.Transaction
		want error
	}{
		// The first invariant: each instrument adds up to zero.
		{"an expense that does not balance", raw(model.KindExpense,
			accountPosting(acctA, twd, "-120"),
			categoryPosting(model.BookExpense, twd, "100", "100")), model.ErrUnbalanced},
		{"an income that does not balance", raw(model.KindIncome,
			accountPosting(acctA, twd, "50000"),
			categoryPosting(model.BookIncome, twd, "-49999", "-49999")), model.ErrUnbalanced},
		{"a transfer that loses money", raw(model.KindTransfer,
			accountPosting(acctA, twd, "-3000"),
			accountPosting(acctB, twd, "2999")), model.ErrUnbalanced},
		{"a transfer that moves two instruments", raw(model.KindTransfer,
			accountPosting(acctA, twd, "-3000"),
			accountPosting(acctB, usdID, "3000")), model.ErrUnbalanced},
		{"an exchange whose conversion of one instrument does not mirror its account", raw(model.KindExchange,
			accountPosting(acctA, usdID, "-3000"),
			bookPosting(model.BookConversion, usdID, "2999"),
			bookPosting(model.BookConversion, twd, "-93000"),
			accountPosting(acctB, twd, "93000")), model.ErrUnbalanced},

		// The second: the sign is that of the thing the posting is made to.
		{"an expense that puts money into the account", raw(model.KindExpense,
			accountPosting(acctA, twd, "120"),
			categoryPosting(model.BookExpense, twd, "-120", "-120")), model.ErrWrongSign},
		{"an income that takes money out of the account", raw(model.KindIncome,
			accountPosting(acctA, twd, "-50"),
			categoryPosting(model.BookIncome, twd, "50", "50")), model.ErrWrongSign},
		{"an expense with a positive posting among negative ones", raw(model.KindExpense,
			accountPosting(acctA, twd, "-100"),
			categoryPosting(model.BookExpense, twd, "150", "150"),
			categoryPosting(model.BookExpense, twd, "-50", "-50")), model.ErrWrongSign},
		{"a transfer that moves into both accounts", raw(model.KindTransfer,
			accountPosting(acctA, twd, "3000"),
			accountPosting(acctB, twd, "3000")), model.ErrWrongSign},
		{"an exchange that takes out of both accounts", raw(model.KindExchange,
			accountPosting(acctA, usdID, "-3000"),
			bookPosting(model.BookConversion, usdID, "3000"),
			bookPosting(model.BookConversion, twd, "93000"),
			accountPosting(acctB, twd, "-93000")), model.ErrWrongSign},

		// Which postings make which kind.
		{"an expense with two accounts", raw(model.KindExpense,
			accountPosting(acctA, twd, "-60"),
			accountPosting(acctB, twd, "-60"),
			categoryPosting(model.BookExpense, twd, "120", "120")), model.ErrWrongPostings},
		{"an expense that has no expense posting", raw(model.KindExpense,
			accountPosting(acctA, twd, "-120"),
			accountPosting(acctB, twd, "120")), model.ErrWrongPostings},
		{"an expense with an income posting", raw(model.KindExpense,
			accountPosting(acctA, twd, "-120"),
			categoryPosting(model.BookIncome, twd, "120", "120")), model.ErrWrongPostings},
		{"an income with an expense posting", raw(model.KindIncome,
			accountPosting(acctA, twd, "120"),
			categoryPosting(model.BookExpense, twd, "-120", "-120")), model.ErrWrongPostings},
		{"a transfer with a conversion posting", raw(model.KindTransfer,
			accountPosting(acctA, twd, "-3000"),
			bookPosting(model.BookConversion, twd, "3000")), model.ErrWrongPostings},
		{"a transfer of three postings", raw(model.KindTransfer,
			accountPosting(acctA, twd, "-3000"),
			accountPosting(acctB, twd, "2000"),
			accountPosting(acctB, twd, "1000")), model.ErrWrongPostings},
		{"an exchange of one instrument", raw(model.KindExchange,
			accountPosting(acctA, twd, "-3000"),
			bookPosting(model.BookConversion, twd, "3000"),
			bookPosting(model.BookConversion, twd, "-3000"),
			accountPosting(acctB, twd, "3000")), model.ErrWrongPostings},
		{"an exchange with two conversions of one instrument", raw(model.KindExchange,
			accountPosting(acctA, usdID, "-3000"),
			bookPosting(model.BookConversion, usdID, "1000"),
			bookPosting(model.BookConversion, usdID, "2000"),
			accountPosting(acctB, twd, "93000")), model.ErrWrongPostings},
		{"an exchange without conversion postings", raw(model.KindExchange,
			accountPosting(acctA, usdID, "-3000"),
			accountPosting(acctB, twd, "93000")), model.ErrWrongPostings},
		{"an opening balance of three postings", raw(model.KindOpening,
			accountPosting(acctA, twd, "1000"),
			bookPosting(model.BookOpening, twd, "-500"),
			bookPosting(model.BookOpening, twd, "-500")), model.ErrWrongPostings},
		{"an opening balance against a conversion", raw(model.KindOpening,
			accountPosting(acctA, twd, "1000"),
			bookPosting(model.BookConversion, twd, "-1000")), model.ErrWrongPostings},

		// A transfer needs two accounts.
		{"a transfer from an account to itself", raw(model.KindTransfer,
			accountPosting(acctA, twd, "-3000"),
			accountPosting(acctA, twd, "3000")), model.ErrSameAccount},

		// What a posting must have by itself, as the table's CHECKs say.
		{"an account posting without an account", raw(model.KindOpening,
			bookPosting(model.BookAccount, twd, "1000"),
			bookPosting(model.BookOpening, twd, "-1000")), model.ErrMalformedPosting},
		{"an opening posting with an account", raw(model.KindOpening,
			accountPosting(acctA, twd, "1000"),
			model.Posting{Book: model.BookOpening, AccountID: &acctB, InstrumentID: twd, Quantity: dec("-1000")}), model.ErrMalformedPosting},
		{"an account posting with a category", raw(model.KindOpening,
			model.Posting{Book: model.BookAccount, AccountID: &acctA, CategoryID: &catX, InstrumentID: twd, Quantity: dec("1000")},
			bookPosting(model.BookOpening, twd, "-1000")), model.ErrMalformedPosting},
		{"an account posting with a base amount", raw(model.KindOpening,
			model.Posting{Book: model.BookAccount, AccountID: &acctA, InstrumentID: twd, Quantity: dec("1000"), BaseAmount: decPtr("1000")},
			bookPosting(model.BookOpening, twd, "-1000")), model.ErrMalformedPosting},
		{"an expense posting without a category", raw(model.KindExpense,
			accountPosting(acctA, twd, "-120"),
			model.Posting{Book: model.BookExpense, InstrumentID: twd, Quantity: dec("120"), BaseAmount: decPtr("120")}), model.ErrMalformedPosting},
		{"an expense posting without a base amount", raw(model.KindExpense,
			accountPosting(acctA, twd, "-120"),
			model.Posting{Book: model.BookExpense, CategoryID: &catX, InstrumentID: twd, Quantity: dec("120")}), model.ErrMalformedPosting},
		{"an expense posting with an account", raw(model.KindExpense,
			accountPosting(acctA, twd, "-120"),
			model.Posting{Book: model.BookExpense, AccountID: &acctA, CategoryID: &catX, InstrumentID: twd, Quantity: dec("120"), BaseAmount: decPtr("120")}), model.ErrMalformedPosting},
		{"a base amount of the other sign", raw(model.KindExpense,
			accountPosting(acctA, usdID, "-100"),
			categoryPosting(model.BookExpense, usdID, "100", "-3200")), model.ErrMalformedPosting},
		{"a base amount of zero", raw(model.KindExpense,
			accountPosting(acctA, usdID, "-100"),
			categoryPosting(model.BookExpense, usdID, "100", "0")), model.ErrMalformedPosting},
		{"a conversion posting with a base amount", raw(model.KindExchange,
			accountPosting(acctA, usdID, "-3000"),
			model.Posting{Book: model.BookConversion, InstrumentID: usdID, Quantity: dec("3000"), BaseAmount: decPtr("93000")},
			bookPosting(model.BookConversion, twd, "-93000"),
			accountPosting(acctB, twd, "93000")), model.ErrMalformedPosting},
		{"a posting that moves nothing", raw(model.KindOpening,
			accountPosting(acctA, twd, "0"),
			bookPosting(model.BookOpening, twd, "0")), model.ErrMalformedPosting},
		{"a posting made to no book", raw(model.KindOpening,
			accountPosting(acctA, twd, "1000"),
			bookPosting(model.Book("payable"), twd, "-1000")), model.ErrMalformedPosting},

		// The base amount of an amount in the base currency is that amount.
		{"a base amount that is not the quantity, in the base currency", raw(model.KindExpense,
			accountPosting(acctA, twd, "-120"),
			categoryPosting(model.BookExpense, twd, "120", "121")), model.ErrBaseAmountMismatch},

		{"no postings", raw(model.KindExpense), model.ErrNoPostings},
		{"a kind that is not one of the fixed ones", raw(model.Kind("payment"),
			accountPosting(acctA, twd, "-1"),
			accountPosting(acctB, twd, "1")), model.ErrInvalidKind},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.txn.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("Validate: err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestValidateRefusesAStatusThatIsNotOneOfTheFixedOnes(t *testing.T) {
	txn := raw(model.KindTransfer, accountPosting(acctA, usdID, "-1"), accountPosting(acctB, usdID, "1"))
	txn.Status = model.Status("pending")

	if err := txn.Validate(); !errors.Is(err, model.ErrInvalidStatus) {
		t.Fatalf("Validate: err = %v, want ErrInvalidStatus", err)
	}
}

func TestValidateNamesThePostingAndTheInstrumentItRefuses(t *testing.T) {
	txn := raw(model.KindExpense,
		accountPosting(acctA, usdID, "-100"),
		categoryPosting(model.BookExpense, usdID, "100", "-3200"))
	err := txn.Validate()
	if err == nil || !strings.HasPrefix(err.Error(), "posting 2: ") {
		t.Errorf("err = %v, want it to say it is posting 2", err)
	}

	txn = raw(model.KindTransfer,
		accountPosting(acctA, usdID, "-3000"),
		accountPosting(acctB, usdID, "2999"))
	err = txn.Validate()
	if err == nil || !strings.Contains(err.Error(), usdID.String()) || !strings.Contains(err.Error(), "-1") {
		t.Errorf("err = %v, want it to name the instrument and by how much the postings are off", err)
	}
}

func TestOnlyADraftCanBeConfirmed(t *testing.T) {
	for _, tt := range []struct {
		status model.Status
		ok     bool
	}{
		{model.StatusDraft, true},
		{model.StatusConfirmed, false},
		{model.StatusVoid, false},
	} {
		draft := model.Transaction{ID: uuid.Must(uuid.NewV7()), Status: tt.status}

		got, err := draft.Confirm()
		switch {
		case tt.ok && (err != nil || got.Status != model.StatusConfirmed):
			t.Errorf("%s: Confirm = %q, %v, want it confirmed", tt.status, got.Status, err)
		case !tt.ok && !errors.Is(err, model.ErrNotDraft):
			t.Errorf("%s: err = %v, want ErrNotDraft", tt.status, err)
		}
		if draft.Status != tt.status {
			t.Errorf("%s: the transaction changed to %q, want the original left as it was", tt.status, draft.Status)
		}
	}
}

func TestOnlyAConfirmedTransactionCanBeVoided(t *testing.T) {
	for _, tt := range []struct {
		status model.Status
		ok     bool
	}{
		{model.StatusDraft, false},
		{model.StatusConfirmed, true},
		{model.StatusVoid, false},
	} {
		txn := model.Transaction{ID: uuid.Must(uuid.NewV7()), Status: tt.status}

		got, err := txn.Void()
		switch {
		case tt.ok && (err != nil || got.Status != model.StatusVoid):
			t.Errorf("%s: Void = %q, %v, want it void", tt.status, got.Status, err)
		case !tt.ok && !errors.Is(err, model.ErrNotConfirmed):
			t.Errorf("%s: err = %v, want ErrNotConfirmed", tt.status, err)
		}
		if txn.Status != tt.status {
			t.Errorf("%s: the transaction changed to %q, want the original left as it was", tt.status, txn.Status)
		}
	}
}

func TestOnlyADraftCanBeDiscarded(t *testing.T) {
	for _, tt := range []struct {
		status model.Status
		ok     bool
	}{
		{model.StatusDraft, true},
		{model.StatusConfirmed, false},
		{model.StatusVoid, false},
	} {
		txn := model.Transaction{ID: uuid.Must(uuid.NewV7()), Status: tt.status}

		err := txn.CheckDiscardable()
		switch {
		case tt.ok && err != nil:
			t.Errorf("%s: CheckDiscardable: %v, want nil", tt.status, err)
		case !tt.ok && !errors.Is(err, model.ErrNotDraft):
			t.Errorf("%s: err = %v, want ErrNotDraft", tt.status, err)
		}
	}
}

func TestTransitionErrorsSayWhatTheTransactionIs(t *testing.T) {
	txn := model.Transaction{ID: uuid.Must(uuid.NewV7()), Status: model.StatusVoid}

	_, err := txn.Confirm()
	if err == nil || !strings.Contains(err.Error(), txn.ID.String()) || !strings.Contains(err.Error(), "void") {
		t.Errorf("Confirm: err = %v, want it to name the transaction and its status", err)
	}
	_, err = txn.Void()
	if err == nil || !strings.Contains(err.Error(), txn.ID.String()) || !strings.Contains(err.Error(), "void") {
		t.Errorf("Void: err = %v, want it to name the transaction and its status", err)
	}
}

func TestNormalizeIdempotencyKey(t *testing.T) {
	got, err := model.NormalizeIdempotencyKey("  lunch-0001 \n")
	if err != nil || got != "lunch-0001" {
		t.Fatalf("NormalizeIdempotencyKey = %q, %v, want the trimmed key", got, err)
	}

	var keyErr *model.IdempotencyKeyLengthError
	_, err = model.NormalizeIdempotencyKey("short")
	if !errors.As(err, &keyErr) || keyErr.Runes != 5 || keyErr.Min != model.MinKeyRunes || keyErr.Max != model.MaxKeyRunes {
		t.Fatalf("err = %v, want an *IdempotencyKeyLengthError for 5 characters", err)
	}

	// The bounds count characters, not bytes.
	if _, err := model.NormalizeIdempotencyKey("午餐午餐午餐午餐"); err != nil {
		t.Errorf("8 characters of 3 bytes each: %v, want it accepted", err)
	}
}

func TestDateOfIsTheCalendarDayInItsOwnLocation(t *testing.T) {
	got := model.DateOf(time.Date(2026, 11, 14, 0, 30, 0, 0, taipei))
	if want := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("DateOf = %v, want %v", got, want)
	}
}
