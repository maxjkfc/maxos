package usecase

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// GetPreviewBalances returns what the accounts would hold today if every draft were
// confirmed: the page that shows a ledger whose transactions are all still drafts
// reads it, so that it shows balances and not an empty list. It is not what the
// accounts hold: a draft changes no balance, no budget and no report until
// ConfirmTransactions confirms it, and GetBalances goes on saying what is confirmed.
// It says what GetBalances would say today once all the drafts were confirmed, and
// nothing is stored.
//
// A preview balance is the confirmed balance and what the drafts add to it. The
// confirmed balance is the Quantity that GetBalances returns for today: the
// confirmed account postings that happened on or before today, added up. What the
// drafts add is the net of GetDraftSummary, account by account and of all the months
// added up: the account postings of the transactions in the status draft that happened
// on or before today, each in its own account. So the rules are those of GetDraftSummary,
// and the tests hold the two to each other:
//
//   - A transfer between two of my accounts is in the preview balance of both of them,
//     what leaves the one and what arrives in the other: it is in no total of drafts, and
//     it is in the balance of each account it moves.
//   - A draft dated after today is not in it, nor is a period of a draft instalment plan
//     that is not yet due: a transaction counts toward a balance from its day. A period
//     that has fallen due is, as it will be in the Quantity of the balance once the plan
//     is confirmed.
//   - A void transaction is not a draft, and is not in it. A draft that is of no account,
//     such as an expense that a party paid for me, moves no balance.
//
// UnbilledInstalment is the one that GetBalances returns: of the confirmed plans only.
// A draft plan has no unbilled instalment to list until it is confirmed, whatever its
// periods that are due have charged to the account (that is in Drafts), and the plans
// that ListInstalmentPlans lists are the confirmed ones too.
//
// It is one read: the confirmed part and the draft part of a balance are of one moment,
// so a draft that is confirmed meanwhile is in one of them and not in both.
type GetPreviewBalances interface {
	GetPreviewBalances(ctx context.Context, in GetPreviewBalancesInput) (GetPreviewBalancesOutput, error)
}

// GetPreviewBalancesInput asks for the balances of every account, as of today, which
// the clock of the application says, in its timezone.
type GetPreviewBalancesInput struct{}

// PreviewBalance is how much of an instrument an account would hold today if every draft
// were confirmed. A negative quantity is what the account would owe.
type PreviewBalance struct {
	AccountID    uuid.UUID
	InstrumentID uuid.UUID
	// Quantity is what the account would hold: Confirmed and Drafts added up.
	Quantity decimal.Decimal
	// Confirmed is what the account holds: the Quantity of its Balance as GetBalances
	// returns it for today.
	Confirmed decimal.Decimal
	// Drafts is what confirming the drafts would add to Confirmed, negative when it
	// would take away: zero when no draft moves this account in this instrument today,
	// and then Quantity is the balance as it is. It is the Net of the amounts of this
	// account in GetDraftSummary, of all the months added up.
	Drafts decimal.Decimal
	// UnbilledInstalment is the principal of the confirmed instalment plans of the
	// account that has not fallen due by today, as in Balance. It is zero or less, with
	// the sign of what an account owes, and it is not in Quantity.
	UnbilledInstalment decimal.Decimal
}

// GetPreviewBalancesOutput holds one preview balance for each account and instrument
// that has a confirmed posting of either book, or a draft's account posting, by today,
// by account name and then instrument code. A balance can be zero, when the postings
// cancel out; an account with no such posting has none. An account and instrument that
// only has unbilled instalment has one, with a quantity of zero. The accounts and
// instruments are those of GetBalances, and those that only the drafts move.
type GetPreviewBalancesOutput struct {
	Balances []PreviewBalance
}
