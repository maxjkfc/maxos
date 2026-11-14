package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/maxjkfc/maxos/app/finance/adapter/out/persistence/postgres"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/internal/testpg"
)

// Postgres SQLSTATEs of the constraint violations the tests expect.
const (
	foreignKeyViolation = "23503"
	checkViolation      = "23514"
)

// wantConstraint fails the test unless err is the database rejecting a row for
// breaking the named constraint.
func wantConstraint(t *testing.T, err error, sqlState, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != sqlState || pgErr.ConstraintName != constraint {
		t.Fatalf("err = %v, want SQLSTATE %s on constraint %s", err, sqlState, constraint)
	}
}

// newInstrument returns an instrument ready to be stored: a stock with no
// decimals, named after its code.
func newInstrument(t *testing.T, code string) model.Instrument {
	t.Helper()
	i, err := model.NewInstrument(model.NewInstrumentParams{
		ID:   uuid.Must(uuid.NewV7()),
		Code: code,
		Name: code,
		Kind: model.InstrumentKindStock,
	})
	if err != nil {
		t.Fatalf("NewInstrument: %v", err)
	}
	return i
}

// insertInstrument stores a new instrument and returns it as stored.
func insertInstrument(t *testing.T, r *postgres.InstrumentRepo, code string) model.Instrument {
	t.Helper()
	stored, err := r.Insert(context.Background(), newInstrument(t, code))
	if err != nil {
		t.Fatalf("Insert instrument %q: %v", code, err)
	}
	return stored
}

func instrumentCodes(instruments []model.Instrument) []string {
	codes := make([]string, len(instruments))
	for i, instrument := range instruments {
		codes[i] = instrument.Code
	}
	return codes
}

func TestSeededBaseCurrencyMatchesTheModelConstant(t *testing.T) {
	r := postgres.NewInstrumentRepo(testpg.New(t))

	got, err := r.Get(context.Background(), model.BaseCurrencyID)
	if err != nil {
		t.Fatalf("Get(BaseCurrencyID): %v", err)
	}
	if got.Code != model.BaseCurrencyCode || got.Kind != model.InstrumentKindFiat || !got.IsCurrency || got.ArchivedAt != nil {
		t.Fatalf("base currency = %+v, want the TWD fiat currency in use", got)
	}
}

func TestInstrumentRoundTripsEveryField(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewInstrumentRepo(testpg.New(t))
	usdt, err := model.NewInstrument(model.NewInstrumentParams{
		ID:         uuid.Must(uuid.NewV7()),
		Code:       "USDT",
		Name:       "泰達幣",
		Kind:       model.InstrumentKindCrypto,
		IsCurrency: true,
		Decimals:   6,
	})
	if err != nil {
		t.Fatalf("NewInstrument: %v", err)
	}

	stored, err := r.Insert(ctx, usdt)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if stored.CreatedAt.IsZero() {
		t.Fatal("created_at was not assigned by the database")
	}
	got, err := r.Get(ctx, usdt.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	listed, err := r.List(ctx, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var fromList model.Instrument
	for _, i := range listed {
		if i.ID == usdt.ID {
			fromList = i
		}
	}

	for name, in := range map[string]model.Instrument{"Insert": stored, "Get": got, "List": fromList} {
		if in.ID != usdt.ID || in.Code != "USDT" || in.Name != "泰達幣" || in.Kind != model.InstrumentKindCrypto ||
			!in.IsCurrency || in.Decimals != 6 || in.ArchivedAt != nil || in.CreatedAt.IsZero() {
			t.Errorf("%s = %+v, want %+v as stored", name, in, usdt)
		}
	}
}

func TestInstrumentCodesAreUniqueEvenWhenArchived(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewInstrumentRepo(testpg.New(t))
	first := insertInstrument(t, r, "BTC")

	if _, err := r.Insert(ctx, newInstrument(t, "BTC")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("second insert: err = %v, want ErrAlreadyExists", err)
	}

	if _, err := r.Archive(ctx, first.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if _, err := r.Insert(ctx, newInstrument(t, "BTC")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("insert next to an archived instrument: err = %v, want ErrAlreadyExists", err)
	}
}

func TestInstrumentKindAndDecimalsAreCheckedByTheDatabase(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewInstrumentRepo(testpg.New(t))

	// The literals skip model.NewInstrument on purpose: the table is the last guard.
	literal := func(code string, kind model.InstrumentKind, decimals int) model.Instrument {
		return model.Instrument{ID: uuid.Must(uuid.NewV7()), Code: code, Name: code, Kind: kind, Decimals: decimals}
	}

	for _, ok := range []model.Instrument{
		literal("MIN", model.InstrumentKindFund, 0),
		literal("MAX", model.InstrumentKindFund, 18),
		literal("YEN", model.InstrumentKindFiat, 0),
	} {
		if _, err := r.Insert(ctx, ok); err != nil {
			t.Errorf("Insert %s with %d decimals: %v, want it stored", ok.Code, ok.Decimals, err)
		}
	}

	tests := []struct {
		name       string
		instrument model.Instrument
		constraint string
	}{
		{"unknown kind", literal("BOND", "bond", 2), "fin_instrument_kind_check"},
		{"empty kind", literal("NOKIND", "", 2), "fin_instrument_kind_check"},
		// What fiat was called: a currency is what an instrument is used for, not a kind.
		{"the old name of fiat", literal("OLDKIND", "currency", 2), "fin_instrument_kind_check"},
		{"19 decimals", literal("OVER", model.InstrumentKindCrypto, 19), "fin_instrument_decimals_check"},
		{"-1 decimals", literal("UNDER", model.InstrumentKindCrypto, -1), "fin_instrument_decimals_check"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Insert(ctx, tt.instrument)
			wantConstraint(t, err, checkViolation, tt.constraint)
		})
	}
}

func TestInstrumentMethodsReportAnUnknownID(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewInstrumentRepo(testpg.New(t))
	unknown := uuid.Must(uuid.NewV7())

	if _, err := r.Get(ctx, unknown); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("Get: err = %v, want ErrNotFound", err)
	}
	if _, err := r.Archive(ctx, unknown); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("Archive: err = %v, want ErrNotFound", err)
	}
}

func TestInstrumentArchiveKeepsTheTimeOfTheFirstArchive(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewInstrumentRepo(testpg.New(t))
	i := insertInstrument(t, r, "BTC")

	first, err := r.Archive(ctx, i.ID)
	if err != nil {
		t.Fatalf("first Archive: %v", err)
	}
	if first.ArchivedAt == nil {
		t.Fatal("ArchivedAt is nil after Archive")
	}

	second, err := r.Archive(ctx, i.ID)
	if err != nil {
		t.Fatalf("second Archive: %v", err)
	}
	if second.ArchivedAt == nil || !second.ArchivedAt.Equal(*first.ArchivedAt) {
		t.Fatalf("ArchivedAt = %v after archiving again, want the first time %v", second.ArchivedAt, first.ArchivedAt)
	}
}

func TestInstrumentListIsByCodeAndHidesArchivedUnlessAsked(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewInstrumentRepo(testpg.New(t))
	insertInstrument(t, r, "ETH")
	insertInstrument(t, r, "BTC")
	old := insertInstrument(t, r, "OLD")
	if _, err := r.Archive(ctx, old.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	// The seeded base currency, TWD, is in every database.
	inUse, err := r.List(ctx, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if codes := instrumentCodes(inUse); len(codes) != 3 || codes[0] != "BTC" || codes[1] != "ETH" || codes[2] != "TWD" {
		t.Fatalf("List(false) = %v, want [BTC ETH TWD]", codes)
	}

	all, err := r.List(ctx, true)
	if err != nil {
		t.Fatalf("List(true): %v", err)
	}
	if codes := instrumentCodes(all); len(codes) != 4 || codes[0] != "BTC" || codes[1] != "ETH" || codes[2] != "OLD" || codes[3] != "TWD" {
		t.Fatalf("List(true) = %v, want [BTC ETH OLD TWD]", codes)
	}
}
