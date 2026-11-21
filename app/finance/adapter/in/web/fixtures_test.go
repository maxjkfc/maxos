package web

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// seqID is the id of the n-th thing of a test: it has no meaning but to be the
// same every time and to differ from the others.
func seqID(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-7000-8000-%012d", n))
}

// accountPosting is a posting to an account.
func accountPosting(seq int, account, instrument uuid.UUID, quantity string) usecase.Posting {
	return usecase.Posting{Seq: seq, Book: "account", AccountID: &account, InstrumentID: instrument, Quantity: dec(quantity)}
}

// expensePosting is an expense of a category, with its amount in the base currency.
func expensePosting(seq int, category, instrument uuid.UUID, quantity, base string) usecase.Posting {
	b := dec(base)
	return usecase.Posting{Seq: seq, Book: "expense", CategoryID: &category, InstrumentID: instrument, Quantity: dec(quantity), BaseAmount: &b}
}

// dealingPosting is a posting of a dealing with a party.
func dealingPosting(seq int, party, instrument uuid.UUID, quantity string) usecase.Posting {
	return usecase.Posting{Seq: seq, Book: "dealing", PartyID: &party, InstrumentID: instrument, Quantity: dec(quantity)}
}

// transaction is a transaction of the kind and the status that happened on a day, a
// confirmed one when the status is not said otherwise by the test.
func transaction(id uuid.UUID, kind, status string, on time.Time, postings ...usecase.Posting) usecase.Transaction {
	t := usecase.Transaction{
		ID:         id,
		Kind:       kind,
		Status:     status,
		OccurredOn: on,
		Postings:   postings,
		CreatedAt:  time.Date(2026, time.November, 1, 3, 4, 0, 0, time.UTC),
	}
	if status == "confirmed" || status == "void" {
		t.ConfirmedAt = new(time.Date(2026, time.November, 2, 5, 6, 0, 0, time.UTC))
	}
	if status == "void" {
		t.VoidedAt = new(time.Date(2026, time.November, 3, 7, 8, 0, 0, time.UTC))
	}
	return t
}

// lunchTransaction is an expense of lunch paid from the account.
func lunchTransaction(n int, on time.Time, account uuid.UUID, amount string) usecase.Transaction {
	t := transaction(seqID(n), "expense", "confirmed", on,
		accountPosting(1, account, twd, "-"+amount),
		expensePosting(2, lunch, twd, amount, amount),
	)
	t.Description = fmt.Sprintf("午餐 %d", n)
	return t
}
