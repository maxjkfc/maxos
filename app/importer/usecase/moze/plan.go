package moze

import (
	"context"
	"errors"
	"slices"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/domain/service"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/filefailure"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/mapping"
)

// loaded is the inputs of an import read, and the plan made of them.
type loaded struct {
	plan       model.Plan
	mapping    mapping.Loaded
	ratesFound bool
}

// fileFailure is what a port's error comes to when it is about a file. The adapters
// give a *filefailure.Failure that names the file, and that is returned as it is, and so is
// the end of the context, which is not the fault of the file. An error that is neither
// is made the failure of the file with that code, with the error as its reason: no
// sentence of this layer is put in front of the user.
func fileFailure(code filefailure.Code, file string, err error) error {
	var failure *filefailure.Failure
	if errors.As(err, &failure) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &filefailure.Failure{Code: code, File: file, Err: err}
}

// load reads the export, the mapping and the rates and makes the plan of them.
func (in Inputs) load(ctx context.Context, files usecase.Files) (loaded, error) {
	rows, err := in.Source.Rows(ctx, files.Export)
	if err != nil {
		return loaded{}, fileFailure(filefailure.Unreadable, files.Export, err)
	}
	m, err := in.Mapping.Load(ctx, files.Mapping)
	if err != nil {
		return loaded{}, fileFailure(filefailure.Unreadable, files.Mapping, err)
	}
	r, found, err := in.Rates.Load(ctx, files.Rates)
	if err != nil {
		return loaded{}, fileFailure(filefailure.Unreadable, files.Rates, err)
	}
	plan := service.BuildPlan(service.PlanInput{Rows: rows, Mapping: m.Mapping, Rates: r})
	// What the file says that is odd comes before what the plan says of its rows.
	plan.Warnings = slices.Concat(m.Warnings, plan.Warnings)
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
		return usecase.PlanOutput{}, fileFailure(filefailure.NotWritten, in.Mapping, err)
	}
	out.MappingCreated = !ld.mapping.Exists
	out.MappingAdded = ld.plan.Guessed.Entries()
	out.Guessed += out.MappingAdded
	return out, nil
}
