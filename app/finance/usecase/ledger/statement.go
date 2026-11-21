package ledger

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// SetPostingDate sets, changes or clears the posting date of a transaction, in one
// database transaction. The rules are the model's (Transaction.WithPostedOn); the
// use case loads what they are checked on: the accounts of the transaction's
// account postings, which say whether it posts to a card.
//
// Only the posting date is written: nothing a balance, a budget or a dealing counts
// is touched, which is why a confirmed transaction may have it changed.
//
// It takes one lock, on the transaction, which is the one lock ConfirmTransactions,
// VoidTransaction and DiscardDraft take on an ordinary transaction, and takes it
// the way they do: a call that overlaps one of them waits for it, and then sees the
// status it left. It is also the only lock it takes and it takes it first, so it
// cannot be part of a cycle of calls that wait for each other. A transaction that is
// part of an instalment plan is not locked: whether it is in one cannot change (a
// transaction is never moved into a plan or out of one), so it is read without a
// lock and refused, as lockActed refuses a period whose origin is not given, and the
// locks that a call on the plan takes in its order, the periods last, are never
// waited for by this one.
func (s *Service) SetPostingDate(ctx context.Context, in usecase.SetPostingDateInput) (usecase.Transaction, error) {
	var out usecase.Transaction
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		known, err := s.transactions.Get(ctx, in.TransactionID)
		if err != nil {
			return fmt.Errorf("transaction %s: %w", in.TransactionID, err)
		}
		if err := known.CheckPostedOnAllowed(); err != nil {
			return err
		}

		// What the lock waits for may have changed the status, and what is checked is
		// what the lock returns.
		txn, err := s.transactions.GetForUpdate(ctx, in.TransactionID)
		if err != nil {
			return fmt.Errorf("transaction %s: %w", in.TransactionID, err)
		}
		accounts, err := s.postingAccounts(ctx, txn)
		if err != nil {
			return err
		}
		next, err := txn.WithPostedOn(in.PostedOn, accounts)
		if err != nil {
			return err
		}
		stored, err := s.transactions.SetPostedOn(ctx, txn.ID, next.PostedOn)
		if err != nil {
			return fmt.Errorf("set the posting date of transaction %s: %w", txn.ID, err)
		}
		out = toTransaction(stored)
		return nil
	})
	if err != nil {
		return usecase.Transaction{}, err
	}
	return out, nil
}

// postingAccounts loads the accounts the transaction has an account posting to,
// each once, archived or not.
func (s *Service) postingAccounts(ctx context.Context, txn model.Transaction) ([]model.Account, error) {
	var accounts []model.Account
	seen := make(map[uuid.UUID]bool, 2)
	for _, p := range txn.Postings {
		if p.Book != model.BookAccount || p.AccountID == nil || seen[*p.AccountID] {
			continue
		}
		seen[*p.AccountID] = true
		account, err := s.account(ctx, *p.AccountID)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}

// cardWithStatements loads the card whose statements are asked for. An account that
// is not there is model.ErrNotFound, and one that has no statements, which is one
// that is not a card and one that has no closing day to cut them by, is
// model.ErrNoStatements (Account.CheckHasStatements).
func (s *Service) cardWithStatements(ctx context.Context, id uuid.UUID) (model.Account, error) {
	account, err := s.account(ctx, id)
	if err != nil {
		return model.Account{}, err
	}
	if err := account.CheckHasStatements(); err != nil {
		return model.Account{}, err
	}
	return account, nil
}

// GetCardStatement returns the statement of the card that has the day the request
// names, or today, which is what the clock says in the timezone of the application.
// The days of the statement and when it is due are the model's arithmetic on the
// closing day and the payment due day of the card; what is on it is the repo's,
// which reads the figures, the transactions and the drafts in one snapshot. A
// statement is closed when its closing date is before today. What the card was
// charged for is divided up from the transactions of the statement
// (model.ChargesOfCard): by the top-level category of each expense line that the
// card paid for, to Shares for what was paid for others in an expense, and to Other
// for the rest.
func (s *Service) GetCardStatement(ctx context.Context, in usecase.GetCardStatementInput) (usecase.CardStatement, error) {
	card, err := s.cardWithStatements(ctx, in.AccountID)
	if err != nil {
		return usecase.CardStatement{}, err
	}
	today := s.today()
	on := today
	if in.ClosingOn != nil {
		on = model.DateOf(*in.ClosingOn)
	}
	period := model.StatementContaining(*card.ClosingDay, on)

	content, err := s.transactions.Statement(ctx, repo.StatementFilter{AccountID: card.ID, Period: period, Today: today})
	if err != nil {
		return usecase.CardStatement{}, fmt.Errorf("get the statement of card %q closing on %s: %w", card.Name, period.ClosingOn.Format(time.DateOnly), err)
	}
	charges, err := s.statementCharges(ctx, card.ID, content)
	if err != nil {
		return usecase.CardStatement{}, fmt.Errorf("get the statement of card %q closing on %s: %w", card.Name, period.ClosingOn.Format(time.DateOnly), err)
	}

	out := usecase.CardStatement{
		CardStatementSummary: statementSummary(card, period, today, content.Figures),
		Transactions:         make([]usecase.StatementTransaction, len(content.Transactions)),
		Drafts:               content.Drafts,
		Charges:              charges,
	}
	for i, txn := range content.Transactions {
		out.Transactions[i] = usecase.StatementTransaction{
			Transaction:   toTransaction(txn),
			StatementDate: txn.StatementDate(),
			Amounts:       amountsOn(txn, card.ID),
		}
	}
	return out, nil
}

// statementCharges divides up what the transactions of the statement charged the
// card, for each instrument it was charged in, in the order of the figures. The
// parents of the categories, the archived ones too, say which top-level category a
// line of an expense is for; they are read, in one call, only when there is a
// transaction to divide up.
func (s *Service) statementCharges(ctx context.Context, cardID uuid.UUID, content repo.StatementContent) ([]usecase.StatementCharges, error) {
	if len(content.Transactions) == 0 {
		return nil, nil
	}
	categories, err := s.categories.List(ctx, "", true)
	if err != nil {
		return nil, fmt.Errorf("list the categories to divide up the charges: %w", err)
	}
	parents := make(map[uuid.UUID]uuid.UUID, len(categories))
	for _, c := range categories {
		if c.ParentID != nil {
			parents[c.ID] = *c.ParentID
		}
	}
	topLevel := func(category uuid.UUID) uuid.UUID {
		if parent, ok := parents[category]; ok {
			return parent
		}
		return category
	}

	divided := model.ChargesOfCard(cardID, content.Transactions, topLevel)
	place := make(map[uuid.UUID]int, len(content.Figures))
	for i, f := range content.Figures {
		place[f.InstrumentID] = i
	}
	slices.SortFunc(divided, func(a, b model.CardCharges) int { return cmp.Compare(place[a.InstrumentID], place[b.InstrumentID]) })

	out := make([]usecase.StatementCharges, len(divided))
	for i, c := range divided {
		out[i] = usecase.StatementCharges{InstrumentID: c.InstrumentID, Shares: c.Shares, Other: c.Other, Categories: make([]usecase.CategoryCharge, len(c.Categories))}
		for j, category := range c.Categories {
			out[i].Categories[j] = usecase.CategoryCharge{CategoryID: category.CategoryID, Amount: category.Amount}
		}
	}
	return out, nil
}

// ListCardStatements lists the statements of the card that close from the first day
// of the range to the last, newest first, with their figures. The closing dates are
// the model's to work out; the figures of all the statements are read by one
// statement of SQL, so that they agree with each other.
func (s *Service) ListCardStatements(ctx context.Context, in usecase.ListCardStatementsInput) (usecase.ListCardStatementsOutput, error) {
	if in.From.IsZero() || in.To.IsZero() {
		return usecase.ListCardStatementsOutput{}, model.ErrDateRangeRequired
	}
	from, to := model.DateOf(in.From), model.DateOf(in.To)
	if to.Before(from) {
		return usecase.ListCardStatementsOutput{}, fmt.Errorf("%w: from %s, to %s",
			model.ErrDateRangeReversed, from.Format(time.DateOnly), to.Format(time.DateOnly))
	}
	if to.After(from.AddDate(usecase.MaxStatementRangeYears, 0, 0)) {
		return usecase.ListCardStatementsOutput{}, fmt.Errorf("%w: from %s, to %s, and the longest range is %d years",
			model.ErrStatementRangeTooWide, from.Format(time.DateOnly), to.Format(time.DateOnly), usecase.MaxStatementRangeYears)
	}
	card, err := s.cardWithStatements(ctx, in.AccountID)
	if err != nil {
		return usecase.ListCardStatementsOutput{}, err
	}

	periods := model.StatementsClosingBetween(*card.ClosingDay, from, to)
	out := usecase.ListCardStatementsOutput{Statements: []usecase.CardStatementSummary{}}
	if len(periods) == 0 {
		return out, nil
	}
	today := s.today()
	figures, err := s.transactions.StatementFigures(ctx, card.ID, periods, today)
	if err != nil {
		return usecase.ListCardStatementsOutput{}, fmt.Errorf("get the figures of the statements of card %q: %w", card.Name, err)
	}
	if len(figures) != len(periods) {
		return usecase.ListCardStatementsOutput{}, fmt.Errorf("get the figures of the statements of card %q: %d lists of figures for %d statements",
			card.Name, len(figures), len(periods))
	}
	for i, period := range periods {
		out.Statements = append(out.Statements, statementSummary(card, period, today, figures[i]))
	}
	slices.Reverse(out.Statements)
	return out, nil
}

// statementSummary makes the statement of the card for the period, with the
// figures.
func statementSummary(card model.Account, period model.StatementPeriod, today time.Time, figures []model.StatementFigures) usecase.CardStatementSummary {
	summary := usecase.CardStatementSummary{
		From:      period.From,
		ClosingOn: period.ClosingOn,
		Closed:    period.Closed(today),
		Figures:   make([]usecase.StatementFigures, len(figures)),
	}
	if card.PaymentDueDay != nil {
		summary.DueOn = new(period.DueDate(*card.PaymentDueDay))
	}
	for i, f := range figures {
		summary.Figures[i] = usecase.StatementFigures{
			InstrumentID: f.InstrumentID,
			Previous:     f.Previous,
			Charges:      f.Charges,
			Credits:      f.Credits,
			Closing:      f.Closing,
		}
	}
	return summary
}

// amountsOn returns what the transaction moved on the account: its account
// postings to it, in the order of the postings.
func amountsOn(txn model.Transaction, accountID uuid.UUID) []usecase.StatementAmount {
	var amounts []usecase.StatementAmount
	for _, p := range txn.Postings {
		if p.Book == model.BookAccount && p.AccountID != nil && *p.AccountID == accountID {
			amounts = append(amounts, usecase.StatementAmount{InstrumentID: p.InstrumentID, Quantity: p.Quantity})
		}
	}
	return amounts
}
