package ledger

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// RecordBillInstalment loads the account, the instrument, the fees and the interest
// category the request names, and records the conversion of what the account owes
// into an instalment plan: the origin and a transaction for each period, all
// drafts in one database transaction, as recordPlan does.
func (s *Service) RecordBillInstalment(ctx context.Context, in usecase.RecordBillInstalmentInput) (usecase.RecordOutput, error) {
	return s.recordPlan(ctx, in.RecordDetails, func(ctx context.Context, h model.Header) (model.InstalmentPlan, error) {
		account, err := s.account(ctx, in.AccountID)
		if err != nil {
			return model.InstalmentPlan{}, err
		}
		instrument, err := s.instrument(ctx, in.InstrumentID)
		if err != nil {
			return model.InstalmentPlan{}, err
		}
		base, err := s.baseCurrency(ctx, instrument)
		if err != nil {
			return model.InstalmentPlan{}, err
		}
		fees, err := s.fees(ctx, in.Fees)
		if err != nil {
			return model.InstalmentPlan{}, err
		}
		schedule, err := s.schedule(ctx, in.Schedule)
		if err != nil {
			return model.InstalmentPlan{}, err
		}
		return model.NewBillInstalment(model.BillInstalmentParams{
			Header:       h,
			Account:      account,
			Instrument:   instrument,
			BaseCurrency: base,
			Principal:    in.Principal,
			Schedule:     schedule,
			Fees:         fees,
		})
	})
}

// schedule returns the schedule of a request as the domain takes it: the interest
// category loaded, when the request has one, and the id of the transaction of each
// period made. Whether the category can be used, and whether the periods add up,
// is for the domain.
func (s *Service) schedule(ctx context.Context, in usecase.InstalmentSchedule) (model.InstalmentSchedule, error) {
	out := model.InstalmentSchedule{Periods: make([]model.InstalmentPeriod, len(in.Periods))}
	if in.InterestCategoryID != nil {
		category, err := s.category(ctx, *in.InterestCategoryID)
		if err != nil {
			return model.InstalmentSchedule{}, fmt.Errorf("interest: %w", err)
		}
		out.InterestCategory = &category
	}
	for i, period := range in.Periods {
		id, err := uuid.NewV7()
		if err != nil {
			return model.InstalmentSchedule{}, fmt.Errorf("generate the id of period %d: %w", i+1, err)
		}
		out.Periods[i] = model.InstalmentPeriod{
			ID:                 id,
			DueOn:              period.DueOn,
			Principal:          period.Principal,
			Interest:           period.Interest,
			InterestBaseAmount: period.InterestBaseAmount,
		}
	}
	return out, nil
}

// GetInstalmentPlan returns the origin of the plan and its periods in the order
// they fall due, whatever their status. An id that is not that of a plan, such as
// the id of a period, is model.ErrNotFound.
func (s *Service) GetInstalmentPlan(ctx context.Context, planID uuid.UUID) (usecase.InstalmentPlan, error) {
	plan, err := s.transactions.GetPlan(ctx, planID)
	if err != nil {
		return usecase.InstalmentPlan{}, fmt.Errorf("instalment plan %s: %w", planID, err)
	}
	out := usecase.InstalmentPlan{Origin: toTransaction(plan.Origin), Periods: make([]usecase.Transaction, len(plan.Periods))}
	for i, period := range plan.Periods {
		out.Periods[i] = toTransaction(period)
	}
	return out, nil
}

// ListInstalmentPlans returns where each confirmed plan stands today: what the
// repo adds up from the confirmed plans and their periods, as of the day the
// clock says. A plan is finished when nothing remains to charge on that day, which
// is when all its periods have fallen due; those are left out unless the request
// asks for them.
func (s *Service) ListInstalmentPlans(ctx context.Context, in usecase.ListInstalmentPlansInput) (usecase.ListInstalmentPlansOutput, error) {
	plans, err := s.transactions.InstalmentPlans(ctx, in.AccountID, s.today())
	if err != nil {
		return usecase.ListInstalmentPlansOutput{}, fmt.Errorf("list instalment plans: %w", err)
	}
	out := usecase.ListInstalmentPlansOutput{Plans: make([]usecase.InstalmentPlanSummary, 0, len(plans))}
	for _, plan := range plans {
		if plan.Finished() && !in.IncludeFinished {
			continue
		}
		out.Plans = append(out.Plans, usecase.InstalmentPlanSummary{
			PlanID:             plan.PlanID,
			AccountID:          plan.AccountID,
			InstrumentID:       plan.InstrumentID,
			Principal:          plan.Principal,
			Charged:            plan.Charged,
			UnbilledInstalment: plan.UnbilledInstalment(),
			NextDueOn:          clone(plan.NextDueOn),
		})
	}
	return out, nil
}
