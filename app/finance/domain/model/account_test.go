package model_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

func TestNewAccountTrimsTheNameAndOnlyTakesTheFixedKinds(t *testing.T) {
	for _, kind := range []model.AccountKind{
		model.AccountKindCash, model.AccountKindBank, model.AccountKindCard, model.AccountKindExchange,
		model.AccountKindBrokerage, model.AccountKindWallet, model.AccountKindLoan,
	} {
		got, err := model.NewAccount(model.NewAccountParams{ID: uuid.Must(uuid.NewV7()), Name: "  錢包 \n", Kind: kind})
		if err != nil {
			t.Errorf("kind %q rejected: %v", kind, err)
			continue
		}
		if got.Name != "錢包" || got.Kind != kind {
			t.Errorf("kind %q: account = %+v, want the trimmed name and the kind", kind, got)
		}
	}

	for _, kind := range []model.AccountKind{"", "savings", "Bank", "bank "} {
		_, err := model.NewAccount(model.NewAccountParams{ID: uuid.Must(uuid.NewV7()), Name: "x", Kind: kind})
		if !errors.Is(err, model.ErrInvalidAccountKind) {
			t.Errorf("kind %q: err = %v, want ErrInvalidAccountKind", kind, err)
		}
	}
}

func TestNewAccountRejectsABlankName(t *testing.T) {
	_, err := model.NewAccount(model.NewAccountParams{ID: uuid.Must(uuid.NewV7()), Name: " ", Kind: model.AccountKindCash})
	if !errors.Is(err, model.ErrNameEmpty) {
		t.Fatalf("err = %v, want ErrNameEmpty", err)
	}
}

func TestAnAccountMayHaveNoInstitutionAndDoesNotShareTheID(t *testing.T) {
	none, err := model.NewAccount(model.NewAccountParams{ID: uuid.Must(uuid.NewV7()), Name: "現金", Kind: model.AccountKindCash})
	if err != nil || none.InstitutionID != nil {
		t.Fatalf("account = %+v, %v, want no institution", none, err)
	}

	institution := uuid.Must(uuid.NewV7())
	got, err := model.NewAccount(model.NewAccountParams{ID: uuid.Must(uuid.NewV7()), Name: "薪轉戶", Kind: model.AccountKindBank, InstitutionID: &institution})
	if err != nil || got.InstitutionID == nil || *got.InstitutionID != institution {
		t.Fatalf("account = %+v, %v, want the institution", got, err)
	}
	institution = uuid.Must(uuid.NewV7())
	if *got.InstitutionID == institution {
		t.Fatal("changing the id the caller passed changed the account")
	}
}

func TestNewInstitutionTrimsTheNameAndRejectsABlankOne(t *testing.T) {
	got, err := model.NewInstitution(uuid.Must(uuid.NewV7()), "  玉山銀行 ")
	if err != nil || got.Name != "玉山銀行" {
		t.Fatalf("institution = %+v, %v, want the trimmed name", got, err)
	}

	if _, err := model.NewInstitution(uuid.Must(uuid.NewV7()), "\t "); !errors.Is(err, model.ErrNameEmpty) {
		t.Fatalf("err = %v, want ErrNameEmpty", err)
	}
}
