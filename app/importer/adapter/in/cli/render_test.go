package cli_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/adapter/in/cli"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

// asciiSentence finds three or more words of plain ASCII letters in a row: what an
// English sentence looks like. Keys, codes and invented names of one word are not.
var asciiSentence = regexp.MustCompile(`[A-Za-z]{2,} [A-Za-z]{2,} [A-Za-z]{2,}`)

// everything is a plan with something of every kind the report has words for. Its names
// are single words, so that anything of two words or more in the report is text.
func everything() model.Plan {
	rows := []model.Row{
		{Key: "k1", Pos: 0, Account: "Cash", Currency: "TWD", Type: model.TypeExpense, Amount: dec("-10"), Date: day("2026-01-05"), Clock: "12:00", Name: "Noodles", Project: "Trip"},
		{Key: "k2", Pos: 1, Account: "Cash", Currency: "TWD", Type: model.TypeTransferOut, Amount: dec("-5"), Date: day("2026-01-06")},
		{Key: "k3", Pos: 2, Account: "Bank", Currency: "TWD", Type: model.TypeReceivable, Amount: dec("7"), Discount: dec("1"), Date: day("2026-01-07")},
		{Key: "k4", Pos: 3, Account: "Bank", Currency: "USD", Type: model.TypeInitial, Amount: dec("3")},
	}
	var skipped []model.Skipped
	for i, r := range []model.Reason{
		model.ReasonUnknownType, model.ReasonBadSign, model.ReasonZeroAmount, model.ReasonTooManyDecimals, model.ReasonZeroQuantity,
		model.ReasonNotCurrencies, model.ReasonUnpaired, model.ReasonNoPairing, model.ReasonAmbiguousPair, model.ReasonAmountMismatch,
		model.ReasonSameAccount, model.ReasonInstalmentUnmatched, model.ReasonInstalmentAccounts, model.ReasonSkipped,
		model.ReasonNoDate, model.ReasonNoParty, model.ReasonNoCategory,
	} {
		skipped = append(skipped, model.Skipped{Reason: r, Rows: []string{rows[i%len(rows)].Key}, Args: []string{"USD", "BTC"}})
	}
	var caveats []model.Caveat
	for _, k := range []model.CaveatKind{
		model.CaveatInvestment, model.CaveatBrokerage, model.CaveatDiscountOverPrice, model.CaveatSameInstrumentExchange,
		model.CaveatPendingIncome, model.CaveatRefund,
	} {
		caveats = append(caveats, model.Caveat{Kind: k, Row: "k1"})
	}
	caveats = append(caveats,
		model.Caveat{Kind: model.CaveatOpeningDate, Row: "k4", Args: []string{model.OpeningFromLedgerAccount}},
		model.Caveat{Kind: model.CaveatOpeningDate, Row: "k4", Args: []string{model.OpeningFromFile}},
	)
	var problems []model.Problem
	for _, c := range []struct {
		code    model.ProblemCode
		subject string
		args    []string
	}{
		{model.ProblemNameEmpty, "accounts.A", nil},
		{model.ProblemKindInvalid, "instruments.XYZ", []string{"gold"}},
		{model.ProblemKindInvalid, "accounts.A", []string{"gold"}},
		{model.ProblemKindInvalid, "projects.P", []string{"gold"}},
		{model.ProblemDecimalsRange, "instruments.XYZ", []string{"30"}},
		{model.ProblemDayRange, "accounts.A", []string{"closing_day", "40"}},
		{model.ProblemCardOnlyDays, "accounts.A", nil},
		{model.ProblemDueNeedsClosing, "accounts.A", nil},
		{model.ProblemAccountConflict, "accounts.B", []string{"A", "card", "Inst"}},
		{model.ProblemDealingsNeedsParty, "projects.P", nil},
		{model.ProblemDefaultAreaUnknown, "projects.P", []string{"Nowhere"}},
		{model.ProblemOnlyProjectHasArea, "projects.Q", nil},
		{model.ProblemTreatmentInvalid, "categories.expense.x/y", []string{"mystery"}},
		{model.ProblemTreatmentInvalid, "rows.k1", []string{"mystery"}},
		{model.ProblemCategoryEmpty, "categories.expense.x/y", nil},
		{model.ProblemOverrideNotFit, "rows.k1", []string{"reward", "支出"}},
		{model.ProblemRateMissing, "rates.USD", []string{"USD", "2026-01-05"}},
	} {
		problems = append(problems, model.Problem{Code: c.code, Subject: c.subject, Args: c.args})
	}
	return model.Plan{
		Rows:    rows,
		Mapping: model.Mapping{Projects: map[string]model.ProjectMap{"Trip": {Kind: model.ProjectProject, Name: "Trip", DefaultArea: "Fun"}}},
		Guessed: model.Mapping{
			Accounts: map[string]model.AccountMap{"Cash": {Name: "Cash"}},
			Defaults: model.Defaults{FeeCategory: "Fees"},
		},
		Problems: problems,
		Warnings: []model.Warning{
			{Code: model.WarningOverrideUnused, Subject: "rows.k1"},
			{Code: model.WarningRowUnknown, Subject: "rows.k9"},
		},
		Units: []model.Unit{
			{Kind: model.KindExpense, Date: day("2026-01-05")},
			{Kind: model.KindDealing, Date: day("2026-01-07")},
			{Kind: model.KindBillInstalment, Date: day("2026-01-08")},
		},
		Adjustments: []model.Adjustment{{Date: day("2026-01-09")}},
		NoOps:       []model.NoOp{{Reason: model.NoOpZeroOpening, Row: "k4"}, {Reason: model.NoOpZeroAdjustment, Row: "k4"}},
		Skipped:     skipped,
		Caveats:     caveats,
		Differences: []model.Difference{
			{Unit: "k3", Rows: []string{"k3"}, Reasons: []model.DifferenceReason{model.DifferenceDiscount, model.DifferenceFee, model.DifferenceOther},
				Effects: []model.Effect{{Account: "Bank", Instrument: "TWD", Date: day("2026-01-07"), Quantity: dec("1")}}},
		},
		Projects: []model.ProjectEvidence{
			{Value: "Trip", Rows: 3, First: day("2026-01-05"), Last: day("2026-01-08"), From: day("2026-01-05"), To: day("2026-01-08")},
		},
		Effects: []model.Effect{{Account: "Cash", Instrument: "TWD", Date: day("2026-01-05"), Quantity: dec("-10"), Row: "k1"}},
	}
}

// words the glossary does not allow in what the import says itself: 餘額調整 is MOZE's
// word, and a kind of the ledger is not a 記錄類型.
func checkWording(t *testing.T, what, text string) {
	t.Helper()
	for _, word := range []string{"餘額調整", "記錄類型"} {
		for i := strings.Index(text, word); i >= 0; {
			before := text[:i]
			if !strings.HasSuffix(before, "MOZE 的") && !strings.HasSuffix(before, "MOZE ") {
				t.Errorf("%s says %s without saying it is MOZE's: …%s", what, word, text[max(0, i-20):min(len(text), i+30)])
			}
			next := strings.Index(text[i+len(word):], word)
			if next < 0 {
				break
			}
			i += len(word) + next
		}
	}
}

func TestEverythingThePlanReportSaysIsInChinese(t *testing.T) {
	// The detector finds what it is to find, and lets keys, codes and Chinese through.
	for text, want := range map[string]bool{
		"there is no rate of USD":       true,
		"accounts.Fake Bank: 名稱是空的":     false,
		"先執行 plan（它會寫進對照檔）":             false,
		"rows 以 treatment 指定 # guessed": false,
	} {
		if got := asciiSentence.MatchString(text); got != want {
			t.Fatalf("asciiSentence(%q) = %v, want %v", text, got, want)
		}
	}
	out := usecase.PlanOutput{Plan: everything(), MappingAdded: 4, Guessed: 2, RatesFound: false}
	var report, summary strings.Builder
	if err := cli.RenderPlan(&report, out); err != nil {
		t.Fatal(err)
	}
	if err := cli.RenderPlanSummary(&summary, out, "/tmp/report.md"); err != nil {
		t.Fatal(err)
	}
	for what, text := range map[string]string{"the report": report.String(), "the summary": summary.String()} {
		if m := asciiSentence.FindString(text); m != "" {
			t.Errorf("%s has an English sentence: %q", what, m)
		}
		checkWording(t, what, text)
	}

	// A code the words lack would show as the code.
	for _, code := range []model.ProblemCode{
		model.ProblemNameEmpty, model.ProblemKindInvalid, model.ProblemDecimalsRange, model.ProblemDayRange, model.ProblemCardOnlyDays,
		model.ProblemDueNeedsClosing, model.ProblemAccountConflict, model.ProblemDealingsNeedsParty, model.ProblemDefaultAreaUnknown,
		model.ProblemOnlyProjectHasArea, model.ProblemTreatmentInvalid, model.ProblemCategoryEmpty, model.ProblemOverrideNotFit,
		model.ProblemRateMissing,
	} {
		if strings.Contains(report.String(), string(code)) {
			t.Errorf("the report says the problem %q as its code: it has no words for it", code)
		}
	}
	for _, code := range []model.WarningCode{model.WarningOverrideUnused, model.WarningRowUnknown} {
		if strings.Contains(report.String(), string(code)) {
			t.Errorf("the report says the warning %q as its code", code)
		}
	}
	for _, reason := range []model.Reason{
		model.ReasonUnknownType, model.ReasonBadSign, model.ReasonZeroAmount, model.ReasonTooManyDecimals, model.ReasonZeroQuantity,
		model.ReasonNotCurrencies, model.ReasonUnpaired, model.ReasonNoPairing, model.ReasonAmbiguousPair, model.ReasonAmountMismatch,
		model.ReasonSameAccount, model.ReasonInstalmentUnmatched, model.ReasonInstalmentAccounts, model.ReasonSkipped,
		model.ReasonNoDate, model.ReasonNoParty, model.ReasonNoCategory,
	} {
		if strings.Contains(report.String(), string(reason)+"（") || strings.Contains(report.String(), "### （") {
			t.Errorf("the report has no words for the reason %q", reason)
		}
	}
	for _, want := range []string{"記錄下來、但金額與 CSV 不同的單位", "專案值", "僅供參考", "fee_category"} {
		if !strings.Contains(report.String(), want) {
			t.Errorf("the report lacks %q", want)
		}
	}
	if strings.Contains(report.String(), "進行期間 2026") {
		t.Error("the report presents a suggested active period as something that is set")
	}
}

func TestWhatTheCommandsPrintIsInChinese(t *testing.T) {
	unit := model.Unit{Kind: model.KindExpense, Date: day("2026-03-05"), Description: "Noodles"}
	counting := usecase.Orphan{Key: "moze:k9-1@0123456789ab", Status: "confirmed", Kind: "expense", Date: day("2026-03-02"), Account: "Cash", Instrument: "TWD", Amount: dec("-300"), Description: "Noodles"}
	waiting := usecase.Orphan{Key: "moze:k8-1@0123456789ab", Status: "draft", Kind: "reconciliation", Date: day("2026-03-03"), Account: "Bank", Instrument: "USD", Amount: dec("2.5")}
	warnings := []model.Warning{{Code: model.WarningActivePeriodIgnored, Subject: "projects.Trip", Args: []string{"12"}}}
	plan := everything()
	plan.Guessed = model.Mapping{Accounts: map[string]model.AccountMap{"Cash": {Name: "Cash"}}, Defaults: model.Defaults{RefundCategory: "Refunds"}}

	var b strings.Builder
	for _, err := range []error{
		&usecase.BlockedError{Plan: plan, Guessed: 3},
		&usecase.MonthOrderError{Open: "2026-01", Month: "2026-03", Drafts: 2},
		&usecase.StaleError{Month: "2026-03", Units: []usecase.StaleUnit{{Unit: unit, Lines: []int{4}}}},
		&usecase.OrphanError{Month: "2026-03", Orphans: []usecase.Orphan{counting}},
	} {
		got := cli.Refusal(&b, err)
		if got == nil || asciiSentence.MatchString(got.Error()) {
			t.Errorf("Refusal(%T) = %v, want an error in Chinese", err, got)
		}
	}
	if got := cli.Refusal(&b, usecase.ErrUnknownMonth); got == nil || asciiSentence.MatchString(got.Error()) {
		t.Errorf("Refusal(unknown month) = %v", got)
	}
	other := errors.New("the ledger says no")
	if got := cli.Refusal(&b, other); got != other {
		t.Errorf("Refusal of an error that is not the user's to put right = %v, want it as it is", got)
	}

	check := model.MonthCheck{Month: "2026-03", End: day("2026-03-31"), Decimals: map[string]int{"TWD": 0}, Lines: []model.BalanceLine{
		{Account: "Cash", Instrument: "TWD", Ledger: dec("5"), Expected: dec("10"), Unrecorded: dec("5")},
		{Account: "Bank", Instrument: "TWD", Ledger: dec("5"), Expected: dec("10")},
	}}
	stale := []usecase.StaleUnit{{Unit: unit, Lines: []int{4}}}
	for name, render := range map[string]func() error{
		"apply": func() error {
			return cli.RenderApply(&b, usecase.ApplyOutput{
				Warnings: warnings,
				Created:  map[string]int{usecase.CreatedAccounts: 2},
				New:      map[model.UnitKind]int{model.KindExpense: 2}, Updated: map[model.UnitKind]int{model.KindIncome: 1},
				Revived:   map[model.UnitKind]int{model.KindTransfer: 1},
				Duplicate: map[model.UnitKind]int{model.KindDealing: 1}, Stale: stale,
				Voided:             []usecase.VoidedUnit{{Unit: unit, Lines: []int{6}}},
				Removed:            []usecase.Orphan{waiting},
				Orphans:            []usecase.Orphan{counting},
				Failed:             []usecase.Failure{{Unit: unit, Lines: []int{4}, Err: errors.New("ledger refuses")}},
				ProjectDifferences: []usecase.ProjectDifference{{Project: "Trip", Mapping: "Fun", Ledger: ""}},
				Adjustments:        3, NoOps: 1, Skipped: 2,
			})
		},
		"confirm": func() error {
			return cli.RenderConfirm(&b, usecase.ConfirmOutput{Month: "2026-03", Confirmed: 2, Check: check, Orphans: []usecase.Orphan{waiting}, Warnings: warnings,
				Adjustments: []usecase.AdjustmentResult{{State: usecase.AdjustmentBlocked, Err: errors.New("pending drafts")}, {State: usecase.AdjustmentApplied}}})
		},
		"verify": func() error {
			return cli.RenderVerify(&b, usecase.VerifyOutput{Months: []model.MonthCheck{check}, Open: "2026-04", Unexplained: 1, Stale: stale,
				Orphans: []usecase.Orphan{counting, waiting}, Warnings: warnings})
		},
		"status": func() error {
			return cli.RenderStatus(&b, usecase.StatusOutput{Months: []usecase.MonthStatus{{Month: "2026-03", Drafts: 1, Stale: 1}},
				Orphans: []usecase.Orphan{counting, waiting}, Warnings: warnings})
		},
		"warnings": func() error { return cli.RenderWarnings(&b, warnings) },
	} {
		b.Reset()
		if err := render(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := b.String()
		checkWording(t, name, text)
		// The ledger's own words, in what it replies, are English: they are quoted.
		text = strings.NewReplacer("ledger refuses", "", "pending drafts", "").Replace(text)
		if m := asciiSentence.FindString(text); m != "" {
			t.Errorf("%s has an English sentence: %q", name, m)
		}
	}
}

func TestTheRefusalNamesWhatToPutRight(t *testing.T) {
	plan := model.Plan{Guessed: model.Mapping{
		Accounts: map[string]model.AccountMap{"Cash": {Name: "Cash"}},
		Defaults: model.Defaults{FeeCategory: "Fees"},
	}}
	var b strings.Builder
	err := cli.Refusal(&b, &usecase.BlockedError{Plan: plan, Guessed: 3})
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"帳戶：1 個", "預設類別：1 個（fee_category）", "3 個標了 # guessed"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the refusal does not say %q:\n%s", want, b.String())
		}
	}

	// Nothing to map and nothing wrong, but the mapping still has guesses: the error says so.
	b.Reset()
	err = cli.Refusal(&b, &usecase.BlockedError{Guessed: 3})
	if err == nil || !strings.Contains(err.Error(), "3 個標了 # guessed") {
		t.Errorf("the refusal of a mapping with guesses = %v, want it to say how many", err)
	}
}
