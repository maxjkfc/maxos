package service_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// anEnglishSentence is a run of three words of letters: what a plan must not say. It
// says codes, names, keys and numbers, and what it writes to the ledger is Chinese; the
// words of a sentence for the user are the business of the adapter that shows it.
var anEnglishSentence = regexp.MustCompile(`[A-Za-z]{2,} [A-Za-z]{2,} [A-Za-z]{2,}`)

func TestThePlanSaysNothingInEnglish(t *testing.T) {
	// Everything the plan can have to say: every record type, and a row left out for
	// each of the reasons that have values to fill in, a rate that is missing, an
	// override that does not fit, one that is not used, and one with no row.
	conversionApart := instalmentRows("Cash", "Card")
	for i := range conversionApart {
		conversionApart[i].Date = conversionApart[i].Date.AddDate(1, 0, 0)
	}
	base := everyRecordType()
	problems := append([]model.Row{
		row(model.RecordType("新類型"), "Cash", "TWD", "5", on("2026-03-20")),
		row(model.TypeExpense, "Bank USD", "USD", "-10", main_("Food", "Lunch"), on("2026-02-01")),
		row(model.TypeExpense, "Cash", "TWD", "-20", main_("Food", "Lunch"), on("2026-03-20")),
		row(model.TypeExpense, "Cash", "TWD", "-1.234", main_("Food", "Lunch"), on("2026-03-20")),
		row(model.TypeExpense, "Cash", "TWD", "-5", main_("Food", "Lunch"), on("0001-01-01")),

		out("Bank", "TWD", "-100", at("13:01")),
		out("Bank", "TWD", "-100", at("15:00")), out("Cash", "TWD", "-100", at("15:00")),
		in("Card", "TWD", "100", at("15:00")), in("Exchange", "TWD", "100", at("15:00")),
		out("Bank", "TWD", "-100", at("16:00")), out("Cash", "TWD", "-300", at("16:00")),
		in("Card", "TWD", "100", at("16:00")), in("Exchange", "TWD", "999", at("16:00")),
		out("Exchange", "USDT", "-100", at("17:00")), in("Exchange BTC", "BTC", "0", at("17:00")),
		out("Exchange BTC", "BTC", "-0.5", at("18:00")), in("Cash", "ETH", "4", at("18:00")),
		out("Bank", "TWD", "-100", at("19:00")), in("Bank USD", "TWD", "100", at("19:00")),
		out("Bank", "TWD", "-100", at("20:00")), in("Card", "TWD", "90", at("20:00")),

		row(model.TypeInstalmentPayment, "Card", "TWD", "-200", main_("帳單分期", "帳單分期"), on("2026-03-22")),
		row(model.TypeInitial, "Elsewhere", "TWD", "50", on("0001-01-01")),
		row(model.TypeInitial, "Bank USD", "JPY", "100", on("0001-01-01")),
	}, conversionApart...)
	rows := rowsOf(append(base, problems...)...)

	m := testMapping()
	m.Rows = map[string]model.RowOverride{
		rows[len(base)+2].Key: {Treatment: model.TreatmentReward}, // a 支出 is not a reward
		"no-such-row-1":       {Treatment: model.TreatmentSkip},
	}
	for _, r := range rows {
		if r.Type == model.TypeInstalmentPayment && r.Date.Equal(day("2026-02-15")) {
			m.Rows[r.Key] = model.RowOverride{Treatment: model.TreatmentSkip} // part of a plan: not used
		}
	}
	p := serviceBuild(rows, m)

	// The plan has what the test is about.
	reasons := map[model.Reason]bool{}
	for _, s := range p.Skipped {
		reasons[s.Reason] = true
	}
	for _, want := range []model.Reason{
		model.ReasonUnknownType, model.ReasonTooManyDecimals, model.ReasonNoDate, model.ReasonUnpaired, model.ReasonAmbiguousPair,
		model.ReasonNoPairing, model.ReasonZeroQuantity, model.ReasonNotCurrencies, model.ReasonSameAccount, model.ReasonAmountMismatch,
		model.ReasonInstalmentUnmatched, model.ReasonInstalmentAccounts,
	} {
		if !reasons[want] {
			t.Errorf("no row is left out as %s: the file does not say what it was made to say", want)
		}
	}
	codes := map[model.ProblemCode]bool{}
	for _, pr := range p.Problems {
		codes[pr.Code] = true
	}
	if !codes[model.ProblemRateMissing] || !codes[model.ProblemOverrideNotFit] {
		t.Errorf("problems = %+v, want a missing rate and an override that does not fit", p.Problems)
	}
	if len(p.Warnings) != 2 || len(p.Caveats) == 0 || len(p.Adjustments) == 0 {
		t.Errorf("warnings = %+v, caveats = %d, adjustments = %d, want both warnings, caveats and adjustments", p.Warnings, len(p.Caveats), len(p.Adjustments))
	}

	say := func(where string, text ...string) {
		for _, s := range text {
			if anEnglishSentence.MatchString(s) {
				t.Errorf("%s says %q: the plan says values, and the text is for whoever shows it", where, s)
			}
		}
	}
	for _, pr := range p.Problems {
		say("problem "+string(pr.Code), pr.Args...)
	}
	for _, s := range p.Skipped {
		say("skipped as "+string(s.Reason), s.Args...)
	}
	for _, c := range p.Caveats {
		say("caveat "+string(c.Kind), c.Args...)
	}
	for _, w := range p.Warnings {
		say("warning "+string(w.Code), w.Args...)
	}
	for _, u := range p.Units {
		say("unit "+string(u.Kind), u.Description)
		if u.Reward != nil {
			say("reward of a unit", u.Reward.Description)
		}
	}
	for _, a := range p.Adjustments {
		say("adjustment", a.Description)
		if !strings.HasPrefix(a.Description, "對帳（MOZE 的餘額調整）") {
			t.Errorf("adjustment says %q, want a reconciliation that quotes MOZE", a.Description)
		}
	}
}
