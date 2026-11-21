package moze

import (
	"context"
	"slices"
	"sort"
	"strconv"

	"github.com/google/uuid"

	catalog "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// refs are the ids of what the ledger has, by the names the plan uses.
type refs struct {
	instruments map[string]uuid.UUID // by code
	accounts    map[string]uuid.UUID // by name
	expense     map[string]uuid.UUID // by category path
	income      map[string]uuid.UUID
	areas       map[string]uuid.UUID
	projects    map[string]uuid.UUID
	tags        map[string]uuid.UUID
	parties     map[string]uuid.UUID
	// areaNames are the names of the areas the ledger has, by id: the default area of a
	// project that exists is told by it.
	areaNames map[uuid.UUID]string
}

func newRefs() refs {
	return refs{
		instruments: map[string]uuid.UUID{},
		accounts:    map[string]uuid.UUID{},
		expense:     map[string]uuid.UUID{},
		income:      map[string]uuid.UUID{},
		areas:       map[string]uuid.UUID{},
		projects:    map[string]uuid.UUID{},
		tags:        map[string]uuid.UUID{},
		parties:     map[string]uuid.UUID{},
		areaNames:   map[uuid.UUID]string{},
	}
}

// needs is what a plan uses of the reference data.
type needs struct {
	instruments []string
	accounts    []string
	expense     []string
	income      []string
	areas       []string
	projects    []string
	tags        []string
	parties     []string
}

// needsOf lists what the units, the adjustments and the rows of the plan use: every
// instrument and account of its rows, and what each unit names.
func needsOf(plan model.Plan) needs {
	set := map[string]map[string]bool{}
	add := func(kind, name string) {
		if name == "" {
			return
		}
		if set[kind] == nil {
			set[kind] = map[string]bool{}
		}
		set[kind][name] = true
	}
	for _, r := range plan.Rows {
		if r.Currency != model.BaseCurrencyCode {
			add("instruments", r.Currency)
		}
		add("accounts", plan.Mapping.Accounts[r.Account].Name)
	}
	for _, u := range plan.Units {
		add("areas", u.Area)
		add("projects", u.Project)
		add("parties", u.Party)
		for _, t := range u.Tags {
			add("tags", t)
		}
		for _, l := range u.Lines {
			if u.Kind == model.KindIncome {
				add("income", l.Category)
			} else {
				add("expense", l.Category)
			}
		}
		for _, f := range u.Fees {
			add("expense", f.Category)
		}
		add("expense", u.InterestCategory)
	}
	for _, adj := range plan.Adjustments {
		add("areas", adj.Area)
		add("projects", adj.Project)
		for _, t := range adj.Tags {
			add("tags", t)
		}
	}
	for _, p := range plan.Mapping.Projects {
		if p.Kind == model.ProjectProject && set["projects"][p.Name] {
			add("areas", p.DefaultArea)
		}
	}
	list := func(kind string) []string {
		names := make([]string, 0, len(set[kind]))
		for n := range set[kind] {
			names = append(names, n)
		}
		sort.Strings(names)
		return names
	}
	return needs{
		instruments: list("instruments"), accounts: list("accounts"), expense: list("expense"), income: list("income"),
		areas: list("areas"), projects: list("projects"), tags: list("tags"), parties: list("parties"),
	}
}

// ensured is what ensure made of the reference data, and what it found that the user is
// to know.
type ensured struct {
	// created is how much was made, by what it is.
	created map[string]int
	// projectDifferences are the projects the ledger had, whose default area is not what
	// the mapping says.
	projectDifferences []usecase.ProjectDifference
}

// archived is the failure for reference data that the mapping names and the ledger has
// archived: it is not used, and not made again, since it has the name.
func archived(thing model.Thing, name string) *model.Failure {
	return &model.Failure{Code: model.FailureArchived, Args: []string{string(thing), name}}
}

// currencyFlag is how a failure says whether an instrument is a currency.
func currencyFlag(isCurrency bool) string {
	if isCurrency {
		return "currency"
	}
	return "not_currency"
}

// ensure makes what the plan uses of the reference data, and returns the ids of all
// of it, and how much was made. What the ledger has, by name, is used as it is; an
// instrument or an account that has the name but is another thing than the mapping
// says is a conflict to put right by hand, and is a failure, as is an archived one. A
// project that the ledger has is used as it is too, and a default area of it that is not
// the mapping's is reported.
//
// What stops it is a *model.Failure, never a sentence of this layer: a step the ledger
// did not take is model.LedgerFailure, with the reply of the ledger in it as it was
// given; the rest are the codes of the conflicts. The command line has the words.
func (a *Importer) ensure(ctx context.Context, plan model.Plan) (refs, ensured, error) {
	need := needsOf(plan)
	r := newRefs()
	made := ensured{created: map[string]int{}}
	created := made.created

	steps := []func() error{
		func() error { return a.ensureInstruments(ctx, plan, need, &r, created) },
		func() error { return a.ensureAccounts(ctx, plan, need, &r, created) },
		func() error { return a.ensureCategories(ctx, need, &r, created) },
		func() error { return a.ensureAreas(ctx, need, &r, created) },
		func() error { return a.ensureProjects(ctx, plan, need, &r, &made) },
		func() error { return a.ensureTags(ctx, need, &r, created) },
		func() error { return a.ensureParties(ctx, need, &r, created) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return refs{}, ensured{}, err
		}
	}
	return r, made, nil
}

func (a *Importer) ensureInstruments(ctx context.Context, plan model.Plan, need needs, r *refs, created map[string]int) error {
	listed, err := a.ledger.ListInstruments.ListInstruments(ctx, finance.ListInstrumentsInput{IncludeArchived: true})
	if err != nil {
		return model.LedgerFailure(model.VerbList, model.ThingInstrument, "", err)
	}
	have := map[string]finance.Instrument{}
	for _, in := range listed.Instruments {
		have[in.Code] = in
	}
	// The base currency is the ledger's own and is not made.
	if twd, ok := have[model.BaseCurrencyCode]; ok {
		r.instruments[model.BaseCurrencyCode] = twd.ID
	}
	for _, code := range need.instruments {
		want := plan.Mapping.Instruments[code]
		if in, ok := have[code]; ok {
			if in.ArchivedAt != nil {
				return archived(model.ThingInstrument, code)
			}
			if in.IsCurrency != want.IsCurrency || in.Decimals != want.Decimals || in.Kind != want.Kind {
				return &model.Failure{Code: model.FailureInstrumentDiffers, Args: []string{
					code, in.Kind, strconv.Itoa(in.Decimals), currencyFlag(in.IsCurrency),
					want.Kind, strconv.Itoa(want.Decimals), currencyFlag(want.IsCurrency),
				}}
			}
			r.instruments[code] = in.ID
			continue
		}
		made, err := a.ledger.CreateInstrument.CreateInstrument(ctx, finance.CreateInstrumentInput{
			Code: code, Name: want.Name, Kind: want.Kind, IsCurrency: want.IsCurrency, Decimals: want.Decimals,
		})
		if err != nil {
			return model.LedgerFailure(model.VerbCreate, model.ThingInstrument, code, err)
		}
		r.instruments[code] = made.ID
		created[usecase.CreatedInstruments]++
	}
	return nil
}

// accountSpecs gives, for each account of the ledger the mapping makes, what it is: of
// the accounts of MOZE that are it, in order of their names, the first has the kind
// and the institution, and the first that has them the days of the statements.
func accountSpecs(m model.Mapping) map[string]model.AccountMap {
	names := make([]string, 0, len(m.Accounts))
	for n := range m.Accounts {
		names = append(names, n)
	}
	sort.Strings(names)
	specs := map[string]model.AccountMap{}
	for _, n := range names {
		entry := m.Accounts[n]
		spec, ok := specs[entry.Name]
		if !ok {
			specs[entry.Name] = entry
			continue
		}
		if spec.ClosingDay == 0 && entry.ClosingDay != 0 {
			spec.ClosingDay, spec.PaymentDueDay = entry.ClosingDay, entry.PaymentDueDay
			specs[entry.Name] = spec
		}
	}
	return specs
}

func (a *Importer) ensureAccounts(ctx context.Context, plan model.Plan, need needs, r *refs, created map[string]int) error {
	specs := accountSpecs(plan.Mapping)

	institutions, err := a.ledger.ListInstitutions.ListInstitutions(ctx, finance.ListInstitutionsInput{IncludeArchived: true})
	if err != nil {
		return model.LedgerFailure(model.VerbList, model.ThingInstitution, "", err)
	}
	haveInstitution := map[string]finance.Institution{}
	for _, in := range institutions.Institutions {
		haveInstitution[in.Name] = in
	}
	accounts, err := a.ledger.ListAccounts.ListAccounts(ctx, finance.ListAccountsInput{IncludeArchived: true})
	if err != nil {
		return model.LedgerFailure(model.VerbList, model.ThingAccount, "", err)
	}
	haveAccount := map[string]finance.Account{}
	for _, acc := range accounts.Accounts {
		haveAccount[acc.Name] = acc
	}

	for _, name := range need.accounts {
		spec := specs[name]
		if acc, ok := haveAccount[name]; ok {
			if acc.ArchivedAt != nil {
				return archived(model.ThingAccount, name)
			}
			if acc.Kind != spec.Kind {
				return &model.Failure{Code: model.FailureAccountKindDiffers, Args: []string{name, acc.Kind, spec.Kind}}
			}
			r.accounts[name] = acc.ID
			continue
		}
		in := finance.CreateAccountInput{Name: name, Kind: spec.Kind}
		if spec.Institution != "" {
			inst, ok := haveInstitution[spec.Institution]
			if !ok {
				made, err := a.ledger.CreateInstitution.CreateInstitution(ctx, finance.CreateInstitutionInput{Name: spec.Institution})
				if err != nil {
					return model.LedgerFailure(model.VerbCreate, model.ThingInstitution, spec.Institution, err)
				}
				inst = made
				haveInstitution[inst.Name] = inst
				created[usecase.CreatedInstitutions]++
			}
			if inst.ArchivedAt != nil {
				return archived(model.ThingInstitution, spec.Institution)
			}
			id := inst.ID
			in.InstitutionID = &id
		}
		if spec.ClosingDay != 0 {
			day := spec.ClosingDay
			in.ClosingDay = &day
		}
		if spec.PaymentDueDay != 0 {
			day := spec.PaymentDueDay
			in.PaymentDueDay = &day
		}
		made, err := a.ledger.CreateAccount.CreateAccount(ctx, in)
		if err != nil {
			return model.LedgerFailure(model.VerbCreate, model.ThingAccount, name, err)
		}
		r.accounts[name] = made.ID
		created[usecase.CreatedAccounts]++
	}
	return nil
}

// categoryKey identifies a category by what the ledger keeps it by.
type categoryKey struct {
	direction string
	parent    uuid.UUID
	name      string
}

func (a *Importer) ensureCategories(ctx context.Context, need needs, r *refs, created map[string]int) error {
	listed, err := a.ledger.ListCategories.ListCategories(ctx, finance.ListCategoriesInput{IncludeArchived: true})
	if err != nil {
		return model.LedgerFailure(model.VerbList, model.ThingCategory, "", err)
	}
	have := map[categoryKey]finance.Category{}
	for _, c := range listed.Categories {
		var parent uuid.UUID
		if c.ParentID != nil {
			parent = *c.ParentID
		}
		have[categoryKey{c.Direction, parent, c.Name}] = c
	}

	ensure := func(direction string, parent *uuid.UUID, name string) (uuid.UUID, error) {
		var parentID uuid.UUID
		if parent != nil {
			parentID = *parent
		}
		key := categoryKey{direction, parentID, name}
		if c, ok := have[key]; ok {
			if c.ArchivedAt != nil {
				return uuid.Nil, archived(model.ThingCategory, name)
			}
			return c.ID, nil
		}
		made, err := a.ledger.CreateCategory.CreateCategory(ctx, finance.CreateCategoryInput{Direction: direction, ParentID: parent, Name: name})
		if err != nil {
			return uuid.Nil, model.LedgerFailure(model.VerbCreate, model.ThingCategory, name, err)
		}
		have[key] = made
		created[usecase.CreatedCategories]++
		return made.ID, nil
	}
	path := func(direction, p string) (uuid.UUID, error) {
		top, child := model.SplitCategory(p)
		topID, err := ensure(direction, nil, top)
		if err != nil {
			return uuid.Nil, err
		}
		if child == "" {
			return topID, nil
		}
		return ensure(direction, &topID, child)
	}
	for _, p := range need.expense {
		id, err := path("expense", p)
		if err != nil {
			return err
		}
		r.expense[p] = id
	}
	for _, p := range need.income {
		id, err := path("income", p)
		if err != nil {
			return err
		}
		r.income[p] = id
	}
	return nil
}

func (a *Importer) ensureAreas(ctx context.Context, need needs, r *refs, created map[string]int) error {
	listed, err := a.ledger.ListAreas.ListAreas(ctx, catalog.ListAreasInput{IncludeArchived: true})
	if err != nil {
		return model.LedgerFailure(model.VerbList, model.ThingArea, "", err)
	}
	have := map[string]catalog.Area{}
	for _, ar := range listed.Areas {
		have[ar.Name] = ar
		r.areaNames[ar.ID] = ar.Name
	}
	for _, name := range need.areas {
		if ar, ok := have[name]; ok {
			if ar.ArchivedAt != nil {
				return archived(model.ThingArea, name)
			}
			r.areas[name] = ar.ID
			continue
		}
		made, err := a.ledger.CreateArea.CreateArea(ctx, catalog.CreateAreaInput{Name: name})
		if err != nil {
			return model.LedgerFailure(model.VerbCreate, model.ThingArea, name, err)
		}
		r.areas[name] = made.ID
		r.areaNames[made.ID] = name
		created[usecase.CreatedAreas]++
	}
	return nil
}

// projectSpec returns what the mapping says the project of the ledger with the name
// is: the entry of the first value of 專案 that makes it, by value.
func projectSpec(m model.Mapping, name string) model.ProjectMap {
	values := make([]string, 0, len(m.Projects))
	for v := range m.Projects {
		values = append(values, v)
	}
	slices.Sort(values)
	for _, v := range values {
		if p := m.Projects[v]; p.Kind == model.ProjectProject && p.Name == name {
			return p
		}
	}
	return model.ProjectMap{Kind: model.ProjectProject, Name: name}
}

func (a *Importer) ensureProjects(ctx context.Context, plan model.Plan, need needs, r *refs, made *ensured) error {
	listed, err := a.ledger.ListProjects.ListProjects(ctx, catalog.ListProjectsInput{IncludeArchived: true})
	if err != nil {
		return model.LedgerFailure(model.VerbList, model.ThingProject, "", err)
	}
	have := map[string]catalog.Project{}
	for _, p := range listed.Projects {
		have[p.Name] = p
	}
	for _, name := range need.projects {
		spec := projectSpec(plan.Mapping, name)
		if p, ok := have[name]; ok {
			if p.ArchivedAt != nil {
				return archived(model.ThingProject, name)
			}
			r.projects[name] = p.ID
			ledgerArea := ""
			if p.DefaultAreaID != nil {
				ledgerArea = r.areaNames[*p.DefaultAreaID]
			}
			if ledgerArea != spec.DefaultArea {
				made.projectDifferences = append(made.projectDifferences, usecase.ProjectDifference{Project: name, Mapping: spec.DefaultArea, Ledger: ledgerArea})
			}
			continue
		}
		// The import never sets the active period of a project: the ledger puts every
		// transaction that has no project of its own into the project that is active on
		// its day, and a period guessed from the rows would take in transactions that are
		// not part of it.
		in := catalog.CreateProjectInput{Name: name}
		if spec.DefaultArea != "" {
			id := r.areas[spec.DefaultArea]
			in.DefaultAreaID = &id
		}
		created, err := a.ledger.CreateProject.CreateProject(ctx, in)
		if err != nil {
			return model.LedgerFailure(model.VerbCreate, model.ThingProject, name, err)
		}
		r.projects[name] = created.ID
		made.created[usecase.CreatedProjects]++
	}
	return nil
}

func (a *Importer) ensureTags(ctx context.Context, need needs, r *refs, created map[string]int) error {
	listed, err := a.ledger.ListTags.ListTags(ctx, catalog.ListTagsInput{IncludeArchived: true})
	if err != nil {
		return model.LedgerFailure(model.VerbList, model.ThingTag, "", err)
	}
	have := map[string]catalog.Tag{}
	for _, t := range listed.Tags {
		have[t.Name] = t
	}
	for _, name := range need.tags {
		if t, ok := have[name]; ok {
			if t.ArchivedAt != nil {
				return archived(model.ThingTag, name)
			}
			r.tags[name] = t.ID
			continue
		}
		made, err := a.ledger.CreateTag.CreateTag(ctx, catalog.CreateTagInput{Name: name})
		if err != nil {
			return model.LedgerFailure(model.VerbCreate, model.ThingTag, name, err)
		}
		r.tags[name] = made.ID
		created[usecase.CreatedTags]++
	}
	return nil
}

func (a *Importer) ensureParties(ctx context.Context, need needs, r *refs, created map[string]int) error {
	listed, err := a.ledger.ListParties.ListParties(ctx, catalog.ListPartiesInput{IncludeArchived: true})
	if err != nil {
		return model.LedgerFailure(model.VerbList, model.ThingParty, "", err)
	}
	have := map[string]catalog.Party{}
	for _, p := range listed.Parties {
		have[p.Name] = p
	}
	for _, name := range need.parties {
		if p, ok := have[name]; ok {
			if p.ArchivedAt != nil {
				return archived(model.ThingParty, name)
			}
			r.parties[name] = p.ID
			continue
		}
		made, err := a.ledger.CreateParty.CreateParty(ctx, catalog.CreatePartyInput{Name: name})
		if err != nil {
			return model.LedgerFailure(model.VerbCreate, model.ThingParty, name, err)
		}
		r.parties[name] = made.ID
		created[usecase.CreatedParties]++
	}
	return nil
}
