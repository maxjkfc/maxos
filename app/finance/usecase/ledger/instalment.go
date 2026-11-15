package ledger

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

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
	return toInstalmentPlan(plan), nil
}

// toInstalmentPlan returns the plan as the use cases return it.
func toInstalmentPlan(plan model.InstalmentPlan) usecase.InstalmentPlan {
	out := usecase.InstalmentPlan{Origin: toTransaction(plan.Origin), Periods: make([]usecase.Transaction, len(plan.Periods))}
	for i, period := range plan.Periods {
		out.Periods[i] = toTransaction(period)
	}
	return out
}

// planOf makes the plan of the transactions of one: its origin, and its periods in
// the order of their positions.
func planOf(txns []model.Transaction) (model.InstalmentPlan, error) {
	var plan model.InstalmentPlan
	found := false
	for _, txn := range txns {
		if txn.IsPlanOrigin() {
			plan.Origin, found = txn, true
			continue
		}
		plan.Periods = append(plan.Periods, txn)
	}
	if !found {
		return model.InstalmentPlan{}, model.ErrNotFound
	}
	slices.SortFunc(plan.Periods, func(a, b model.Transaction) int { return cmp.Compare(*a.PlanSeq, *b.PlanSeq) })
	return plan, nil
}

// RescheduleInstalment replaces the schedule of a plan. It locks the plan as the
// lifecycle does (lockActed), has the domain make the periods of the new schedule
// from the plan as it stands once locked, and then, in the one database
// transaction, deletes the old periods and stores the new ones, which it confirms
// when the plan is confirmed: as every transaction is stored a draft, and then
// confirmed. The origin is not touched. Everything the domain refuses is refused
// before anything is written.
func (s *Service) RescheduleInstalment(ctx context.Context, in usecase.RescheduleInstalmentInput) (usecase.InstalmentPlan, error) {
	var out usecase.InstalmentPlan
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		// An id that is not that of a plan is not found, as it is for GetInstalmentPlan.
		// This read takes no lock: what the plan is made of is settled by the lock of
		// its origin.
		if _, err := s.transactions.GetPlan(ctx, in.PlanID); err != nil {
			return fmt.Errorf("instalment plan %s: %w", in.PlanID, err)
		}
		acted, problems, err := s.lockActed(ctx, []uuid.UUID{in.PlanID})
		if err != nil {
			return err
		}
		if len(problems) > 0 {
			return errors.Join(problems...)
		}
		plan, err := planOf(acted)
		if err != nil {
			return fmt.Errorf("instalment plan %s: %w", in.PlanID, err)
		}

		instrumentID, ok := plan.InstrumentID()
		if !ok {
			return fmt.Errorf("instalment plan %s: %w: the origin has no instalment posting", in.PlanID, model.ErrMalformedPlan)
		}
		instrument, err := s.instrument(ctx, instrumentID)
		if err != nil {
			return err
		}
		base, err := s.baseCurrency(ctx, instrument)
		if err != nil {
			return err
		}
		schedule, err := s.schedule(ctx, in.Schedule)
		if err != nil {
			return err
		}
		next, err := plan.Reschedule(model.RescheduleParams{
			Instrument: instrument, BaseCurrency: base, Schedule: schedule, Today: s.today(), ChangePast: in.ChangePast,
		})
		if err != nil {
			return err
		}

		for _, period := range plan.Periods {
			if err := s.transactions.Delete(ctx, period.ID); err != nil {
				return fmt.Errorf("delete period %s: %w", period.ID, err)
			}
		}
		for _, period := range next.Periods {
			if _, err := s.transactions.Insert(ctx, period); err != nil {
				return fmt.Errorf("store period %s: %w", period.ID, err)
			}
			if plan.Origin.Status != model.StatusConfirmed {
				continue
			}
			confirmed, err := period.Confirm()
			if err != nil {
				return fmt.Errorf("confirm period %s: %w", period.ID, err)
			}
			if _, err := s.transactions.SetStatus(ctx, confirmed.ID, confirmed.Status); err != nil {
				return fmt.Errorf("confirm period %s: %w", period.ID, err)
			}
		}

		stored, err := s.transactions.GetPlan(ctx, in.PlanID)
		if err != nil {
			return fmt.Errorf("get instalment plan %s: %w", in.PlanID, err)
		}
		out = toInstalmentPlan(stored)
		return nil
	})
	if err != nil {
		return usecase.InstalmentPlan{}, err
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
			PlanID:       plan.PlanID,
			AccountID:    plan.AccountID,
			InstrumentID: plan.InstrumentID,
			Principal:    plan.Principal,
			Charged:      plan.Charged,
			Remaining:    plan.Remaining(),
			NextDueOn:    clone(plan.NextDueOn),
		})
	}
	return out, nil
}
