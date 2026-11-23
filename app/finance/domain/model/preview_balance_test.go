package model_test

import (
	"testing"

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
