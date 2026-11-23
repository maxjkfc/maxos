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
// It says what GetBalances will say today once all the drafts are confirmed, in both of
// the figures of a balance, and nothing is stored.
//
// A balance has two figures, the ones GetBalances has: what the account holds
// (Held, from the account postings) and the principal of instalment plans that has not
// fallen due (Unbilled, from the instalment postings). Each is the confirmed figure,
// which is what GetBalances says today, and what the drafts add to it. What the drafts
// add is the postings of the transactions in the status draft that happened on or before
// today, each in its own account, of the book that the figure is of. For Held it is the
// net of GetDraftSummary, account by account, of all the months added up, so the rules
// are those of GetDraftSummary, and the tests hold the two to each other:
//
//   - A transfer between two of my accounts is in the Held of both of them, what leaves
//     the one and what arrives in the other: it is in no total of drafts, and it is in
//     the balance of each account it moves.
//   - A draft dated after today is not in it, nor is a period of a draft instalment plan
//     that is not yet due: a transaction counts toward a balance from its day. A period
//     that has fallen due is, as it will be in the balance once the plan is confirmed.
//   - A void transaction is not a draft, and is not in it. A draft that is of no account,
//     such as an expense that a party paid for me, moves no balance.
//
// What the drafts add to Unbilled is the principal of draft plans: the origin of a plan
// owes all of it from its day, and each period gives back what it charges from the day
// it falls due (which is then in Held, as it will be in the Quantity of the balance).
// GetDraftSummary does not count these postings, which are not account postings, and the
// page that shows the figures of a balance next to the table of the drafts says so. A plan
// that is a draft is not in ListInstalmentPlans, which lists the confirmed ones. It is not
// left out of Unbilled for that: a bill that is converted into instalments puts the debt
// it pays off in Held and the debt it makes in Unbilled, and a preview that left Unbilled
// out would show a card owing less than it will be once the plan is confirmed.
//
// It is one read: the confirmed part and the draft part of a balance, and the count of the
// drafts, are of one moment, so a draft that is confirmed meanwhile is in one of the parts
// and not in both. So what a page says of the balances as a whole (that they are a preview,
// of how many drafts, that none of them is confirmed) comes from the same answer as the
// balances.
//
// The balances are added up too, because a page that writes one figure of many accounts does
// not add: Totals for all of them, and Kinds for the accounts of each kind, an instrument at a
// time (PreviewTotal). The sums are of the balances of the same read, so a total and the
// balances under it say one thing; the instruments are put in order by their codes, and the
// kind of each account is its own, both of which the catalog has, and neither changes what
// a balance is.
type GetPreviewBalances interface {
	GetPreviewBalances(ctx context.Context, in GetPreviewBalancesInput) (GetPreviewBalancesOutput, error)
}

// GetPreviewBalancesInput asks for the balances of every account, as of today, which
// the clock of the application says, in its timezone.
type GetPreviewBalancesInput struct{}

// PreviewBasis says what a PreviewFigure is made of. The port has its own words for it and
// does not hand out the domain's type: a context outside finance may import the in-ports of
// finance and nothing of its domain, so what a caller of this use case compares a Basis with
// has to be here.
type PreviewBasis string

const (
	// PreviewBasisConfirmed is a figure that no draft changes: it is what it is, or nothing.
	PreviewBasisConfirmed PreviewBasis = "confirmed"
	// PreviewBasisBoth is a figure that is confirmed in part, and that drafts change.
	PreviewBasisBoth PreviewBasis = "both"
	// PreviewBasisDrafts is a figure none of which is confirmed: drafts make up the whole of it.
	PreviewBasisDrafts PreviewBasis = "drafts"
)

// PreviewFigure is one figure of a balance, as it would be once the drafts are confirmed.
type PreviewFigure struct {
	// Total is what the figure would be with every draft confirmed: Confirmed and Drafts
	// added up. It is what GetBalances will say.
	Total decimal.Decimal
	// Confirmed is what is confirmed of it: what GetBalances says today.
	Confirmed decimal.Decimal
	// Drafts is what confirming the drafts would add to Confirmed, negative when it would
	// take away: zero when no draft changes the figure today.
	Drafts decimal.Decimal
	// Basis says what the figure is made of: PreviewBasisConfirmed when no draft changes it
	// (it is what it is, or nothing), PreviewBasisDrafts when none of it is confirmed and
	// drafts make up the whole of it, and PreviewBasisBoth when it is confirmed in part and
	// drafts change it.
	Basis PreviewBasis
}

// PreviewBalance is how much of an instrument an account would hold today if every draft
// were confirmed, and how much it would owe in instalments that are not yet due. A
// negative figure is what the account would owe.
type PreviewBalance struct {
	AccountID    uuid.UUID
	InstrumentID uuid.UUID
	// Held is what the account would hold: the Quantity of its Balance, once the drafts
	// are confirmed. A negative quantity is what the account would owe.
	Held PreviewFigure
	// Unbilled is the principal of the instalment plans of the account that would not have
	// fallen due by today: the UnbilledInstalment of its Balance, once the drafts are
	// confirmed. It is zero or less, with the sign of what an account owes, and it is not
	// in Held.
	Unbilled PreviewFigure
}

// PreviewTotal is the preview balances of some accounts added up, of one instrument: what they
// would hold of it together (Held) and what they would owe of it in instalments that are not yet
// due (Unbilled), once the drafts are confirmed. Each figure is the sum of the figures of the
// balances: its Confirmed is theirs added up, its Drafts what the drafts would add to all of them,
// its Total the two added, and its Basis is said of those two sums as for any figure (so the
// confirmed parts of two accounts that cancel one another are nothing confirmed of the total).
// Instruments are never added to one another: no rate is known here, and the sum of dollars and
// yen is none.
type PreviewTotal struct {
	InstrumentID uuid.UUID
	Held         PreviewFigure
	Unbilled     PreviewFigure
}

// PreviewKindTotals is the totals of the accounts of one kind, for each instrument they have a
// balance of.
type PreviewKindTotals struct {
	// Kind is the kind of account: "cash", "bank", "card", "exchange", "brokerage", "wallet" or
	// "loan".
	Kind   string
	Totals []PreviewTotal
}

// GetPreviewBalancesOutput holds one preview balance for each account and instrument
// that has a posting of either book of a confirmed transaction or of a draft by today, by
// account name and then instrument code. A figure can be zero, when the postings cancel
// out; an account with no such posting has no balance. The accounts and instruments are
// those of GetBalances, and those that only the drafts move.
type GetPreviewBalancesOutput struct {
	Balances []PreviewBalance
	// Totals are all the balances added up, for each instrument that has one, by instrument
	// code: archived accounts that still hold something are in them, as they are in Balances; the
	// balance of a card or of a loan is negative, and takes away from what the rest hold. The
	// dealings with parties are not in them, nor is anything converted from one instrument to
	// another.
	Totals []PreviewTotal
	// Kinds are the same sums for the accounts of each kind that has a balance, by kind: each
	// instrument's Totals is the sum of the same instrument's total of every kind.
	Kinds []PreviewKindTotals
	// DraftCount is how many drafts wait, as GetDraftSummary counts them (Count): each once,
	// whatever day it happened on and whatever it moves, a plan being one, by its origin. It
	// is not how many of them are in the figures: one that is dated after today, or that is
	// of no account, is waiting and is not.
	DraftCount int
	// HasConfirmed is whether anything of any figure is confirmed. When it is not, the
	// balances are all drafts' and no account holds anything today.
	HasConfirmed bool
}
