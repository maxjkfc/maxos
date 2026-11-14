package ledger_test

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// taipei is the timezone of the application in the tests: ahead of UTC, so that
// the first hours of a day there are still the day before in UTC.
var taipei = time.FixedZone("UTC+8", 8*60*60)

// fakeClock is a clock.Clock that tells the time a test sets.
type fakeClock struct {
	now time.Time
}

func (f *fakeClock) Now() time.Time { return f.now }

// txKey marks a context that belongs to a database transaction.
type txKey struct{}

// errNotInTx is what the fake repos return for a write that was not made in a
// database transaction: the use cases must make all their writes in one.
var errNotInTx = errors.New("the write was made outside a database transaction")

// errForeignKey is what the fake repo returns for what the foreign key of the
// plan of a transaction would refuse: a period stored before its origin, and an
// origin deleted while a period of its plan is still stored.
var errForeignKey = errors.New("fin_transaction_plan_id_fkey")

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
	// beforeLock, when set, is called with the id GetForUpdate is asked for,
	// before it looks for it: what another call does while this one waits for the
	// lock.
	beforeLock func(id uuid.UUID)

	// listed is the filter List was last called with, and lists how often it was
	// called; listResult is what it returns, cut to the limit of the filter.
	listed     repo.TransactionFilter
	lists      int
	listResult []model.Transaction
	// balancesAsked lists the filters Balances was called with, in order.
	balancesAsked []repo.BalanceFilter
	// dealingsFor and dealingsDay are the party and the day Dealings was last asked
	// for; dealingRows is what it returns, in the order the repo promises: by party
	// id, then instrument code.
	dealingsFor *uuid.UUID
	dealingsDay time.Time
	dealingRows []model.Dealing
	// plansFor and plansDay are the account and the day InstalmentPlans was last
	// asked for, and plansAsked how often it was asked; planRows is what it returns.
	plansFor   *uuid.UUID
	plansDay   time.Time
	plansAsked int
	planRows   []model.InstalmentPlanSummary
	// readErr, when set, is what Balances, PendingDrafts, Dealings and
	// InstalmentPlans return.
	readErr error
	// log, when set, is told of the calls whose order a test checks:
	// "look up the key" for GetByIdempotencyKey, "count the drafts" for
	// PendingDrafts and "read the balance" for Balances.
	log *callLog
}

func newFakeTransactions() *fakeTransactions {
	return &fakeTransactions{byID: map[uuid.UUID]model.Transaction{}}
}

func (f *fakeTransactions) stamp() time.Time {
	return epoch.Add(time.Duration(len(f.byID)+1) * time.Second)
}

// Insert stores the transaction as the table would: a period of a plan refers to
// the origin of its plan, which must be stored already, and takes the place in
// the plan that no other transaction has.
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
	if txn.PlanID != nil {
		if _, found := f.byID[*txn.PlanID]; !found && *txn.PlanID != txn.ID {
			return model.Transaction{}, errForeignKey
		}
		for _, stored := range f.byID {
			if stored.PlanID != nil && *stored.PlanID == *txn.PlanID && *stored.PlanSeq == *txn.PlanSeq {
				return model.Transaction{}, errors.New("fin_transaction_plan_id_plan_seq_key")
			}
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
	if f.beforeLock != nil {
		f.beforeLock(id)
	}
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

// GetPlan returns the origin of the plan and then its periods in order, as the
// repo does, and takes no lock.
func (f *fakeTransactions) GetPlan(_ context.Context, planID uuid.UUID) (model.InstalmentPlan, error) {
	var members []model.Transaction
	for _, txn := range f.byID {
		if txn.PlanID != nil && *txn.PlanID == planID {
			members = append(members, txn)
		}
	}
	if len(members) == 0 {
		return model.InstalmentPlan{}, model.ErrNotFound
	}
	slices.SortFunc(members, func(a, b model.Transaction) int { return cmp.Compare(*a.PlanSeq, *b.PlanSeq) })
	return model.InstalmentPlan{Origin: members[0], Periods: members[1:]}, nil
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

// Delete deletes the transaction, which the table does not let be the origin of a
// plan while a period of the plan is stored.
func (f *fakeTransactions) Delete(ctx context.Context, id uuid.UUID) error {
	if !inTx(ctx) {
		return errNotInTx
	}
	if _, ok := f.byID[id]; !ok {
		return model.ErrNotFound
	}
	for _, other := range f.byID {
		if other.ID != id && other.PlanID != nil && *other.PlanID == id {
			return errForeignKey
		}
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

// Balances adds up, as the repo does, the account postings of the confirmed
// transactions that happened on or before the day for each account and
// instrument, and beside them the instalment postings. It orders the balances by
// account id and then instrument id, which is as much of the order of the repo as
// the fake has the names for.
func (f *fakeTransactions) Balances(_ context.Context, filter repo.BalanceFilter) ([]model.Balance, error) {
	f.log.note("read the balance")
	f.balancesAsked = append(f.balancesAsked, filter)
	if f.readErr != nil {
		return nil, f.readErr
	}
	type key struct{ account, instrument uuid.UUID }
	sums := map[key]*model.Balance{}
	for _, txn := range f.byID {
		if txn.Status != model.StatusConfirmed || txn.OccurredOn.After(filter.Day) {
			continue
		}
		for _, p := range txn.Postings {
			if p.Book != model.BookAccount && p.Book != model.BookInstalment {
				continue
			}
			if filter.AccountID != nil && *p.AccountID != *filter.AccountID {
				continue
			}
			if filter.InstrumentID != nil && p.InstrumentID != *filter.InstrumentID {
				continue
			}
			k := key{*p.AccountID, p.InstrumentID}
			balance, found := sums[k]
			if !found {
				balance = &model.Balance{AccountID: k.account, InstrumentID: k.instrument}
				sums[k] = balance
			}
			if p.Book == model.BookAccount {
				balance.Quantity = balance.Quantity.Add(p.Quantity)
			} else {
				balance.UnbilledInstalment = balance.UnbilledInstalment.Add(p.Quantity)
			}
		}
	}
	out := make([]model.Balance, 0, len(sums))
	for _, balance := range sums {
		out = append(out, *balance)
	}
	slices.SortFunc(out, func(a, b model.Balance) int {
		return cmp.Or(bytes.Compare(a.AccountID[:], b.AccountID[:]), bytes.Compare(a.InstrumentID[:], b.InstrumentID[:]))
	})
	return out, nil
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

func (f *fakeTransactions) Dealings(_ context.Context, partyID *uuid.UUID, day time.Time) ([]model.Dealing, error) {
	f.dealingsFor, f.dealingsDay = partyID, day
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.dealingRows, nil
}

func (f *fakeTransactions) InstalmentPlans(_ context.Context, accountID *uuid.UUID, day time.Time) ([]model.InstalmentPlanSummary, error) {
	f.plansFor, f.plansDay = accountID, day
	f.plansAsked++
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.planRows, nil
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
// parties by name, as the catalog does.
type fakeReferences struct {
	// projects, areas, parties and tags map each id the catalog has to whether it
	// is archived.
	projects, areas, parties, tags map[uuid.UUID]bool
	// partyNames maps each party to its name.
	partyNames map[uuid.UUID]string
	// defaultAreas maps the projects that have a default area to it.
	defaultAreas map[uuid.UUID]uuid.UUID
	calls        []catalogusecase.VerifyReferencesInput
	// verifyErr, when set, is what VerifyReferences returns.
	verifyErr error
	// resolveAreaErr, when set, is what ResolveProjectArea returns.
	resolveAreaErr error
	// listedWith is what ListParties was last asked for, and listPartiesCalls how
	// often it was called; listPartiesErr, when set, is what it returns.
	listedWith       catalogusecase.ListPartiesInput
	listPartiesCalls int
	listPartiesErr   error
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

// setDefaultArea makes area the default area of project.
func (f *fakeReferences) setDefaultArea(project, area uuid.UUID) {
	f.defaultAreas[project] = area
}

func (f *fakeReferences) VerifyReferences(_ context.Context, in catalogusecase.VerifyReferencesInput) error {
	f.calls = append(f.calls, in)
	if f.verifyErr != nil {
		return f.verifyErr
	}
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
