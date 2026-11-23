package web

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// need says which reference lists a page reads. A page reads each list once, in one
// call, however many rows it shows: that is what keeps the calls of a page the same
// when it has ten rows and when it has ten thousand.
type need uint

const (
	needAccounts need = 1 << iota
	needInstitutions
	needCategories
	needAreas
	needProjects
	needParties
	needTags
)

// refs holds the names the rows of a page point at, archived entries included: what
// an old transaction refers to is still shown by its name. The instruments are
// always read, for every amount is written with the places and the code of its
// instrument.
type refs struct {
	accounts     []usecase.Account
	institutions []usecase.Institution
	instruments  map[uuid.UUID]usecase.Instrument
	categories   map[uuid.UUID]usecase.Category
	names        map[uuid.UUID]string // areas, projects, parties, tags and institutions, by id
	accountByID  map[uuid.UUID]usecase.Account
}

// loadRefs reads the lists the page needs, and the instruments.
func (h *handlers) loadRefs(ctx context.Context, which need) (*refs, error) {
	r := &refs{
		instruments: make(map[uuid.UUID]usecase.Instrument),
		categories:  make(map[uuid.UUID]usecase.Category),
		names:       make(map[uuid.UUID]string),
		accountByID: make(map[uuid.UUID]usecase.Account),
	}
	instruments, err := h.p.ListInstruments.ListInstruments(ctx, usecase.ListInstrumentsInput{IncludeArchived: true})
	if err != nil {
		return nil, fmt.Errorf("list instruments: %w", err)
	}
	for _, i := range instruments.Instruments {
		r.instruments[i.ID] = i
	}

	if which&needAccounts != 0 {
		out, err := h.p.ListAccounts.ListAccounts(ctx, usecase.ListAccountsInput{IncludeArchived: true})
		if err != nil {
			return nil, fmt.Errorf("list accounts: %w", err)
		}
		r.accounts = out.Accounts
		for _, a := range out.Accounts {
			r.accountByID[a.ID] = a
		}
	}
	if which&needInstitutions != 0 {
		out, err := h.p.ListInstitutions.ListInstitutions(ctx, usecase.ListInstitutionsInput{IncludeArchived: true})
		if err != nil {
			return nil, fmt.Errorf("list institutions: %w", err)
		}
		r.institutions = out.Institutions
		for _, i := range out.Institutions {
			r.names[i.ID] = i.Name
		}
	}
	if which&needCategories != 0 {
		out, err := h.p.ListCategories.ListCategories(ctx, usecase.ListCategoriesInput{IncludeArchived: true})
		if err != nil {
			return nil, fmt.Errorf("list categories: %w", err)
		}
		for _, c := range out.Categories {
			r.categories[c.ID] = c
		}
	}
	if which&needAreas != 0 {
		out, err := h.p.ListAreas.ListAreas(ctx, catalogusecase.ListAreasInput{IncludeArchived: true})
		if err != nil {
			return nil, fmt.Errorf("list areas: %w", err)
		}
		for _, a := range out.Areas {
			r.names[a.ID] = a.Name
		}
	}
	if which&needProjects != 0 {
		out, err := h.p.ListProjects.ListProjects(ctx, catalogusecase.ListProjectsInput{IncludeArchived: true})
		if err != nil {
			return nil, fmt.Errorf("list projects: %w", err)
		}
		for _, p := range out.Projects {
			r.names[p.ID] = p.Name
		}
	}
	if which&needParties != 0 {
		out, err := h.p.ListParties.ListParties(ctx, catalogusecase.ListPartiesInput{IncludeArchived: true})
		if err != nil {
			return nil, fmt.Errorf("list parties: %w", err)
		}
		for _, p := range out.Parties {
			r.names[p.ID] = p.Name
		}
	}
	if which&needTags != 0 {
		out, err := h.p.ListTags.ListTags(ctx, catalogusecase.ListTagsInput{IncludeArchived: true})
		if err != nil {
			return nil, fmt.Errorf("list tags: %w", err)
		}
		for _, t := range out.Tags {
			r.names[t.ID] = t.Name
		}
	}
	return r, nil
}

// name is the name of an area, project, party, tag or institution; an id the list
// does not have is shown as the id.
func (r *refs) name(id uuid.UUID) string {
	if name, ok := r.names[id]; ok {
		return name
	}
	return id.String()
}

// optionalName is name of an id that may be missing: empty for none.
func (r *refs) optionalName(id *uuid.UUID) string {
	if id == nil {
		return ""
	}
	return r.name(*id)
}

func (r *refs) accountName(id uuid.UUID) string {
	if a, ok := r.accountByID[id]; ok {
		return a.Name
	}
	return id.String()
}

func (r *refs) categoryName(id uuid.UUID) string {
	if c, ok := r.categories[id]; ok {
		return c.Name
	}
	return id.String()
}

// amountView is an amount as a page writes it.
type amountView struct {
	// Text is the number, with the places of the instrument, the commas between its
	// thousands and a minus sign when it is negative.
	Text string
	// Unit is the code of the instrument.
	Unit string
	// Negative is whether the amount is negative: a page marks it with the sign in
	// Text, and may add a colour.
	Negative bool
}

// money writes a quantity of an instrument.
func (r *refs) money(instrument uuid.UUID, quantity decimal.Decimal) amountView {
	i, decimals := r.instrument(instrument)
	return amountView{Text: webserver.FormatAmount(quantity, decimals), Unit: i.Code, Negative: quantity.IsNegative()}
}

// signed writes a quantity that moves something, with a plus sign when it adds.
func (r *refs) signed(instrument uuid.UUID, quantity decimal.Decimal) amountView {
	i, decimals := r.instrument(instrument)
	return amountView{Text: webserver.FormatSigned(quantity, decimals), Unit: i.Code, Negative: quantity.IsNegative()}
}

// taken writes a quantity that is taken out of a total: with a minus sign, which says
// what is done with it and not that it is a loss, so it has no colour for one.
func (r *refs) taken(instrument uuid.UUID, quantity decimal.Decimal) amountView {
	i, decimals := r.instrument(instrument)
	return amountView{Text: webserver.FormatAmount(quantity.Neg(), decimals), Unit: i.Code}
}

// plain writes a quantity as a figure that is only read beside another, with its minus sign
// when it is negative and no colour for a loss: what a balance was before the drafts were
// counted in is a reference, and not what it is.
func (r *refs) plain(instrument uuid.UUID, quantity decimal.Decimal) amountView {
	i, decimals := r.instrument(instrument)
	return amountView{Text: webserver.FormatAmount(quantity, decimals), Unit: i.Code}
}

// base writes an amount of the base currency.
func (r *refs) base(amount decimal.Decimal) amountView {
	return r.money(model.BaseCurrencyID, amount)
}

// instrument returns the instrument and how many places its amounts have. An
// instrument the list does not have is shown by its id, with two places.
func (r *refs) instrument(id uuid.UUID) (usecase.Instrument, int) {
	if i, ok := r.instruments[id]; ok {
		return i, i.Decimals
	}
	return usecase.Instrument{ID: id, Code: id.String()}, 2
}

// code is the code of an instrument.
func (r *refs) code(id uuid.UUID) string {
	i, _ := r.instrument(id)
	return i.Code
}
