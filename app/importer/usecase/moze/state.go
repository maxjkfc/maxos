package moze

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	catalog "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// state is what the ledger has of the import, read at one time: the ids of the
// accounts and instruments by the names the plan uses, and the transactions the
// import recorded, found by their idempotency keys. The import tells its own
// transactions from the others by the key: it starts with model.KeyPrefix.
//
// A unit's transactions are told by the ID and the version in their key: the
// transactions of a unit in the version the plan says now are its own (current), and
// those of other versions of it, or of units made of rows it has, are what it
// replaces (see look). What no unit of the plan accounts for at all is an orphan.
type state struct {
	accountIDs      map[string]uuid.UUID
	accountNames    map[uuid.UUID]string
	instrumentIDs   map[string]uuid.UUID
	instrumentCodes map[uuid.UUID]string
	// byKey are the transactions of the import, by idempotency key.
	byKey map[string]finance.Transaction
	// refs has the ids of the areas, projects and tags by name, which a reconciliation
	// is recorded with.
	refs refs

	// planIDs are the IDs of the units of the plan, and planRows the keys of the rows
	// they are made of; units are the transactions of the units, by the ID in their key,
	// and rowUnits the IDs of the units that are made of a row, by the key of the row.
	// adjustments are the keys of the adjustments of the plan, which are the keys of
	// their reconciliations and are not the keys of units.
	planIDs     map[string]bool
	planRows    map[string]bool
	units       map[string][]versioned
	rowUnits    map[string]map[string]bool
	adjustments map[string]bool
	// instalmentUnits are the units of the plan that are made of rows of an instalment
	// (a conversion, a payment, interest), by ID, with the account of the ledger they
	// are of; plansGone are the plans of instalments the import recorded that the plan
	// does not have any more, which are not void.
	instalmentUnits map[string]string
	plansGone       []planGone
	// discarded are the drafts that have been discarded since the state was read.
	discarded map[uuid.UUID]bool
}

// versioned is a transaction of a unit, with the version and the attempt its key says.
type versioned struct {
	tx      finance.Transaction
	version string
	attempt int
	// gone is true for a draft that has been discarded since the state was read: by
	// the unit that replaced it first, when two units are made of the rows of one.
	gone bool
}

// planGone is the origin of a plan of instalments that the plan does not have any
// more, and the account of the ledger it is of.
type planGone struct {
	versioned
	account string
}

// readState reads the ledger, and sorts what the import recorded by the units of the
// plan.
func (a *Importer) readState(ctx context.Context, plan model.Plan) (*state, error) {
	st := &state{
		accountIDs:      map[string]uuid.UUID{},
		accountNames:    map[uuid.UUID]string{},
		instrumentIDs:   map[string]uuid.UUID{},
		instrumentCodes: map[uuid.UUID]string{},
		byKey:           map[string]finance.Transaction{},
		refs:            newRefs(),
		discarded:       map[uuid.UUID]bool{},
	}
	accounts, err := a.ledger.ListAccounts.ListAccounts(ctx, finance.ListAccountsInput{IncludeArchived: true})
	if err != nil {
		return nil, model.LedgerFailure(model.VerbList, model.ThingAccount, "", err)
	}
	for _, acc := range accounts.Accounts {
		st.accountIDs[acc.Name] = acc.ID
		st.accountNames[acc.ID] = acc.Name
	}
	instruments, err := a.ledger.ListInstruments.ListInstruments(ctx, finance.ListInstrumentsInput{IncludeArchived: true})
	if err != nil {
		return nil, model.LedgerFailure(model.VerbList, model.ThingInstrument, "", err)
	}
	for _, in := range instruments.Instruments {
		st.instrumentIDs[in.Code] = in.ID
		st.instrumentCodes[in.ID] = in.Code
	}
	if err := st.readCatalog(ctx, a); err != nil {
		return nil, err
	}
	if err := st.readTransactions(ctx, a); err != nil {
		return nil, err
	}
	st.index(plan)
	return st, nil
}

// readTransactions lists every transaction of the ledger, a page at a time, and keeps
// the ones the import recorded.
func (st *state) readTransactions(ctx context.Context, a *Importer) error {
	for offset := 0; ; {
		page, err := a.ledger.ListTransactions.ListTransactions(ctx, finance.ListTransactionsInput{Limit: finance.MaxListLimit, Offset: offset})
		if err != nil {
			return model.LedgerFailure(model.VerbList, model.ThingTransaction, "", err)
		}
		for _, t := range page.Transactions {
			if strings.HasPrefix(t.IdempotencyKey, model.KeyPrefix) {
				st.byKey[t.IdempotencyKey] = t
			}
		}
		offset += len(page.Transactions)
		if !page.HasMore || len(page.Transactions) == 0 {
			return nil
		}
	}
}

// readCatalog reads the ids of the areas, projects and tags, by name.
func (st *state) readCatalog(ctx context.Context, a *Importer) error {
	areas, err := a.ledger.ListAreas.ListAreas(ctx, catalog.ListAreasInput{IncludeArchived: true})
	if err != nil {
		return model.LedgerFailure(model.VerbList, model.ThingArea, "", err)
	}
	for _, ar := range areas.Areas {
		st.refs.areas[ar.Name] = ar.ID
	}
	projects, err := a.ledger.ListProjects.ListProjects(ctx, catalog.ListProjectsInput{IncludeArchived: true})
	if err != nil {
		return model.LedgerFailure(model.VerbList, model.ThingProject, "", err)
	}
	for _, p := range projects.Projects {
		st.refs.projects[p.Name] = p.ID
	}
	tags, err := a.ledger.ListTags.ListTags(ctx, catalog.ListTagsInput{IncludeArchived: true})
	if err != nil {
		return model.LedgerFailure(model.VerbList, model.ThingTag, "", err)
	}
	for _, t := range tags.Tags {
		st.refs.tags[t.Name] = t.ID
	}
	return nil
}

// index sorts the transactions of the import by the unit their key names, and the
// units by the rows they are made of.
func (st *state) index(plan model.Plan) {
	rows := plan.RowIndex()
	st.planIDs = map[string]bool{}
	st.planRows = map[string]bool{}
	st.instalmentUnits = map[string]string{}
	for _, u := range plan.Units {
		st.planIDs[u.ID] = true
		for _, row := range u.Rows {
			st.planRows[row] = true
			switch rows[row].Type {
			case model.TypeBillInstalment, model.TypeInstalmentPayment, model.TypeInterest:
				st.instalmentUnits[u.ID] = u.Main.Account
			}
		}
	}
	st.adjustments = map[string]bool{}
	for _, adj := range plan.Adjustments {
		st.adjustments[adj.Key] = true
	}
	st.units = map[string][]versioned{}
	st.rowUnits = map[string]map[string]bool{}
	st.plansGone = nil

	keys := make([]string, 0, len(st.byKey))
	for key := range st.byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parsed, ok := model.ParseKey(key)
		if !ok || st.adjustments[key] {
			continue
		}
		t := st.byKey[key]
		v := versioned{tx: t, version: parsed.Version, attempt: parsed.Attempt}
		st.units[parsed.ID] = append(st.units[parsed.ID], v)
		for _, row := range parsed.Rows() {
			if st.rowUnits[row] == nil {
				st.rowUnits[row] = map[string]bool{}
			}
			st.rowUnits[row][parsed.ID] = true
		}
		if parsed.IsPlan() && !st.planIDs[parsed.ID] && t.Status != "void" {
			st.plansGone = append(st.plansGone, planGone{versioned: v, account: st.firstAccount(t).Account})
		}
	}
}

// lookup is what the ledger has of a unit: its transactions in the version the plan
// says now, and the transactions it replaces.
type lookup struct {
	// attempt is the number of the latest attempt at the unit's present version: the
	// first, until the version was voided and recorded again.
	attempt int
	// current are the transactions of the unit in its present version, at that attempt,
	// by key.
	current map[string]finance.Transaction
	// others are the transactions of earlier versions of the unit, and of units that are
	// made of rows the unit has: the pairing of two rows has changed, a row that was
	// half of a pair stands alone, or the plan of instalments they were in is gone.
	// None of them is a unit of the plan, whose transactions are its own to replace.
	others []versioned
}

// look finds what the ledger has of the unit.
func (st *state) look(u model.Unit) lookup {
	related := map[string]bool{u.ID: true}
	for _, row := range u.Rows {
		for id := range st.rowUnits[row] {
			related[id] = true
		}
	}
	ids := make([]string, 0, len(related))
	for id := range related {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	l := lookup{attempt: 1, current: map[string]finance.Transaction{}}
	var mine []versioned
	seen := map[uuid.UUID]bool{}
	add := func(v versioned) {
		v.gone = st.discarded[v.tx.ID]
		seen[v.tx.ID] = true
		l.others = append(l.others, v)
	}
	for _, id := range ids {
		if id != u.ID && st.planIDs[id] {
			continue
		}
		for _, v := range st.units[id] {
			if id == u.ID && v.version == u.Version {
				mine = append(mine, v)
				l.attempt = max(l.attempt, v.attempt)
				continue
			}
			add(v)
		}
	}
	for _, v := range mine {
		if v.attempt == l.attempt {
			l.current[v.tx.IdempotencyKey] = v.tx
		}
	}
	// A plan of instalments that is gone still charges the interest and takes the
	// payments that the rows of the instalments in its account now come to as units of
	// their own, so those units are not recorded beside it: that would count the same
	// interest twice. A unit that the ledger has already in its present version is not
	// held back by it: it is another plan, which is as it was.
	if account, ok := st.instalmentUnits[u.ID]; ok && len(l.current) == 0 {
		for _, g := range st.plansGone {
			if g.account == account && !seen[g.tx.ID] {
				add(g.versioned)
			}
		}
	}
	return l
}

// stale tells whether a transaction that the unit replaces is confirmed: it is never
// changed, so the unit cannot be recorded as the plan says until it is voided.
func (l lookup) stale() bool {
	return slices.ContainsFunc(l.others, func(v versioned) bool { return v.tx.Status == "confirmed" })
}

// drafts are the transactions that the unit replaces that are drafts, and that have not
// been discarded: they are discarded when the unit is recorded.
func (l lookup) drafts() []versioned {
	var out []versioned
	for _, v := range l.others {
		if v.tx.Status == "draft" && !v.gone {
			out = append(out, v)
		}
	}
	return out
}

// madeRoomFor tells whether something else was recorded after the void of the
// transactions: the void was what made room for it, so that it was the plan that
// changed, and not the user who gave the unit up.
func (l lookup) madeRoomFor(voided []finance.Transaction) bool {
	var at time.Time
	for _, t := range voided {
		if t.VoidedAt != nil && t.VoidedAt.After(at) {
			at = *t.VoidedAt
		}
	}
	if at.IsZero() {
		return false
	}
	return slices.ContainsFunc(l.others, func(v versioned) bool { return !v.gone && v.tx.CreatedAt.After(at) })
}

// unitState is where a unit stands in the ledger.
type unitState int

const (
	// unitMissing is a unit the ledger does not have in its present version, or has
	// only some of.
	unitMissing unitState = iota
	unitDraft
	unitConfirmed
	// unitVoid is a unit that the user voided in the version the plan says now, and
	// gave up: it stays as it is.
	unitVoid
	// unitRevived is a unit whose present version was voided to make room for another
	// that is gone too: the plan says what it said before, and the unit is wanted.
	unitRevived
	// unitStale is a unit that the ledger has in another version that is confirmed.
	unitStale
)

// of returns where the unit stands, and the transactions of it that are drafts.
func (st *state) of(u model.Unit) (unitState, []uuid.UUID) {
	return stateOf(u, st.look(u))
}

// stateOf tells where the unit stands by what the ledger has of it.
func stateOf(u model.Unit, l lookup) (unitState, []uuid.UUID) {
	if l.stale() {
		return unitStale, nil
	}
	var drafts []uuid.UUID
	var voided []finance.Transaction
	keys := u.KeysAt(l.attempt)
	for _, key := range keys {
		t, ok := l.current[key]
		if !ok {
			return unitMissing, nil
		}
		switch t.Status {
		case "draft":
			drafts = append(drafts, t.ID)
		case "void":
			voided = append(voided, t)
		}
	}
	switch {
	case len(drafts) > 0:
		return unitDraft, drafts
	case len(voided) == 0:
		return unitConfirmed, nil
	case len(voided) == len(keys) && l.madeRoomFor(voided):
		return unitRevived, nil
	default:
		return unitVoid, nil
	}
}

// staleUnits are the units of the month that the ledger has in a version that is
// confirmed and is not the present one.
func (st *state) staleUnits(plan model.Plan, month string) []usecase.StaleUnit {
	var out []usecase.StaleUnit
	for _, u := range unitsOf(plan, month) {
		if s, _ := st.of(u); s == unitStale {
			out = append(out, usecase.StaleUnit{Unit: u, Lines: linesOf(plan, u)})
		}
	}
	return out
}

// adjustment returns the transaction that reconciles the adjustment, if the import
// recorded one.
func (st *state) adjustment(adj model.Adjustment) (finance.Transaction, bool) {
	t, ok := st.byKey[adj.Key]
	return t, ok
}

// orphans are the transactions of the import, drafts and confirmed ones, that no unit
// of the plan accounts for any more: the row was edited (an edited row is another row,
// which has another key), or deleted, or is left out now. A transaction of an earlier
// version of a unit, or of a pairing of rows that the plan now has otherwise, is not
// one: it is what the unit replaces. Void ones are not: they count toward nothing. The
// drafts that apply has discarded since the state was read are not.
func (st *state) orphans() []finance.Transaction {
	var out []finance.Transaction
	for key, t := range st.byKey {
		if t.Status == "void" || st.adjustments[key] || st.discarded[t.ID] {
			continue
		}
		parsed, ok := model.ParseKey(key)
		if !ok || st.planIDs[parsed.ID] || slices.ContainsFunc(parsed.Rows(), func(row string) bool { return st.planRows[row] }) {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].OccurredOn.Equal(out[j].OccurredOn) {
			return out[i].OccurredOn.Before(out[j].OccurredOn)
		}
		return out[i].IdempotencyKey < out[j].IdempotencyKey
	})
	return out
}

// orphanViews are the orphans as the use cases report them.
func (st *state) orphanViews() []usecase.Orphan {
	var out []usecase.Orphan
	for _, t := range st.orphans() {
		out = append(out, st.orphanOf(t))
	}
	return out
}

// orphanOf says what a transaction of the import is: its day, the first account it
// moves with how much, and what it says of itself.
func (st *state) orphanOf(t finance.Transaction) usecase.Orphan {
	o := st.firstAccount(t)
	o.Key, o.Status, o.Kind, o.Date, o.Description = t.IdempotencyKey, t.Status, t.Kind, t.OccurredOn, t.Description
	return o
}

// firstAccount is what the transaction does to the first account it moves: the
// account and the instrument by name and by code, and the quantity with its sign.
func (st *state) firstAccount(t finance.Transaction) usecase.Orphan {
	for _, p := range t.Postings {
		if p.AccountID != nil {
			return usecase.Orphan{Account: st.accountNames[*p.AccountID], Instrument: st.instrumentCodes[p.InstrumentID], Amount: p.Quantity}
		}
	}
	return usecase.Orphan{}
}
