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
// replaces (see look). What no unit of the plan accounts for at all is an orphan, when
// the days it is of are days that the export covers (see orphans).
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
	// does not have any more, which are not void and which the export is not cut short
	// of (see cuts).
	instalmentUnits map[string]string
	plansGone       []planGone
	// periods are the days each plan of instalments the ledger has is charged, by the id of
	// its origin; cuts are the plans of the import that the plan does not have and whose
	// periods run past the last day of the export, by the id of their origin: the export has
	// a part of them (see findCuts).
	periods map[uuid.UUID][]time.Time
	cuts    map[uuid.UUID]cutPlan
	// first and last are the first and the last day that the export covers, and covers
	// says whether it covers any: the days of its rows, and of its units that are of
	// rows with none (an opening balance is dated from the first row of its account).
	// dateless says whether it has rows with no day, which are the opening balances.
	covers      bool
	first, last time.Time
	dateless    bool
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

// openState reads the transactions of the ledger that the import recorded, and nothing
// else, and refuses the export that begins after the import in the ledger does (a
// *usecase.LaterStartError, with the state, whose transactions it has read): that is
// before anything is made or changed. The refusal names the transactions that are before
// the export, and for that it reads the accounts and the instruments too.
func (a *Importer) openState(ctx context.Context, plan model.Plan) (*state, error) {
	st := &state{
		accountIDs:      map[string]uuid.UUID{},
		accountNames:    map[uuid.UUID]string{},
		instrumentIDs:   map[string]uuid.UUID{},
		instrumentCodes: map[uuid.UUID]string{},
		byKey:           map[string]finance.Transaction{},
		periods:         map[uuid.UUID][]time.Time{},
		cuts:            map[uuid.UUID]cutPlan{},
		refs:            newRefs(),
		discarded:       map[uuid.UUID]bool{},
	}
	if err := st.readTransactions(ctx, a); err != nil {
		return nil, err
	}
	later, before := st.laterStart(plan)
	if len(before) == 0 {
		return st, nil
	}
	if err := st.readAccounts(ctx, a); err != nil {
		return st, err
	}
	if err := st.readInstruments(ctx, a); err != nil {
		return st, err
	}
	later.Before = st.viewsOf(before)
	return st, &usecase.LaterStartError{LaterStart: later}
}

// complete reads the accounts, the instruments and the catalog by the names the plan
// uses, and sorts what the import recorded by the units of the plan. It returns the plan
// as the ledger has it where the export has only a part of a plan of instalments (see
// adopt), which is the plan the use cases go on with.
func (st *state) complete(ctx context.Context, a *Importer, plan model.Plan) (model.Plan, error) {
	if err := st.readAccounts(ctx, a); err != nil {
		return plan, err
	}
	if err := st.readInstruments(ctx, a); err != nil {
		return plan, err
	}
	if err := st.readCatalog(ctx, a); err != nil {
		return plan, err
	}
	st.findCuts(plan)
	plan = st.adopt(plan)
	st.index(plan)
	return plan, nil
}

// readAccounts reads the accounts of the ledger, archived ones too, by name and by id.
func (st *state) readAccounts(ctx context.Context, a *Importer) error {
	accounts, err := a.ledger.ListAccounts.ListAccounts(ctx, finance.ListAccountsInput{IncludeArchived: true})
	if err != nil {
		return usecase.StepFailure(usecase.VerbList, usecase.ThingAccount, "", err)
	}
	for _, acc := range accounts.Accounts {
		st.accountIDs[acc.Name] = acc.ID
		st.accountNames[acc.ID] = acc.Name
	}
	return nil
}

// readInstruments reads the instruments of the ledger, archived ones too, by code and by id.
func (st *state) readInstruments(ctx context.Context, a *Importer) error {
	instruments, err := a.ledger.ListInstruments.ListInstruments(ctx, finance.ListInstrumentsInput{IncludeArchived: true})
	if err != nil {
		return usecase.StepFailure(usecase.VerbList, usecase.ThingInstrument, "", err)
	}
	for _, in := range instruments.Instruments {
		st.instrumentIDs[in.Code] = in.ID
		st.instrumentCodes[in.ID] = in.Code
	}
	return nil
}

// readTransactions lists every transaction of the ledger, a page at a time, and keeps
// the ones the import recorded, and the days the periods of each plan of instalments fall due.
func (st *state) readTransactions(ctx context.Context, a *Importer) error {
	for offset := 0; ; {
		page, err := a.ledger.ListTransactions.ListTransactions(ctx, finance.ListTransactionsInput{Limit: finance.MaxListLimit, Offset: offset})
		if err != nil {
			return usecase.StepFailure(usecase.VerbList, usecase.ThingTransaction, "", err)
		}
		for _, t := range page.Transactions {
			st.note(t)
		}
		offset += len(page.Transactions)
		if !page.HasMore || len(page.Transactions) == 0 {
			return nil
		}
	}
}

// note keeps a transaction of the ledger that the import recorded, by its key, and the day
// of a period of a plan of instalments, by the plan.
func (st *state) note(t finance.Transaction) {
	if strings.HasPrefix(t.IdempotencyKey, model.KeyPrefix) {
		st.byKey[t.IdempotencyKey] = t
	}
	if t.PlanID != nil && t.PlanSeq != nil && *t.PlanSeq > 0 {
		st.periods[*t.PlanID] = append(st.periods[*t.PlanID], t.OccurredOn)
	}
}

// readCatalog reads the ids of the areas, projects and tags, by name.
func (st *state) readCatalog(ctx context.Context, a *Importer) error {
	areas, err := a.ledger.ListAreas.ListAreas(ctx, catalog.ListAreasInput{IncludeArchived: true})
	if err != nil {
		return usecase.StepFailure(usecase.VerbList, usecase.ThingArea, "", err)
	}
	for _, ar := range areas.Areas {
		st.refs.areas[ar.Name] = ar.ID
	}
	projects, err := a.ledger.ListProjects.ListProjects(ctx, catalog.ListProjectsInput{IncludeArchived: true})
	if err != nil {
		return usecase.StepFailure(usecase.VerbList, usecase.ThingProject, "", err)
	}
	for _, p := range projects.Projects {
		st.refs.projects[p.Name] = p.ID
	}
	tags, err := a.ledger.ListTags.ListTags(ctx, catalog.ListTagsInput{IncludeArchived: true})
	if err != nil {
		return usecase.StepFailure(usecase.VerbList, usecase.ThingTag, "", err)
	}
	for _, t := range tags.Tags {
		st.refs.tags[t.Name] = t.ID
	}
	return nil
}

// index sorts the transactions of the import by the unit their key names, and the
// units by the rows they are made of.
func (st *state) index(plan model.Plan) {
	st.first, st.last, st.covers = exportSpan(plan)
	st.dateless = slices.ContainsFunc(plan.Rows, func(r model.Row) bool { return !r.HasDate() })
	rows := plan.RowIndex()
	st.planIDs = map[string]bool{}
	st.planRows = map[string]bool{}
	st.instalmentUnits = map[string]string{}
	for _, u := range plan.Units {
		st.planIDs[u.ID] = true
		for _, row := range u.Rows {
			st.planRows[row] = true
			if instalmentRow(rows[row].Type) {
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
		if _, cut := st.cuts[t.ID]; parsed.IsPlan() && !cut && !st.planIDs[parsed.ID] && t.Status != "void" {
			st.plansGone = append(st.plansGone, planGone{versioned: v, account: st.firstAccount(t).Account})
		}
	}
}

// exportSpan is the first and the last day that the plan covers, and whether it covers
// any: the days of the rows of the export, those without a day (an opening balance)
// excepted, and the days of its units, which an opening balance has from the first row
// of its account, a day that no row has.
func exportSpan(plan model.Plan) (first, last time.Time, covers bool) {
	widen := func(day time.Time) {
		switch {
		case day.IsZero():
		case !covers:
			first, last, covers = day, day, true
		default:
			if day.Before(first) {
				first = day
			}
			if day.After(last) {
				last = day
			}
		}
	}
	for _, r := range plan.Rows {
		widen(r.Date)
	}
	for _, u := range plan.Units {
		widen(u.Date)
	}
	return first, last, covers
}

// covered tells whether a day is one that the export covers: from its first day to its
// last. What the export says of a day it does not cover is nothing, so what the ledger
// has of such a day is not for the export to say it should not have.
func (st *state) covered(day time.Time) bool {
	return st.covers && !day.Before(st.first) && !day.After(st.last)
}
