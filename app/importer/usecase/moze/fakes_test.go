package moze_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	catalog "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/moze"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/mapping"
)

// delta is what a transaction does to what an account holds, on its day.
type delta struct {
	account    uuid.UUID
	instrument uuid.UUID
	quantity   decimal.Decimal
	on         time.Time
}

// stored is a transaction of the fake ledger.
type stored struct {
	finance.Transaction
	deltas []delta
}

// errPending is what the fake's Reconcile refuses with when drafts wait on the account.
var errPending = errors.New("fake: drafts are waiting on the account")

// fakeLedger is the finance and catalog use cases the import calls, in memory: it
// makes reference data, keeps transactions by their idempotency keys and counts what
// they do to the balances, so that what the import recorded and confirmed can be
// read back as balances by day. It records every call, so a test can say what the
// import called with what.
type fakeLedger struct {
	instruments  []finance.Instrument
	institutions []finance.Institution
	accounts     []finance.Account
	categories   []finance.Category
	areas        []catalog.Area
	projects     []catalog.Project
	tags         []catalog.Tag
	parties      []catalog.Party

	txns []*stored
	// calls are the inputs of the Record* use cases, in order.
	calls []any
	// reconciled are the inputs of Reconcile.
	reconciled []finance.ReconcileInput
	// confirms are the ids each ConfirmTransactions was asked for.
	confirms [][]uuid.UUID
	// events are the confirmations and the reconciliations in the order they were made:
	// "confirm <n>" with how many it confirmed, "reconcile <account> <day>".
	events []string
	// failOn makes the Record* use case fail for a key that has this text in it.
	failOn string
	// pageSize makes ListTransactions return pages of this size at most; 0 is as asked.
	pageSize int
	created  map[string]int
}

func newFakeLedger() *fakeLedger {
	twd := finance.Instrument{ID: uuid.MustParse("00000000-0000-7000-8000-000000000001"), Code: "TWD", Name: "TWD", Kind: "fiat", IsCurrency: true, Decimals: 2}
	return &fakeLedger{instruments: []finance.Instrument{twd}, created: map[string]int{}}
}

func (f *fakeLedger) id() uuid.UUID { return uuid.Must(uuid.NewV7()) }

func (f *fakeLedger) ledger() moze.Ledger {
	return moze.Ledger{
		CreateInstrument: f, ListInstruments: f, CreateInstitution: f, ListInstitutions: f, CreateAccount: f,
		ListAccounts: f, CreateCategory: f, ListCategories: f, RecordExpense: f, RecordIncome: f, RecordTransfer: f,
		RecordExchange: f, RecordTrade: f, RecordDealing: f, RecordReward: f, RecordBillInstalment: f,
		SetOpeningBalance: f, Reconcile: f, ConfirmTransactions: f, ListTransactions: f, GetBalances: f,
		CreateArea: f, ListAreas: f, CreateProject: f, ListProjects: f, CreateParty: f, ListParties: f,
		CreateTag: f, ListTags: f,
	}
}

// ---- reference data

func (f *fakeLedger) ListInstruments(context.Context, finance.ListInstrumentsInput) (finance.ListInstrumentsOutput, error) {
	return finance.ListInstrumentsOutput{Instruments: slices.Clone(f.instruments)}, nil
}

func (f *fakeLedger) CreateInstrument(_ context.Context, in finance.CreateInstrumentInput) (finance.Instrument, error) {
	f.created["instrument"]++
	i := finance.Instrument{ID: f.id(), Code: in.Code, Name: in.Name, Kind: in.Kind, IsCurrency: in.IsCurrency, Decimals: in.Decimals}
	f.instruments = append(f.instruments, i)
	return i, nil
}

func (f *fakeLedger) ListInstitutions(context.Context, finance.ListInstitutionsInput) (finance.ListInstitutionsOutput, error) {
	return finance.ListInstitutionsOutput{Institutions: slices.Clone(f.institutions)}, nil
}

func (f *fakeLedger) CreateInstitution(_ context.Context, in finance.CreateInstitutionInput) (finance.Institution, error) {
	f.created["institution"]++
	i := finance.Institution{ID: f.id(), Name: in.Name}
	f.institutions = append(f.institutions, i)
	return i, nil
}

func (f *fakeLedger) ListAccounts(context.Context, finance.ListAccountsInput) (finance.ListAccountsOutput, error) {
	return finance.ListAccountsOutput{Accounts: slices.Clone(f.accounts)}, nil
}

func (f *fakeLedger) CreateAccount(_ context.Context, in finance.CreateAccountInput) (finance.Account, error) {
	f.created["account"]++
	a := finance.Account{ID: f.id(), Name: in.Name, Kind: in.Kind, InstitutionID: in.InstitutionID, ClosingDay: in.ClosingDay, PaymentDueDay: in.PaymentDueDay}
	f.accounts = append(f.accounts, a)
	return a, nil
}

func (f *fakeLedger) ListCategories(context.Context, finance.ListCategoriesInput) (finance.ListCategoriesOutput, error) {
	return finance.ListCategoriesOutput{Categories: slices.Clone(f.categories)}, nil
}

func (f *fakeLedger) CreateCategory(_ context.Context, in finance.CreateCategoryInput) (finance.Category, error) {
	f.created["category"]++
	c := finance.Category{ID: f.id(), Direction: in.Direction, ParentID: in.ParentID, Name: in.Name}
	f.categories = append(f.categories, c)
	return c, nil
}

func (f *fakeLedger) ListAreas(context.Context, catalog.ListAreasInput) (catalog.ListAreasOutput, error) {
	return catalog.ListAreasOutput{Areas: slices.Clone(f.areas)}, nil
}

func (f *fakeLedger) CreateArea(_ context.Context, in catalog.CreateAreaInput) (catalog.Area, error) {
	f.created["area"]++
	a := catalog.Area{ID: f.id(), Name: in.Name}
	f.areas = append(f.areas, a)
	return a, nil
}

func (f *fakeLedger) ListProjects(context.Context, catalog.ListProjectsInput) (catalog.ListProjectsOutput, error) {
	return catalog.ListProjectsOutput{Projects: slices.Clone(f.projects)}, nil
}

func (f *fakeLedger) CreateProject(_ context.Context, in catalog.CreateProjectInput) (catalog.Project, error) {
	f.created["project"]++
	p := catalog.Project{ID: f.id(), Name: in.Name, DefaultAreaID: in.DefaultAreaID, ActivePeriod: in.ActivePeriod}
	f.projects = append(f.projects, p)
	return p, nil
}

func (f *fakeLedger) ListParties(context.Context, catalog.ListPartiesInput) (catalog.ListPartiesOutput, error) {
	return catalog.ListPartiesOutput{Parties: slices.Clone(f.parties)}, nil
}

func (f *fakeLedger) CreateParty(_ context.Context, in catalog.CreatePartyInput) (catalog.Party, error) {
	f.created["party"]++
	p := catalog.Party{ID: f.id(), Name: in.Name}
	f.parties = append(f.parties, p)
	return p, nil
}

func (f *fakeLedger) ListTags(context.Context, catalog.ListTagsInput) (catalog.ListTagsOutput, error) {
	return catalog.ListTagsOutput{Tags: slices.Clone(f.tags)}, nil
}

func (f *fakeLedger) CreateTag(_ context.Context, in catalog.CreateTagInput) (catalog.Tag, error) {
	f.created["tag"]++
	t := catalog.Tag{ID: f.id(), Name: in.Name}
	f.tags = append(f.tags, t)
	return t, nil
}

func (f *fakeLedger) account(id uuid.UUID) string {
	for _, a := range f.accounts {
		if a.ID == id {
			return a.Name
		}
	}
	return id.String()
}

func (f *fakeLedger) instrument(id uuid.UUID) string {
	for _, i := range f.instruments {
		if i.ID == id {
			return i.Code
		}
	}
	return id.String()
}

// ---- transactions

// record stores a draft under its key, with what it does to the accounts; a key that
// is stored already is a duplicate and changes nothing.
func (f *fakeLedger) record(kind string, d finance.RecordDetails, deltas []delta) (finance.RecordOutput, error) {
	if f.failOn != "" && strings.Contains(d.IdempotencyKey, f.failOn) {
		return finance.RecordOutput{}, errors.New("fake: the ledger refuses " + d.IdempotencyKey)
	}
	if t := f.byKey(d.IdempotencyKey); t != nil {
		return finance.RecordOutput{Transaction: t.Transaction, Duplicate: true}, nil
	}
	t := &stored{
		Transaction: finance.Transaction{
			ID: f.id(), Kind: kind, Status: "draft", OccurredOn: d.OccurredOn, OccurredAt: d.OccurredAt,
			Description: d.Description, Merchant: d.Merchant, ProjectID: d.ProjectID, AreaID: d.AreaID,
			IdempotencyKey: d.IdempotencyKey, TagIDs: d.TagIDs,
		},
		deltas: deltas,
	}
	for i := range t.deltas {
		t.deltas[i].on = d.OccurredOn
	}
	f.txns = append(f.txns, t)
	return finance.RecordOutput{Transaction: t.Transaction}, nil
}

func (f *fakeLedger) byKey(key string) *stored {
	for _, t := range f.txns {
		if t.IdempotencyKey == key {
			return t
		}
	}
	return nil
}

func (f *fakeLedger) byID(id uuid.UUID) *stored {
	for _, t := range f.txns {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func neg(d decimal.Decimal) decimal.Decimal { return d.Neg() }

func fees(fs []finance.Fee) []delta {
	var out []delta
	for _, fee := range fs {
		out = append(out, delta{account: fee.AccountID, instrument: fee.InstrumentID, quantity: neg(fee.Amount)})
	}
	return out
}

func (f *fakeLedger) RecordExpense(_ context.Context, in finance.RecordExpenseInput) (finance.RecordOutput, error) {
	f.calls = append(f.calls, in)
	total := decimal.Zero
	for _, l := range in.Lines {
		total = total.Add(l.Amount)
	}
	return f.record("expense", in.RecordDetails, []delta{{account: *in.AccountID, instrument: in.InstrumentID, quantity: neg(total)}})
}

func (f *fakeLedger) RecordIncome(_ context.Context, in finance.RecordIncomeInput) (finance.RecordOutput, error) {
	f.calls = append(f.calls, in)
	total := decimal.Zero
	for _, l := range in.Lines {
		total = total.Add(l.Amount)
	}
	return f.record("income", in.RecordDetails, append([]delta{{account: in.AccountID, instrument: in.InstrumentID, quantity: total}}, fees(in.Fees)...))
}

func (f *fakeLedger) RecordReward(_ context.Context, in finance.RecordRewardInput) (finance.RecordOutput, error) {
	f.calls = append(f.calls, in)
	return f.record("reward", in.RecordDetails, append([]delta{{account: in.AccountID, instrument: in.InstrumentID, quantity: in.Amount}}, fees(in.Fees)...))
}

func (f *fakeLedger) RecordDealing(_ context.Context, in finance.RecordDealingInput) (finance.RecordOutput, error) {
	f.calls = append(f.calls, in)
	q := in.Amount
	if in.Direction == "paid_out" {
		q = neg(q)
	}
	return f.record("dealing", in.RecordDetails, append([]delta{{account: in.AccountID, instrument: in.InstrumentID, quantity: q}}, fees(in.Fees)...))
}

func (f *fakeLedger) RecordTransfer(_ context.Context, in finance.RecordTransferInput) (finance.RecordOutput, error) {
	f.calls = append(f.calls, in)
	return f.record("transfer", in.RecordDetails, append([]delta{
		{account: in.FromAccountID, instrument: in.InstrumentID, quantity: neg(in.Amount)},
		{account: in.ToAccountID, instrument: in.InstrumentID, quantity: in.Amount},
	}, fees(in.Fees)...))
}

func (f *fakeLedger) RecordExchange(_ context.Context, in finance.RecordExchangeInput) (finance.RecordOutput, error) {
	f.calls = append(f.calls, in)
	return f.record("exchange", in.RecordDetails, append([]delta{
		{account: in.FromAccountID, instrument: in.FromInstrumentID, quantity: neg(in.FromAmount)},
		{account: in.ToAccountID, instrument: in.ToInstrumentID, quantity: in.ToAmount},
	}, fees(in.Fees)...))
}

func (f *fakeLedger) RecordTrade(_ context.Context, in finance.RecordTradeInput) (finance.RecordOutput, error) {
	f.calls = append(f.calls, in)
	asset, cash := in.Quantity, neg(in.Amount)
	if in.Direction == "sell" {
		asset, cash = neg(asset), neg(cash)
	}
	return f.record("trade", in.RecordDetails, append([]delta{
		{account: in.AccountID, instrument: in.InstrumentID, quantity: asset},
		{account: in.CashAccountID, instrument: in.CurrencyID, quantity: cash},
	}, fees(in.Fees)...))
}

func (f *fakeLedger) SetOpeningBalance(_ context.Context, in finance.SetOpeningBalanceInput) (finance.RecordOutput, error) {
	f.calls = append(f.calls, in)
	return f.record("opening", in.RecordDetails, []delta{{account: in.AccountID, instrument: in.InstrumentID, quantity: in.Amount}})
}

func (f *fakeLedger) RecordBillInstalment(_ context.Context, in finance.RecordBillInstalmentInput) (finance.RecordOutput, error) {
	f.calls = append(f.calls, in)
	deltas := []delta{{account: in.AccountID, instrument: in.InstrumentID, quantity: in.Principal}}
	for _, p := range in.Schedule.Periods {
		charge := p.Principal
		if p.Interest != nil {
			charge = charge.Add(*p.Interest)
		}
		deltas = append(deltas, delta{account: in.AccountID, instrument: in.InstrumentID, quantity: neg(charge), on: p.DueOn})
	}
	out, err := f.record("instalment", in.RecordDetails, deltas)
	if err == nil && !out.Duplicate {
		// The periods keep the day they fall due, which record() overwrote with the day
		// of the origin.
		t := f.byKey(in.IdempotencyKey)
		for i, p := range in.Schedule.Periods {
			t.deltas[i+1].on = p.DueOn
		}
	}
	return out, err
}

func (f *fakeLedger) Reconcile(_ context.Context, in finance.ReconcileInput) (finance.ReconcileOutput, error) {
	f.reconciled = append(f.reconciled, in)
	f.events = append(f.events, fmt.Sprintf("reconcile %s %s", f.account(in.AccountID), in.OccurredOn.Format(time.DateOnly)))
	if t := f.byKey(in.IdempotencyKey); t != nil {
		return finance.ReconcileOutput{Transaction: &t.Transaction, Duplicate: true}, nil
	}
	day := in.OccurredOn
	for _, t := range f.txns {
		if t.Status != "draft" || t.OccurredOn.After(day) {
			continue
		}
		for _, d := range t.deltas {
			if d.account == in.AccountID && d.instrument == in.InstrumentID {
				return finance.ReconcileOutput{}, fmt.Errorf("reconcile: %w", errPending)
			}
		}
	}
	recorded := f.balance(in.AccountID, in.InstrumentID, day)
	difference := in.ActualBalance.Sub(recorded)
	if difference.IsZero() {
		return finance.ReconcileOutput{RecordedBalance: recorded}, nil
	}
	out, err := f.record("reconciliation", in.RecordDetails, []delta{{account: in.AccountID, instrument: in.InstrumentID, quantity: difference}})
	if err != nil {
		return finance.ReconcileOutput{}, err
	}
	return finance.ReconcileOutput{RecordedBalance: recorded, Difference: difference, Transaction: &out.Transaction}, nil
}

func (f *fakeLedger) ConfirmTransactions(_ context.Context, in finance.ConfirmTransactionsInput) (finance.ConfirmTransactionsOutput, error) {
	f.confirms = append(f.confirms, slices.Clone(in.IDs))
	f.events = append(f.events, fmt.Sprintf("confirm %d", len(in.IDs)))
	var out finance.ConfirmTransactionsOutput
	for _, id := range in.IDs {
		t := f.byID(id)
		if t == nil || t.Status != "draft" {
			return finance.ConfirmTransactionsOutput{}, fmt.Errorf("fake: %s is not a draft", id)
		}
	}
	for _, id := range in.IDs {
		t := f.byID(id)
		t.Status = "confirmed"
		out.Transactions = append(out.Transactions, t.Transaction)
	}
	return out, nil
}

func (f *fakeLedger) ListTransactions(_ context.Context, in finance.ListTransactionsInput) (finance.ListTransactionsOutput, error) {
	all := make([]finance.Transaction, 0, len(f.txns))
	for _, t := range f.txns {
		if in.Status == "" || t.Status == in.Status {
			all = append(all, t.Transaction)
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].OccurredOn.After(all[j].OccurredOn) })
	limit := in.Limit
	if limit == 0 || (f.pageSize > 0 && f.pageSize < limit) {
		limit = f.pageSize
	}
	end := min(in.Offset+limit, len(all))
	return finance.ListTransactionsOutput{Transactions: all[min(in.Offset, len(all)):end], HasMore: end < len(all)}, nil
}

// balance is what the account holds of the instrument on the day: the confirmed
// transactions up to it.
func (f *fakeLedger) balance(account, instrument uuid.UUID, day time.Time) decimal.Decimal {
	sum := decimal.Zero
	for _, t := range f.txns {
		if t.Status != "confirmed" {
			continue
		}
		for _, d := range t.deltas {
			if d.account == account && d.instrument == instrument && !d.on.After(day) {
				sum = sum.Add(d.quantity)
			}
		}
	}
	return sum
}

func (f *fakeLedger) GetBalances(_ context.Context, in finance.GetBalancesInput) (finance.GetBalancesOutput, error) {
	day := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
	if in.AsOf != nil {
		day = *in.AsOf
	}
	type pair struct{ a, i uuid.UUID }
	seen := map[pair]bool{}
	var out finance.GetBalancesOutput
	for _, t := range f.txns {
		if t.Status != "confirmed" {
			continue
		}
		for _, d := range t.deltas {
			p := pair{d.account, d.instrument}
			if seen[p] || d.on.After(day) || (in.AccountID != nil && *in.AccountID != d.account) {
				continue
			}
			seen[p] = true
			out.Balances = append(out.Balances, finance.Balance{AccountID: d.account, InstrumentID: d.instrument, Quantity: f.balance(d.account, d.instrument, day)})
		}
	}
	return out, nil
}

// balances is what the ledger holds at the end of the day, as the plan names it.
func (f *fakeLedger) balances(day time.Time) model.Balances {
	out := model.Balances{}
	got, _ := f.GetBalances(context.Background(), finance.GetBalancesInput{AsOf: &day})
	for _, b := range got.Balances {
		out[model.Pair{Account: f.account(b.AccountID), Instrument: f.instrument(b.InstrumentID)}] = b.Quantity
	}
	return out
}

// draftsOf is how many of the transactions of the ledger are drafts.
func (f *fakeLedger) drafts() int {
	n := 0
	for _, t := range f.txns {
		if t.Status == "draft" {
			n++
		}
	}
	return n
}

// ---- the files

type fakeSource struct{ rows []model.Row }

func (s fakeSource) Rows(context.Context, string) ([]model.Row, error) {
	rows := slices.Clone(s.rows)
	for i := range rows {
		rows[i].Pos, rows[i].Line = i, i+2
	}
	model.AssignKeys(rows)
	return rows, nil
}

// fakeMapping keeps the mapping in memory: what Append is given is what Load gives
// next, merged.
type fakeMapping struct {
	loaded   mapping.Loaded
	appended []model.Mapping
}

func (m *fakeMapping) Load(context.Context, string) (mapping.Loaded, error) { return m.loaded, nil }

func (m *fakeMapping) Append(_ context.Context, _ string, guessed model.Mapping) error {
	m.appended = append(m.appended, guessed)
	m.loaded.Mapping = m.loaded.Mapping.Merge(guessed)
	m.loaded.Exists = true
	return nil
}

type fakeRates struct{ rates model.Rates }

func (r fakeRates) Load(context.Context, string) (model.Rates, bool, error) {
	return r.rates, true, nil
}
