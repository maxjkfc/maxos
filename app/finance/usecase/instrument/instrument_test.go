package instrument_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/instrument"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fakeRepo is an in-memory repo.InstrumentRepo.
type fakeRepo struct {
	repo.InstrumentRepo

	instruments map[uuid.UUID]model.Instrument
	// err, when set, is what every method returns.
	err error
	// gotIncludeArchived is the flag List was last asked with.
	gotIncludeArchived bool
	// archiveCalls counts the calls of Archive.
	archiveCalls int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{instruments: map[uuid.UUID]model.Instrument{}}
}

func (f *fakeRepo) codeTaken(code string) bool {
	for _, i := range f.instruments {
		if i.Code == code {
			return true
		}
	}
	return false
}

func (f *fakeRepo) Insert(_ context.Context, i model.Instrument) (model.Instrument, error) {
	if f.err != nil {
		return model.Instrument{}, f.err
	}
	if f.codeTaken(i.Code) {
		return model.Instrument{}, model.ErrAlreadyExists
	}
	i.CreatedAt = epoch.Add(time.Duration(len(f.instruments)) * time.Second)
	f.instruments[i.ID] = i
	return i, nil
}

func (f *fakeRepo) Get(_ context.Context, id uuid.UUID) (model.Instrument, error) {
	if f.err != nil {
		return model.Instrument{}, f.err
	}
	i, ok := f.instruments[id]
	if !ok {
		return model.Instrument{}, model.ErrNotFound
	}
	return i, nil
}

func (f *fakeRepo) List(_ context.Context, includeArchived bool) ([]model.Instrument, error) {
	f.gotIncludeArchived = includeArchived
	if f.err != nil {
		return nil, f.err
	}
	var out []model.Instrument
	for _, i := range f.instruments {
		if includeArchived || !i.IsArchived() {
			out = append(out, i)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Code < out[b].Code })
	return out, nil
}

func (f *fakeRepo) Archive(_ context.Context, id uuid.UUID) (model.Instrument, error) {
	f.archiveCalls++
	if f.err != nil {
		return model.Instrument{}, f.err
	}
	i, ok := f.instruments[id]
	if !ok {
		return model.Instrument{}, model.ErrNotFound
	}
	if i.ArchivedAt == nil {
		at := epoch.Add(time.Hour)
		i.ArchivedAt = &at
		f.instruments[id] = i
	}
	return i, nil
}

// seed stores an instrument directly, as an earlier call would have.
func (f *fakeRepo) seed(t *testing.T, code string) uuid.UUID {
	t.Helper()
	i, err := model.NewInstrument(model.NewInstrumentParams{
		ID:       uuid.Must(uuid.NewV7()),
		Code:     code,
		Name:     code,
		Kind:     model.InstrumentKindStock,
		Decimals: 0,
	})
	if err != nil {
		t.Fatalf("NewInstrument: %v", err)
	}
	f.instruments[i.ID] = i
	return i.ID
}

// seedBaseCurrency stores the base currency directly, as the migration does.
func (f *fakeRepo) seedBaseCurrency() {
	f.instruments[model.BaseCurrencyID] = model.Instrument{
		ID:         model.BaseCurrencyID,
		Code:       model.BaseCurrencyCode,
		Name:       "新台幣",
		Kind:       model.InstrumentKindFiat,
		IsCurrency: true,
		Decimals:   2,
	}
}

func TestCreateInstrumentStoresTheNormalizedInstrument(t *testing.T) {
	r := newFakeRepo()

	got, err := instrument.New(r).CreateInstrument(context.Background(), usecase.CreateInstrumentInput{
		Code:       " usdt ",
		Name:       " 泰達幣 ",
		Kind:       "crypto",
		IsCurrency: true,
		Decimals:   6,
	})
	if err != nil {
		t.Fatalf("CreateInstrument: %v", err)
	}
	if got.ID == uuid.Nil || got.Code != "USDT" || got.Name != "泰達幣" || got.Kind != "crypto" ||
		!got.IsCurrency || got.Decimals != 6 || got.CreatedAt.IsZero() || got.ArchivedAt != nil {
		t.Fatalf("instrument = %+v, want USDT, crypto, a currency, 6 decimals, an id, the time the store assigned, and not archived", got)
	}
	if stored := r.instruments[got.ID]; stored.Code != "USDT" || stored.Kind != model.InstrumentKindCrypto {
		t.Fatalf("stored = %+v, want the normalized instrument", stored)
	}
}

func TestCreateInstrumentRejectsInvalidInputWithoutStoring(t *testing.T) {
	tests := []struct {
		name string
		in   usecase.CreateInstrumentInput
		want error
	}{
		{"empty code", usecase.CreateInstrumentInput{Code: " ", Name: "x", Kind: "stock"}, model.ErrCodeEmpty},
		{"code with a space", usecase.CreateInstrumentInput{Code: "A B", Name: "x", Kind: "stock"}, model.ErrCodeHasSpace},
		{"empty name", usecase.CreateInstrumentInput{Code: "A", Name: " \t ", Kind: "stock"}, model.ErrNameEmpty},
		{"unknown kind", usecase.CreateInstrumentInput{Code: "A", Name: "x", Kind: "bond"}, model.ErrInvalidInstrumentKind},
		{"too many decimals", usecase.CreateInstrumentInput{Code: "A", Name: "x", Kind: "stock", Decimals: 19}, model.ErrDecimalsOutOfRange},
		{"negative decimals", usecase.CreateInstrumentInput{Code: "A", Name: "x", Kind: "stock", Decimals: -1}, model.ErrDecimalsOutOfRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newFakeRepo()

			_, err := instrument.New(r).CreateInstrument(context.Background(), tt.in)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if len(r.instruments) != 0 {
				t.Fatalf("stored %d instruments, want 0", len(r.instruments))
			}
		})
	}
}

func TestCreateInstrumentReportsATakenCode(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	svc := instrument.New(r)
	in := usecase.CreateInstrumentInput{Code: "BTC", Name: "比特幣", Kind: "crypto", Decimals: 8}
	if _, err := svc.CreateInstrument(ctx, in); err != nil {
		t.Fatalf("first CreateInstrument: %v", err)
	}

	in.Code = " btc "
	_, err := svc.CreateInstrument(ctx, in)
	if !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("err = %v, want ErrAlreadyExists", err)
	}
	if !strings.Contains(err.Error(), "BTC") {
		t.Errorf("err = %q, want it to name the code", err)
	}
	if len(r.instruments) != 1 {
		t.Fatalf("stored %d instruments, want 1", len(r.instruments))
	}
}

func TestArchiveInstrument(t *testing.T) {
	ctx := context.Background()

	t.Run("archives", func(t *testing.T) {
		r := newFakeRepo()
		id := r.seed(t, "2330.TW")

		got, err := instrument.New(r).ArchiveInstrument(ctx, id)
		if err != nil {
			t.Fatalf("ArchiveInstrument: %v", err)
		}
		if got.ArchivedAt == nil || !r.instruments[id].IsArchived() {
			t.Fatalf("instrument = %+v, stored = %+v, want it archived", got, r.instruments[id])
		}
	})

	t.Run("refuses the base currency without calling the repo to archive it", func(t *testing.T) {
		r := newFakeRepo()
		r.seedBaseCurrency()

		_, err := instrument.New(r).ArchiveInstrument(ctx, model.BaseCurrencyID)
		if !errors.Is(err, model.ErrBaseCurrency) {
			t.Fatalf("err = %v, want ErrBaseCurrency", err)
		}
		if r.archiveCalls != 0 || r.instruments[model.BaseCurrencyID].IsArchived() {
			t.Fatalf("Archive was called %d times and the base currency archived = %v, want neither", r.archiveCalls, r.instruments[model.BaseCurrencyID].IsArchived())
		}
	})

	t.Run("reports an unknown instrument", func(t *testing.T) {
		unknown := uuid.Must(uuid.NewV7())

		_, err := instrument.New(newFakeRepo()).ArchiveInstrument(ctx, unknown)
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if !strings.Contains(err.Error(), unknown.String()) {
			t.Errorf("err = %q, want it to name the id", err)
		}
	})
}

func TestListInstrumentsKeepsArchivedOnesOutUnlessAsked(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	r.seed(t, "BTC")
	r.seed(t, "2330.TW")
	archived := r.seed(t, "OLD")
	if _, err := r.Archive(ctx, archived); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	svc := instrument.New(r)

	inUse, err := svc.ListInstruments(ctx, usecase.ListInstrumentsInput{})
	if err != nil {
		t.Fatalf("ListInstruments: %v", err)
	}
	if r.gotIncludeArchived || len(inUse.Instruments) != 2 {
		t.Fatalf("asked for archived = %v, got %d instruments, want not asked and 2", r.gotIncludeArchived, len(inUse.Instruments))
	}

	all, err := svc.ListInstruments(ctx, usecase.ListInstrumentsInput{IncludeArchived: true})
	if err != nil {
		t.Fatalf("ListInstruments(IncludeArchived): %v", err)
	}
	if !r.gotIncludeArchived || len(all.Instruments) != 3 {
		t.Fatalf("asked for archived = %v, got %d instruments, want asked and 3", r.gotIncludeArchived, len(all.Instruments))
	}
	for i := 1; i < len(all.Instruments); i++ {
		if all.Instruments[i-1].Code > all.Instruments[i].Code {
			t.Fatalf("instruments = %+v, want the order the repo gave (by code)", all.Instruments)
		}
	}
}

func TestGetInstrument(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	id := r.seed(t, "BTC")
	svc := instrument.New(r)

	got, err := svc.GetInstrument(ctx, id)
	if err != nil || got.ID != id || got.Code != "BTC" {
		t.Fatalf("GetInstrument = %+v, %v, want the instrument", got, err)
	}

	unknown := uuid.Must(uuid.NewV7())
	_, err = svc.GetInstrument(ctx, unknown)
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), unknown.String()) {
		t.Errorf("err = %q, want it to name the id", err)
	}
}

func TestInstrumentUseCasesReportStoreFailures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("connection reset")
	r := newFakeRepo()
	id := r.seed(t, "BTC")
	r.err = boom
	svc := instrument.New(r)

	calls := map[string]func() error{
		"CreateInstrument": func() error {
			_, err := svc.CreateInstrument(ctx, usecase.CreateInstrumentInput{Code: "X", Name: "x", Kind: "stock"})
			return err
		},
		"ArchiveInstrument": func() error { _, err := svc.ArchiveInstrument(ctx, id); return err },
		"ListInstruments":   func() error { _, err := svc.ListInstruments(ctx, usecase.ListInstrumentsInput{}); return err },
		"GetInstrument":     func() error { _, err := svc.GetInstrument(ctx, id); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, boom) {
			t.Errorf("%s: err = %v, want the store failure", name, err)
		}
	}
}
