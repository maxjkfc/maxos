package ledger_test

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// txKey marks a context that belongs to a database transaction.
type txKey struct{}

// errNotInTx is what the fake repos return for a write that was not made in a
// database transaction: the use cases must make all their writes in one.
var errNotInTx = errors.New("the write was made outside a database transaction")

// errNoSuchParty is what the fake catalog returns for a party it does not have:
// an error of the catalog, which the ledger cannot tell from any other.
var errNoSuchParty = errors.New("the catalog has no such party")

func inTx(ctx context.Context) bool {
	_, ok := ctx.Value(txKey{}).(struct{})
	return ok
}

// fakeTx is a tx.Transactor that runs the function at once and marks the context
// it passes, so that the fake repos can tell a write made inside the database
// transaction from one made outside. It does not undo anything on an error: the
// use cases must not rely on a rollback for what they can check before they
// write.
type fakeTx struct {
	// calls is how many database transactions were opened.
	calls int
}

func (f *fakeTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	f.calls++
	return fn(context.WithValue(ctx, txKey{}, struct{}{}))
}

// callLog notes, in order, the calls of the fakes that share it which a test
// wants the order of. A fake without a log notes nothing.
type callLog struct{ calls []string }

func (l *callLog) note(call string) {
	if l != nil {
		l.calls = append(l.calls, call)
	}
}

// fakeTransactions is an in-memory repo.TransactionRepo.
type fakeTransactions struct {
	byID map[uuid.UUID]model.Transaction

	// inserts counts the transactions stored; locked lists the ids GetForUpdate
	// was asked for, in order; statusSet lists the ids SetStatus was called for;
	// deleted lists those deleted.
	inserts   int
	locked    []uuid.UUID
	statusSet []uuid.UUID
	deleted   []uuid.UUID

	// insertErr, when set, is what Insert returns.
	insertErr error
	// concurrent is a transaction that another call records under the key while
	// this one is being recorded: it is not there when the key is looked up, and
	// Insert finds the key taken by it.
	concurrent *model.Transaction

	// listed is the filter List was last called with, and lists how often it was
	// called; listResult is what it returns, cut to the limit of the filter.
	listed     repo.TransactionFilter
	lists      int
	listResult []model.Transaction
	// balancesFor is the account Balances was last asked for; balanceRows what it
	// returns.
	balancesFor *uuid.UUID
	balanceRows []model.Balance
	// dealingsFor is the party Dealings was last asked for; dealingRows what it
	// returns, in the order the repo promises: by party id, then instrument code.
	dealingsFor *uuid.UUID
	dealingRows []model.Dealing
	// readErr, when set, is what BalanceAsOf, PendingDrafts and Dealings return.
	readErr error
	// log, when set, is told of the calls whose order a test checks:
	// "look up the key" for GetByIdempotencyKey, "count the drafts" for
	// PendingDrafts and "read the balance" for BalanceAsOf.
	log *callLog
}

func newFakeTransactions() *fakeTransactions {
	return &fakeTransactions{byID: map[uuid.UUID]model.Transaction{}}
}

func (f *fakeTransactions) stamp() time.Time {
	return epoch.Add(time.Duration(len(f.byID)+1) * time.Second)
}

func (f *fakeTransactions) Insert(ctx context.Context, txn model.Transaction) (model.Transaction, error) {
	if !inTx(ctx) {
		return model.Transaction{}, errNotInTx
	}
	if f.insertErr != nil {
		return model.Transaction{}, f.insertErr
	}
	if f.concurrent != nil {
		f.byID[f.concurrent.ID] = *f.concurrent
		f.concurrent = nil
		return model.Transaction{}, repo.ErrDuplicateKey
	}
	for _, stored := range f.byID {
		if stored.IdempotencyKey == txn.IdempotencyKey {
			return model.Transaction{}, repo.ErrDuplicateKey
		}
	}
	txn.CreatedAt = f.stamp()
	f.byID[txn.ID] = txn
	f.inserts++
	return txn, nil
}

func (f *fakeTransactions) Get(_ context.Context, id uuid.UUID) (model.Transaction, error) {
	txn, ok := f.byID[id]
	if !ok {
		return model.Transaction{}, model.ErrNotFound
	}
	return txn, nil
}

func (f *fakeTransactions) GetForUpdate(ctx context.Context, id uuid.UUID) (model.Transaction, error) {
	if !inTx(ctx) {
		return model.Transaction{}, errNotInTx
	}
	f.locked = append(f.locked, id)
	return f.Get(ctx, id)
}

func (f *fakeTransactions) GetByIdempotencyKey(_ context.Context, key string) (model.Transaction, error) {
	f.log.note("look up the key")
	for _, stored := range f.byID {
		if stored.IdempotencyKey == key {
			return stored, nil
		}
	}
	return model.Transaction{}, model.ErrNotFound
}

func (f *fakeTransactions) SetStatus(ctx context.Context, id uuid.UUID, status model.Status) (model.Transaction, error) {
	if !inTx(ctx) {
		return model.Transaction{}, errNotInTx
	}
	txn, ok := f.byID[id]
	if !ok {
		return model.Transaction{}, model.ErrNotFound
	}
	at := epoch.Add(time.Hour)
	txn.Status = status
	switch status {
	case model.StatusConfirmed:
		txn.ConfirmedAt = &at
	case model.StatusVoid:
		txn.VoidedAt = &at
	}
	f.byID[id] = txn
	f.statusSet = append(f.statusSet, id)
	return txn, nil
}

func (f *fakeTransactions) Delete(ctx context.Context, id uuid.UUID) error {
	if !inTx(ctx) {
		return errNotInTx
	}
	if _, ok := f.byID[id]; !ok {
		return model.ErrNotFound
	}
	delete(f.byID, id)
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeTransactions) List(_ context.Context, filter repo.TransactionFilter) ([]model.Transaction, error) {
	f.listed = filter
	f.lists++
	out := f.listResult
	if len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (f *fakeTransactions) Balances(_ context.Context, accountID *uuid.UUID) ([]model.Balance, error) {
	f.balancesFor = accountID
	return f.balanceRows, nil
}

// BalanceAsOf adds up the account postings of the confirmed transactions that
// happened on or before the day, as the repo does.
func (f *fakeTransactions) BalanceAsOf(_ context.Context, accountID, instrumentID uuid.UUID, day time.Time) (decimal.Decimal, error) {
	f.log.note("read the balance")
	if f.readErr != nil {
		return decimal.Zero, f.readErr
	}
	sum := decimal.Zero
	for _, txn := range f.byID {
		if txn.Status != model.StatusConfirmed || txn.OccurredOn.After(day) {
			continue
		}
		for _, p := range txn.Postings {
			if p.Book == model.BookAccount && *p.AccountID == accountID && p.InstrumentID == instrumentID {
				sum = sum.Add(p.Quantity)
			}
		}
	}
	return sum, nil
}

// PendingDrafts counts the drafts that happened on or before the day and have a
// posting to the account in the instrument, as the repo does.
func (f *fakeTransactions) PendingDrafts(_ context.Context, accountID, instrumentID uuid.UUID, day time.Time) (int, error) {
	f.log.note("count the drafts")
	if f.readErr != nil {
		return 0, f.readErr
	}
	pending := 0
	for _, txn := range f.byID {
		if txn.Status != model.StatusDraft || txn.OccurredOn.After(day) {
			continue
		}
		for _, p := range txn.Postings {
			if p.Book == model.BookAccount && *p.AccountID == accountID && p.InstrumentID == instrumentID {
				pending++
				break
			}
		}
	}
	return pending, nil
}

func (f *fakeTransactions) Dealings(_ context.Context, partyID *uuid.UUID) ([]model.Dealing, error) {
	f.dealingsFor = partyID
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.dealingRows, nil
}

// fakeAccounts is an in-memory repo.AccountRepo of which only Get and Lock are
// used.
type fakeAccounts struct {
	repo.AccountRepo

	accounts map[uuid.UUID]model.Account
	// locked lists the ids Lock was asked for, in order, and log, when set, is
	// told "lock the account" each time. lockErr, when set, is what Lock returns.
	locked  []uuid.UUID
	log     *callLog
	lockErr error
}

func newFakeAccounts() *fakeAccounts {
	return &fakeAccounts{accounts: map[uuid.UUID]model.Account{}}
}

func (f *fakeAccounts) add(name string) model.Account {
	a := model.Account{ID: uuid.Must(uuid.NewV7()), Name: name, Kind: model.AccountKindCash}
	f.accounts[a.ID] = a
	return a
}

func (f *fakeAccounts) archive(id uuid.UUID) {
	a := f.accounts[id]
	a.ArchivedAt = &epoch
	f.accounts[id] = a
}

func (f *fakeAccounts) Get(_ context.Context, id uuid.UUID) (model.Account, error) {
	a, ok := f.accounts[id]
	if !ok {
		return model.Account{}, model.ErrNotFound
	}
	return a, nil
}

// Lock notes that the account is locked, and returns model.ErrNotFound for an id
// it does not know. It needs a database transaction, as the repo does: the lock
// is held until the transaction ends, and one taken outside of it holds nothing.
func (f *fakeAccounts) Lock(ctx context.Context, id uuid.UUID) error {
	if !inTx(ctx) {
		return errNotInTx
	}
	f.log.note("lock the account")
	f.locked = append(f.locked, id)
	if f.lockErr != nil {
		return f.lockErr
	}
	if _, ok := f.accounts[id]; !ok {
		return model.ErrNotFound
	}
	return nil
}

// fakeInstruments is an in-memory repo.InstrumentRepo of which only Get is used.
type fakeInstruments struct {
	repo.InstrumentRepo

	instruments map[uuid.UUID]model.Instrument
}

func newFakeInstruments() *fakeInstruments {
	return &fakeInstruments{instruments: map[uuid.UUID]model.Instrument{}}
}

func (f *fakeInstruments) add(i model.Instrument) model.Instrument {
	f.instruments[i.ID] = i
	return i
}

func (f *fakeInstruments) archive(id uuid.UUID) {
	i := f.instruments[id]
	i.ArchivedAt = &epoch
	f.instruments[id] = i
}

func (f *fakeInstruments) Get(_ context.Context, id uuid.UUID) (model.Instrument, error) {
	i, ok := f.instruments[id]
	if !ok {
		return model.Instrument{}, model.ErrNotFound
	}
	return i, nil
}

// fakeCategories is an in-memory repo.CategoryRepo of which only Get is used.
type fakeCategories struct {
	repo.CategoryRepo

	categories map[uuid.UUID]model.Category
	// gets counts the calls of Get.
	gets int
}

func newFakeCategories() *fakeCategories {
	return &fakeCategories{categories: map[uuid.UUID]model.Category{}}
}

func (f *fakeCategories) add(direction model.Direction, name string) model.Category {
	c := model.Category{ID: uuid.Must(uuid.NewV7()), Direction: direction, Name: name}
	f.categories[c.ID] = c
	return c
}

func (f *fakeCategories) archive(id uuid.UUID) {
	c := f.categories[id]
	c.ArchivedAt = &epoch
	f.categories[id] = c
}

func (f *fakeCategories) Get(_ context.Context, id uuid.UUID) (model.Category, error) {
	f.gets++
	c, ok := f.categories[id]
	if !ok {
		return model.Category{}, model.ErrNotFound
	}
	return c, nil
}

// fakeReferences is the catalog as the ledger sees it, with some projects, areas,
// parties and tags. As a catalogusecase.VerifyReferences, an id it does not know
// is not found and one it knows as archived is archived. As a
// catalogusecase.ResolveProjectArea, it follows the rule of the port: the default
// area it was given, unless that is archived, and a *ReferenceError for a project
// that is not found or archived. As a catalogusecase.ListParties it lists its
// parties by name, as the catalog does. As a catalogusecase.GetParty it returns a
// party, archived or not, and errNoSuchParty for one it does not have.
type fakeReferences struct {
	// projects, areas, parties and tags map each id the catalog has to whether it
	// is archived.
	projects, areas, parties, tags map[uuid.UUID]bool
	// partyNames maps each party to its name.
	partyNames map[uuid.UUID]string
	// defaultAreas maps the projects that have a default area to it.
	defaultAreas map[uuid.UUID]uuid.UUID
	calls        []catalogusecase.VerifyReferencesInput
	// resolveAreaErr, when set, is what ResolveProjectArea returns.
	resolveAreaErr error
	// listedWith is what ListParties was last asked for, and listPartiesCalls how
	// often it was called; listPartiesErr, when set, is what it returns.
	listedWith       catalogusecase.ListPartiesInput
	listPartiesCalls int
	listPartiesErr   error
	// gotParties lists the ids GetParty was asked for, in order.
	gotParties []uuid.UUID
}

func newFakeReferences() *fakeReferences {
	return &fakeReferences{
		projects:     map[uuid.UUID]bool{},
		areas:        map[uuid.UUID]bool{},
		parties:      map[uuid.UUID]bool{},
		tags:         map[uuid.UUID]bool{},
		partyNames:   map[uuid.UUID]string{},
		defaultAreas: map[uuid.UUID]uuid.UUID{},
	}
}

func (f *fakeReferences) addProject(archived bool) uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	f.projects[id] = archived
	return id
}

func (f *fakeReferences) addArea(archived bool) uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	f.areas[id] = archived
	return id
}

func (f *fakeReferences) addTag(archived bool) uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	f.tags[id] = archived
	return id
}

func (f *fakeReferences) addParty(name string, archived bool) uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	f.parties[id] = archived
	f.partyNames[id] = name
	return id
}

func (f *fakeReferences) ListParties(_ context.Context, in catalogusecase.ListPartiesInput) (catalogusecase.ListPartiesOutput, error) {
	f.listedWith = in
	f.listPartiesCalls++
	if f.listPartiesErr != nil {
		return catalogusecase.ListPartiesOutput{}, f.listPartiesErr
	}
	var out catalogusecase.ListPartiesOutput
	for id, archived := range f.parties {
		if archived && !in.IncludeArchived {
			continue
		}
		out.Parties = append(out.Parties, catalogusecase.Party{ID: id, Name: f.partyNames[id]})
	}
	slices.SortFunc(out.Parties, func(a, b catalogusecase.Party) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID.String(), b.ID.String()))
	})
	return out, nil
}

func (f *fakeReferences) GetParty(_ context.Context, id uuid.UUID) (catalogusecase.Party, error) {
	f.gotParties = append(f.gotParties, id)
	if _, ok := f.parties[id]; !ok {
		return catalogusecase.Party{}, errNoSuchParty
	}
	return catalogusecase.Party{ID: id, Name: f.partyNames[id]}, nil
}

// setDefaultArea makes area the default area of project.
func (f *fakeReferences) setDefaultArea(project, area uuid.UUID) {
	f.defaultAreas[project] = area
}

func (f *fakeReferences) VerifyReferences(_ context.Context, in catalogusecase.VerifyReferencesInput) error {
	f.calls = append(f.calls, in)
	var problems []catalogusecase.ReferenceProblem
	check := func(kind catalogusecase.ReferenceKind, known map[uuid.UUID]bool, ids []uuid.UUID) {
		for _, id := range ids {
			archived, exists := known[id]
			if exists && !archived {
				continue
			}
			problems = append(problems, catalogusecase.ReferenceProblem{Kind: kind, ID: id, Archived: exists})
		}
	}
	check(catalogusecase.ReferenceProject, f.projects, in.ProjectIDs)
	check(catalogusecase.ReferenceArea, f.areas, in.AreaIDs)
	check(catalogusecase.ReferenceParty, f.parties, in.PartyIDs)
	check(catalogusecase.ReferenceTag, f.tags, in.TagIDs)
	if len(problems) > 0 {
		return &catalogusecase.ReferenceError{Problems: problems}
	}
	return nil
}

func (f *fakeReferences) ResolveProjectArea(_ context.Context, projectID uuid.UUID) (*catalogusecase.Area, error) {
	if f.resolveAreaErr != nil {
		return nil, f.resolveAreaErr
	}
	archived, exists := f.projects[projectID]
	if !exists || archived {
		return nil, &catalogusecase.ReferenceError{Problems: []catalogusecase.ReferenceProblem{
			{Kind: catalogusecase.ReferenceProject, ID: projectID, Archived: exists},
		}}
	}
	areaID, hasDefault := f.defaultAreas[projectID]
	if !hasDefault || f.areas[areaID] {
		return nil, nil
	}
	return &catalogusecase.Area{ID: areaID}, nil
}

// fakeProjects is a catalogusecase.ResolveProjectDefaults that returns what it is
// given, and notes the days it was asked about.
type fakeProjects struct {
	out  catalogusecase.ResolveProjectDefaultsOutput
	err  error
	days []time.Time
}

func (f *fakeProjects) ResolveProjectDefaults(_ context.Context, in catalogusecase.ResolveProjectDefaultsInput) (catalogusecase.ResolveProjectDefaultsOutput, error) {
	f.days = append(f.days, in.Date)
	return f.out, f.err
}
