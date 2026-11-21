package moze

import (
	"context"
	"fmt"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/domain/service"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/mapping"
)

// loaded is the inputs of an import read, and the plan made of them.
type loaded struct {
	plan       model.Plan
	mapping    mapping.Loaded
	ratesFound bool
}

// load reads the export, the mapping and the rates and makes the plan of them.
func (in Inputs) load(ctx context.Context, files usecase.Files) (loaded, error) {
	rows, err := in.Source.Rows(ctx, files.Export)
	if err != nil {
		return loaded{}, fmt.Errorf("read the export %s: %w", files.Export, err)
	}
	m, err := in.Mapping.Load(ctx, files.Mapping)
	if err != nil {
		return loaded{}, fmt.Errorf("read the mapping %s: %w", files.Mapping, err)
	}
	r, found, err := in.Rates.Load(ctx, files.Rates)
	if err != nil {
		return loaded{}, fmt.Errorf("read the rates %s: %w", files.Rates, err)
	}
	plan := service.BuildPlan(service.PlanInput{Rows: rows, Mapping: m.Mapping, Rates: r})
	return loaded{plan: plan, mapping: m, ratesFound: found}, nil
}

// Plan makes the plan of the export. It writes to the mapping file the entries the
// mapping lacks, as guesses, and to nothing else.
func (p *Planner) Plan(ctx context.Context, in usecase.PlanInput) (usecase.PlanOutput, error) {
	ld, err := p.inputs.load(ctx, in.Files)
	if err != nil {
		return usecase.PlanOutput{}, err
	}
	out := usecase.PlanOutput{Plan: ld.plan, RatesFound: ld.ratesFound, Guessed: ld.mapping.Guessed}
	if ld.plan.Guessed.Empty() {
		return out, nil
	}
	if err := p.inputs.Mapping.Append(ctx, in.Mapping, ld.plan.Guessed); err != nil {
		return usecase.PlanOutput{}, fmt.Errorf("add the guesses to the mapping %s: %w", in.Mapping, err)
	}
	out.MappingCreated = !ld.mapping.Exists
	out.MappingAdded = entries(ld.plan.Guessed)
	out.Guessed += out.MappingAdded
	return out, nil
}

// entries counts the entries of a mapping: every item of every section, and each
// default that is set.
func entries(m model.Mapping) int {
	n := len(m.Instruments) + len(m.Accounts) + len(m.Projects) + len(m.Expense) + len(m.Income) +
		len(m.Parties) + len(m.Tags) + len(m.Rows)
	for _, d := range []string{m.Defaults.FeeCategory, m.Defaults.InterestCategory, m.Defaults.RefundCategory} {
		if d != "" {
			n++
		}
	}
	return n
}
