package model_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// What a preview figure is made of decides how a page says it: a figure that no draft changes is
// what it is, one that drafts make up the whole of has nothing confirmed to say, and one that is
// both says what is confirmed of it. The total is what it will be once the drafts are confirmed.

func TestAPreviewFigureIsItsConfirmedPartAndWhatTheDraftsAddAndSaysWhichItIsMadeOf(t *testing.T) {
	tests := []struct {
		name      string
		confirmed string
		drafts    string
		want      string
		basis     model.PreviewBasis
	}{
		{"nothing at all", "0", "0", "0", model.BasisConfirmed},
		{"confirmed, and no draft changes it", "800", "0", "800", model.BasisConfirmed},
		{"owed, and no draft changes it", "-500", "0", "-500", model.BasisConfirmed},
		{"confirmed, and drafts take some away", "800", "-120", "680", model.BasisBoth},
		{"owed, and drafts add to what is owed", "-500", "-2000", "-2500", model.BasisBoth},
		{"drafts that all but cancel what is confirmed", "500", "-499.99", "0.01", model.BasisBoth},
		{"nothing confirmed, and drafts make all of it", "0", "49000", "49000", model.BasisDrafts},
		{"nothing confirmed, and drafts make it owed", "0", "-900", "-900", model.BasisDrafts},
		// What is confirmed is not nothing, and the drafts take all of it: the figure is nothing,
		// and is not of drafts alone.
		{"drafts that cancel what is confirmed", "500", "-500", "0", model.BasisBoth},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := model.PreviewFigure{Confirmed: dec(tt.confirmed), Drafts: dec(tt.drafts)}

			if got := f.Total(); !got.Equal(dec(tt.want)) {
				t.Errorf("Total = %s, want %s", got, tt.want)
			}
			if got := f.Basis(); got != tt.basis {
				t.Errorf("Basis = %q, want %q", got, tt.basis)
			}
		})
	}
}

// Something is confirmed of a balance when either of its figures has something confirmed in it, and
// of a preview when any balance has: a preview of which nothing is confirmed is a ledger of drafts
// only.
func TestAPreviewHasSomethingConfirmedWhenAnyFigureOfAnyBalanceHas(t *testing.T) {
	draftsOnly := model.PreviewBalance{
		Held:     model.PreviewFigure{Drafts: dec("-900")},
		Unbilled: model.PreviewFigure{Drafts: dec("-3000")},
	}
	confirmedHeld := model.PreviewBalance{Held: model.PreviewFigure{Confirmed: dec("100"), Drafts: dec("-100")}}
	confirmedUnbilled := model.PreviewBalance{Unbilled: model.PreviewFigure{Confirmed: dec("-3000")}}

	tests := []struct {
		name     string
		balances []model.PreviewBalance
		want     bool
	}{
		{"no balance", nil, false},
		{"drafts only", []model.PreviewBalance{draftsOnly, draftsOnly}, false},
		{"what is held is confirmed, though the drafts cancel it", []model.PreviewBalance{draftsOnly, confirmedHeld}, true},
		{"only what is unbilled is confirmed", []model.PreviewBalance{draftsOnly, confirmedUnbilled}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (model.BalancePreview{Balances: tt.balances}).HasConfirmed(); got != tt.want {
				t.Errorf("HasConfirmed = %v, want %v", got, tt.want)
			}
		})
	}
	if draftsOnly.HasConfirmed() || !confirmedHeld.HasConfirmed() || !confirmedUnbilled.HasConfirmed() {
		t.Error("HasConfirmed of a balance is not whether either of its figures is confirmed")
	}
}

// The totals are what a page writes as one figure of many accounts. They are sums, kept apart by
// instrument, of the two parts of each figure: the confirmed parts, and what the drafts add.

func TestTotalsAddUpTheFiguresOfAnInstrumentAndNeverAcrossInstruments(t *testing.T) {
	cash, bank, card := uuid.New(), uuid.New(), uuid.New()
	twd, usd, btc := uuid.New(), uuid.New(), uuid.New()
	balance := func(account, instrument uuid.UUID, held, unbilled model.PreviewFigure) model.PreviewBalance {
		return model.PreviewBalance{AccountID: account, InstrumentID: instrument, Held: held, Unbilled: unbilled}
	}
	figure := func(confirmed, drafts string) model.PreviewFigure {
		return model.PreviewFigure{Confirmed: dec(confirmed), Drafts: dec(drafts)}
	}
	nothing := figure("0", "0")

	got := model.TotalPreviewBalances([]model.PreviewBalance{
		balance(bank, twd, figure("1000", "-120"), nothing),
		balance(bank, usd, figure("25.5", "0"), nothing),
		balance(card, twd, figure("-500", "-2000"), figure("-11000", "-6000")),
		balance(cash, btc, figure("0.5", "0"), nothing),
		balance(cash, twd, figure("300", "0"), nothing),
	})

	// An instrument at a time, in the order they first appear; each part of a figure is its own sum.
	if len(got) != 3 || got[0].InstrumentID != twd || got[1].InstrumentID != usd || got[2].InstrumentID != btc {
		t.Fatalf("totals %v, want TWD, USD and BTC, in the order they first appear", got)
	}
	tests := []struct {
		name                      string
		figure                    model.PreviewFigure
		confirmed, drafts, wanted string
	}{
		{"TWD held", got[0].Held, "800", "-2120", "-1320"},
		{"TWD unbilled", got[0].Unbilled, "-11000", "-6000", "-17000"},
		{"USD held", got[1].Held, "25.5", "0", "25.5"},
		{"USD unbilled", got[1].Unbilled, "0", "0", "0"},
		{"BTC held", got[2].Held, "0.5", "0", "0.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.figure.Confirmed.Equal(dec(tt.confirmed)) || !tt.figure.Drafts.Equal(dec(tt.drafts)) || !tt.figure.Total().Equal(dec(tt.wanted)) {
				t.Errorf("confirmed %s, drafts %s, total %s, want %s, %s and %s",
					tt.figure.Confirmed, tt.figure.Drafts, tt.figure.Total(), tt.confirmed, tt.drafts, tt.wanted)
			}
		})
	}
}

// A total is said to be made of what its two sums are: the confirmed figures of accounts that cancel
// one another are nothing confirmed, and drafts that cancel one another change nothing.
func TestTheBasisOfATotalIsThatOfItsSumsAndNotOfTheFiguresItIsMadeOf(t *testing.T) {
	twd := uuid.New()
	total := func(held ...model.PreviewFigure) model.PreviewFigure {
		var balances []model.PreviewBalance
		for _, f := range held {
			balances = append(balances, model.PreviewBalance{AccountID: uuid.New(), InstrumentID: twd, Held: f})
		}
		return model.TotalPreviewBalances(balances)[0].Held
	}
	figure := func(confirmed, drafts string) model.PreviewFigure {
		return model.PreviewFigure{Confirmed: dec(confirmed), Drafts: dec(drafts)}
	}

	tests := []struct {
		name string
		got  model.PreviewFigure
		want model.PreviewBasis
	}{
		{"no draft changes any of them", total(figure("800", "0"), figure("-500", "0")), model.BasisConfirmed},
		{"drafts alone make all of them", total(figure("0", "-900"), figure("0", "49000")), model.BasisDrafts},
		{"one is confirmed, and drafts change the other", total(figure("800", "0"), figure("0", "-900")), model.BasisBoth},
		{"drafts that cancel one another change nothing", total(figure("800", "0"), figure("0", "-900"), figure("0", "900")), model.BasisConfirmed},
		// The confirmed parts of two accounts that cancel one another are nothing confirmed of the
		// total, which the drafts then make: it is said of the sums, and each account says what it is
		// made of.
		{"confirmed parts that cancel one another", total(figure("500", "-10"), figure("-500", "-20")), model.BasisDrafts},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.got.Basis(); got != tt.want {
				t.Errorf("Basis = %q, want %q (confirmed %s, drafts %s)", got, tt.want, tt.got.Confirmed, tt.got.Drafts)
			}
		})
	}
}

func TestNoBalancesAddUpToNoTotals(t *testing.T) {
	if got := model.TotalPreviewBalances(nil); len(got) != 0 {
		t.Errorf("totals = %v, want none", got)
	}
}
