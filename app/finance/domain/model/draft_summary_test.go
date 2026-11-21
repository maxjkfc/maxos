package model_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// draftAmount is what drafts move of an instrument.
func draftAmount(instrumentID uuid.UUID, code, outflow, inflow string) model.DraftAmount {
	return model.DraftAmount{InstrumentID: instrumentID, InstrumentCode: code, Outflow: dec(outflow), Inflow: dec(inflow)}
}

// writeAmounts writes the amounts as "BTC out 1 in 0", one each, in the order they are.
func writeAmounts(amounts []model.DraftAmount) []string {
	out := make([]string, len(amounts))
	for i, a := range amounts {
		out[i] = fmt.Sprintf("%s out %s in %s", a.InstrumentCode, a.Outflow, a.Inflow)
	}
	return out
}

func TestADraftAmountNetIsWhatComesInLessWhatGoesOut(t *testing.T) {
	usd := uuid.Must(uuid.NewV7())
	tests := []struct {
		name            string
		outflow, inflow string
		want            string
	}{
		{"only an outflow, which is a decrease", "120", "0", "-120"},
		{"only an inflow", "0", "50000", "50000"},
		{"both, and more out", "1000.5", "250.25", "-750.25"},
		{"both, and more in", "10", "30.5", "20.5"},
		{"both the same, which moves nothing in all", "1000", "1000", "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := draftAmount(usd, "USD", tt.outflow, tt.inflow).Net(); !got.Equal(dec(tt.want)) {
				t.Errorf("Net = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestAddDraftAmountsAddsUpEachInstrumentOfAllTheGroupsByTheCodeOfTheInstrument(t *testing.T) {
	// The ids go in the opposite order of the codes, so that only the codes say the order.
	usd, twd, btc := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	cash := []model.DraftAmount{draftAmount(btc, "BTC", "1", "0"), draftAmount(usd, "USD", "100", "0")}
	bank := []model.DraftAmount{draftAmount(twd, "TWD", "5000", "300"), draftAmount(usd, "USD", "0", "40.5")}
	card := []model.DraftAmount{draftAmount(twd, "TWD", "120", "0")}

	got := model.AddDraftAmounts(cash, bank, card)

	want := []string{"BTC out 1 in 0", "TWD out 5120 in 300", "USD out 100 in 40.5"}
	if !slices.Equal(writeAmounts(got), want) {
		t.Errorf("amounts = %q, want %q: an amount for each instrument, added up, by code", writeAmounts(got), want)
	}
	if got[0].InstrumentID != btc || got[1].InstrumentID != twd || got[2].InstrumentID != usd {
		t.Errorf("instruments = %v %v %v, want those the codes are of", got[0].InstrumentID, got[1].InstrumentID, got[2].InstrumentID)
	}
	// What was added up is not changed: the groups are what the repo read.
	if !cash[1].Outflow.Equal(dec("100")) || !bank[0].Outflow.Equal(dec("5000")) {
		t.Errorf("the groups added up are %q and %q, want them as they were", writeAmounts(cash), writeAmounts(bank))
	}
	// The net of a sum is the sum of the nets.
	if net := got[2].Net(); !net.Equal(dec("-59.5")) {
		t.Errorf("net of the dollars = %s, want -59.5", net)
	}
}

func TestAddDraftAmountsOfNothingIsNothing(t *testing.T) {
	if got := model.AddDraftAmounts(); len(got) != 0 {
		t.Errorf("AddDraftAmounts() = %q, want none", writeAmounts(got))
	}
	if got := model.AddDraftAmounts(nil, []model.DraftAmount{}, nil); len(got) != 0 {
		t.Errorf("AddDraftAmounts of groups with nothing in them = %q, want none", writeAmounts(got))
	}
}

func TestDraftSummaryCountIsTheDraftsOfTheMonthsAddedUp(t *testing.T) {
	if got := (model.DraftSummary{}).Count(); got != 0 {
		t.Errorf("Count of no month = %d, want 0", got)
	}
	summary := model.DraftSummary{Months: []model.DraftMonth{{Count: 3}, {Count: 1}, {Count: 12}}}
	if got := summary.Count(); got != 16 {
		t.Errorf("Count = %d, want 16", got)
	}
}
