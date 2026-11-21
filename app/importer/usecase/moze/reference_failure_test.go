package moze_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	catalog "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/moze"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// errReply is what the ledger answers when it does not take a step, in its own words.
var errReply = errors.New("the ledger says no")

// refusingLedger is the fake ledger that does not take one step of making the reference
// data, "list instrument" or "create tag": it answers that step with err, whatever it
// is asked, and does the others as the fake does.
type refusingLedger struct {
	*fakeLedger
	step string
	err  error
}

func (r refusingLedger) refuses(verb usecase.Verb, thing usecase.Thing) bool {
	return r.step == string(verb)+" "+string(thing)
}

// ledger is the ledger of the import, with every step of the reference data going
// through r.
func (r refusingLedger) ledger() moze.Ledger {
	l := r.fakeLedger.ledger()
	l.ListInstruments, l.CreateInstrument = r, r
	l.ListInstitutions, l.CreateInstitution = r, r
	l.ListAccounts, l.CreateAccount = r, r
	l.ListCategories, l.CreateCategory = r, r
	l.ListAreas, l.CreateArea = r, r
	l.ListProjects, l.CreateProject = r, r
	l.ListTags, l.CreateTag = r, r
	l.ListParties, l.CreateParty = r, r
	return l
}

func (r refusingLedger) ListInstruments(ctx context.Context, in finance.ListInstrumentsInput) (finance.ListInstrumentsOutput, error) {
	if r.refuses(usecase.VerbList, usecase.ThingInstrument) {
		return finance.ListInstrumentsOutput{}, r.err
	}
	return r.fakeLedger.ListInstruments(ctx, in)
}

func (r refusingLedger) CreateInstrument(ctx context.Context, in finance.CreateInstrumentInput) (finance.Instrument, error) {
	if r.refuses(usecase.VerbCreate, usecase.ThingInstrument) {
		return finance.Instrument{}, r.err
	}
	return r.fakeLedger.CreateInstrument(ctx, in)
}

func (r refusingLedger) ListInstitutions(ctx context.Context, in finance.ListInstitutionsInput) (finance.ListInstitutionsOutput, error) {
	if r.refuses(usecase.VerbList, usecase.ThingInstitution) {
		return finance.ListInstitutionsOutput{}, r.err
	}
	return r.fakeLedger.ListInstitutions(ctx, in)
}

func (r refusingLedger) CreateInstitution(ctx context.Context, in finance.CreateInstitutionInput) (finance.Institution, error) {
	if r.refuses(usecase.VerbCreate, usecase.ThingInstitution) {
		return finance.Institution{}, r.err
	}
	return r.fakeLedger.CreateInstitution(ctx, in)
}

func (r refusingLedger) ListAccounts(ctx context.Context, in finance.ListAccountsInput) (finance.ListAccountsOutput, error) {
	if r.refuses(usecase.VerbList, usecase.ThingAccount) {
		return finance.ListAccountsOutput{}, r.err
	}
	return r.fakeLedger.ListAccounts(ctx, in)
}

func (r refusingLedger) CreateAccount(ctx context.Context, in finance.CreateAccountInput) (finance.Account, error) {
	if r.refuses(usecase.VerbCreate, usecase.ThingAccount) {
		return finance.Account{}, r.err
	}
	return r.fakeLedger.CreateAccount(ctx, in)
}

func (r refusingLedger) ListCategories(ctx context.Context, in finance.ListCategoriesInput) (finance.ListCategoriesOutput, error) {
	if r.refuses(usecase.VerbList, usecase.ThingCategory) {
		return finance.ListCategoriesOutput{}, r.err
	}
	return r.fakeLedger.ListCategories(ctx, in)
}

func (r refusingLedger) CreateCategory(ctx context.Context, in finance.CreateCategoryInput) (finance.Category, error) {
	if r.refuses(usecase.VerbCreate, usecase.ThingCategory) {
		return finance.Category{}, r.err
	}
	return r.fakeLedger.CreateCategory(ctx, in)
}

func (r refusingLedger) ListAreas(ctx context.Context, in catalog.ListAreasInput) (catalog.ListAreasOutput, error) {
	if r.refuses(usecase.VerbList, usecase.ThingArea) {
		return catalog.ListAreasOutput{}, r.err
	}
	return r.fakeLedger.ListAreas(ctx, in)
}

func (r refusingLedger) CreateArea(ctx context.Context, in catalog.CreateAreaInput) (catalog.Area, error) {
	if r.refuses(usecase.VerbCreate, usecase.ThingArea) {
		return catalog.Area{}, r.err
	}
	return r.fakeLedger.CreateArea(ctx, in)
}

func (r refusingLedger) ListProjects(ctx context.Context, in catalog.ListProjectsInput) (catalog.ListProjectsOutput, error) {
	if r.refuses(usecase.VerbList, usecase.ThingProject) {
		return catalog.ListProjectsOutput{}, r.err
	}
	return r.fakeLedger.ListProjects(ctx, in)
}

func (r refusingLedger) CreateProject(ctx context.Context, in catalog.CreateProjectInput) (catalog.Project, error) {
	if r.refuses(usecase.VerbCreate, usecase.ThingProject) {
		return catalog.Project{}, r.err
	}
	return r.fakeLedger.CreateProject(ctx, in)
}

func (r refusingLedger) ListTags(ctx context.Context, in catalog.ListTagsInput) (catalog.ListTagsOutput, error) {
	if r.refuses(usecase.VerbList, usecase.ThingTag) {
		return catalog.ListTagsOutput{}, r.err
	}
	return r.fakeLedger.ListTags(ctx, in)
}

func (r refusingLedger) CreateTag(ctx context.Context, in catalog.CreateTagInput) (catalog.Tag, error) {
	if r.refuses(usecase.VerbCreate, usecase.ThingTag) {
		return catalog.Tag{}, r.err
	}
	return r.fakeLedger.CreateTag(ctx, in)
}

func (r refusingLedger) ListParties(ctx context.Context, in catalog.ListPartiesInput) (catalog.ListPartiesOutput, error) {
	if r.refuses(usecase.VerbList, usecase.ThingParty) {
		return catalog.ListPartiesOutput{}, r.err
	}
	return r.fakeLedger.ListParties(ctx, in)
}

func (r refusingLedger) CreateParty(ctx context.Context, in catalog.CreatePartyInput) (catalog.Party, error) {
	if r.refuses(usecase.VerbCreate, usecase.ThingParty) {
		return catalog.Party{}, r.err
	}
	return r.fakeLedger.CreateParty(ctx, in)
}

// referenceWorld is the import of the fixture on an empty ledger, with a row in a
// project of the ledger, which the fixture has none of, so that every kind of
// reference data the import makes is in use.
type referenceWorld struct {
	*world
	rows []model.Row
}

func newReferenceWorld(t *testing.T) referenceWorld {
	t.Helper()
	rows := fixtureWith(row("2026-01-08 10:00", model.TypeExpense, "Card", "TWD", "-50", cat("Food", "Lunch"), project("Trip")))
	m := fixtureMapping()
	m.Projects["Trip"] = model.ProjectMap{Kind: model.ProjectProject, Name: "Trip"}
	return referenceWorld{world: newWorld(t, rows, m).reviewed(), rows: rows}
}

// applyOn applies the export on the ledger l, which is the fake or has a step refused.
func (rw referenceWorld) applyOn(l moze.Ledger) error {
	inputs := moze.Inputs{Source: fakeSource{rows: rw.rows}, Mapping: rw.mapping, Rates: fakeRates{fixtureRates()}}
	imp := moze.NewImporter(inputs, l, time.FixedZone("UTC+8", 8*3600))
	_, err := imp.Apply(context.Background(), usecase.ApplyInput{Files: rw.files})
	return err
}

// failureAfterApply applies on the world's ledger as it is, and returns the failure it
// stopped with.
func (rw referenceWorld) failureAfterApply(t *testing.T) *usecase.LedgerFailure {
	t.Helper()
	err := rw.applyOn(rw.ledger.ledger())
	var f *usecase.LedgerFailure
	if !errors.As(err, &f) {
		t.Fatalf("Apply = %v, want a *usecase.LedgerFailure", err)
	}
	return f
}

// madeFirst is the ledger after applying the export on an empty one: its first
// instrument (the second, after TWD), institution, account, category, area, project,
// tag and party are the first that the import made of each kind, which is what the first
// step of making one of the kind is about.
func madeFirst(t *testing.T) *fakeLedger {
	t.Helper()
	w := newReferenceWorld(t)
	w.apply()
	l := w.ledger
	if len(l.instruments) < 2 || len(l.institutions) == 0 || len(l.accounts) == 0 || len(l.categories) == 0 ||
		len(l.areas) == 0 || len(l.projects) == 0 || len(l.tags) == 0 || len(l.parties) == 0 {
		t.Fatalf("the export made %d instruments, %d institutions, %d accounts, %d categories, %d areas, %d projects, %d tags and %d parties, want all kinds in use",
			len(l.instruments), len(l.institutions), len(l.accounts), len(l.categories), len(l.areas), len(l.projects), len(l.tags), len(l.parties))
	}
	return l
}

func wantFailure(t *testing.T, f *usecase.LedgerFailure, code usecase.LedgerFailureCode, args ...string) {
	t.Helper()
	if f.Code != code || !slices.Equal(f.Args, args) {
		t.Errorf("failure = %s with %q, want %s with %q", f.Code, f.Args, code, args)
	}
}

// A step that the ledger does not take is a failure that says which step: the verb, what
// it was about, and its name when it is about one, with the reply of the ledger as it
// was given. There is no sentence of the import in it.
func TestAStepTheLedgerDoesNotTakeIsAFailureThatHasItsReply(t *testing.T) {
	plain := madeFirst(t)
	steps := []struct {
		verb  usecase.Verb
		thing usecase.Thing
		name  string // what the step is about: none for a list
	}{
		{usecase.VerbList, usecase.ThingInstrument, ""},
		{usecase.VerbCreate, usecase.ThingInstrument, plain.instruments[1].Code},
		{usecase.VerbList, usecase.ThingInstitution, ""},
		{usecase.VerbCreate, usecase.ThingInstitution, plain.institutions[0].Name},
		{usecase.VerbList, usecase.ThingAccount, ""},
		{usecase.VerbCreate, usecase.ThingAccount, plain.accounts[0].Name},
		{usecase.VerbList, usecase.ThingCategory, ""},
		{usecase.VerbCreate, usecase.ThingCategory, plain.categories[0].Name},
		{usecase.VerbList, usecase.ThingArea, ""},
		{usecase.VerbCreate, usecase.ThingArea, plain.areas[0].Name},
		{usecase.VerbList, usecase.ThingProject, ""},
		{usecase.VerbCreate, usecase.ThingProject, plain.projects[0].Name},
		{usecase.VerbList, usecase.ThingTag, ""},
		{usecase.VerbCreate, usecase.ThingTag, plain.tags[0].Name},
		{usecase.VerbList, usecase.ThingParty, ""},
		{usecase.VerbCreate, usecase.ThingParty, plain.parties[0].Name},
	}
	for _, st := range steps {
		t.Run(string(st.verb)+" "+string(st.thing), func(t *testing.T) {
			rw := newReferenceWorld(t)
			refusing := refusingLedger{fakeLedger: rw.ledger, step: string(st.verb) + " " + string(st.thing), err: errReply}
			err := rw.applyOn(refusing.ledger())
			var f *usecase.LedgerFailure
			if !errors.As(err, &f) {
				t.Fatalf("Apply = %v, want a *usecase.LedgerFailure", err)
			}
			wantFailure(t, f, usecase.LedgerStepFailed, string(st.verb), string(st.thing), st.name)
			if !errors.Is(f.Err, errReply) || f.Err.Error() != errReply.Error() {
				t.Errorf("Err = %v, want the reply of the ledger as it was given", f.Err)
			}
		})
	}
}

// Reference data that the mapping names and the ledger has archived is not used and is
// not made again, since it has the name: the failure says which it is.
func TestReferenceDataTheLedgerHasArchivedIsAFailureThatNamesIt(t *testing.T) {
	plain := madeFirst(t)
	when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		thing usecase.Thing
		name  string
		seed  func(l *fakeLedger) // puts it in the ledger, archived
	}{
		{usecase.ThingInstrument, plain.instruments[1].Code, func(l *fakeLedger) {
			i := plain.instruments[1]
			i.ArchivedAt = &when
			l.instruments = append(l.instruments, i)
		}},
		{usecase.ThingInstitution, plain.institutions[0].Name, func(l *fakeLedger) {
			i := plain.institutions[0]
			i.ArchivedAt = &when
			l.institutions = append(l.institutions, i)
		}},
		{usecase.ThingAccount, plain.accounts[0].Name, func(l *fakeLedger) {
			a := plain.accounts[0]
			a.ArchivedAt = &when
			l.accounts = append(l.accounts, a)
		}},
		{usecase.ThingCategory, plain.categories[0].Name, func(l *fakeLedger) {
			c := plain.categories[0]
			c.ArchivedAt = &when
			l.categories = append(l.categories, c)
		}},
		{usecase.ThingArea, plain.areas[0].Name, func(l *fakeLedger) {
			a := plain.areas[0]
			a.ArchivedAt = &when
			l.areas = append(l.areas, a)
		}},
		{usecase.ThingProject, plain.projects[0].Name, func(l *fakeLedger) {
			p := plain.projects[0]
			p.ArchivedAt = &when
			l.projects = append(l.projects, p)
		}},
		{usecase.ThingTag, plain.tags[0].Name, func(l *fakeLedger) {
			tag := plain.tags[0]
			tag.ArchivedAt = &when
			l.tags = append(l.tags, tag)
		}},
		{usecase.ThingParty, plain.parties[0].Name, func(l *fakeLedger) {
			p := plain.parties[0]
			p.ArchivedAt = &when
			l.parties = append(l.parties, p)
		}},
	}
	for _, tt := range tests {
		t.Run(string(tt.thing), func(t *testing.T) {
			rw := newReferenceWorld(t)
			tt.seed(rw.ledger)
			wantFailure(t, rw.failureAfterApply(t), usecase.LedgerArchived, string(tt.thing), tt.name)
		})
	}
}

// An instrument or an account that the ledger has with the name, and as another thing
// than the mapping says, is a conflict for the user to put right: the failure has what
// each of them says, so that the text can set them side by side.
func TestAnInstrumentOrAnAccountTheLedgerHasOtherwiseIsAFailureThatSaysBoth(t *testing.T) {
	plain := madeFirst(t)
	flag := func(isCurrency bool) string {
		if isCurrency {
			return "currency"
		}
		return "not_currency"
	}
	mapped := plain.instruments[1]
	instrument := func(other finance.Instrument) []string {
		return []string{
			mapped.Code,
			other.Kind, strconv.Itoa(other.Decimals), flag(other.IsCurrency),
			mapped.Kind, strconv.Itoa(mapped.Decimals), flag(mapped.IsCurrency),
		}
	}
	tests := []struct {
		name   string
		change func(*finance.Instrument)
	}{
		{"decimals", func(i *finance.Instrument) { i.Decimals++ }},
		{"a currency that is not one in the mapping", func(i *finance.Instrument) { i.IsCurrency = !i.IsCurrency }},
		{"a kind", func(i *finance.Instrument) { i.Kind = "boat" }},
	}
	for _, tt := range tests {
		t.Run("an instrument that differs in "+tt.name, func(t *testing.T) {
			rw := newReferenceWorld(t)
			other := mapped
			tt.change(&other)
			rw.ledger.instruments = append(rw.ledger.instruments, other)
			wantFailure(t, rw.failureAfterApply(t), usecase.LedgerInstrumentDiffers, instrument(other)...)
		})
	}
	t.Run("an account of another kind", func(t *testing.T) {
		rw := newReferenceWorld(t)
		other := plain.accounts[0]
		other.Kind = "boat"
		rw.ledger.accounts = append(rw.ledger.accounts, other)
		wantFailure(t, rw.failureAfterApply(t), usecase.LedgerAccountKindDiffers, other.Name, "boat", plain.accounts[0].Kind)
	})
}
