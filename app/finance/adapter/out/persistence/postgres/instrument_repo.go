package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maxjkfc/maxos/app/finance/adapter/out/persistence/postgres/dbgen"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
	pkgpostgres "github.com/maxjkfc/maxos/pkg/postgres"
)

// InstrumentRepo implements repo.InstrumentRepo on the fin_instrument table.
type InstrumentRepo struct {
	q *dbgen.Queries
}

var _ repo.InstrumentRepo = (*InstrumentRepo)(nil)

// NewInstrumentRepo returns a repo that reads and writes through pool. Its
// statements join the transaction a context carries (see pkg/postgres).
func NewInstrumentRepo(pool *pgxpool.Pool) *InstrumentRepo {
	return &InstrumentRepo{q: dbgen.New(pkgpostgres.NewDB(pool))}
}

// Insert stores instrument. The table's CHECK constraints are the last guard of
// the kind and the decimals: a value that breaks one comes back as an error from
// the database.
func (r *InstrumentRepo) Insert(ctx context.Context, instrument model.Instrument) (model.Instrument, error) {
	row, err := r.q.InsertInstrument(ctx, dbgen.InsertInstrumentParams{
		ID:         instrument.ID,
		Code:       instrument.Code,
		Name:       instrument.Name,
		Kind:       string(instrument.Kind),
		IsCurrency: instrument.IsCurrency,
		Decimals:   int16(instrument.Decimals),
	})
	if err != nil {
		return model.Instrument{}, mapError(err)
	}
	return toInstrument(row), nil
}

// Get returns the instrument with the given id.
func (r *InstrumentRepo) Get(ctx context.Context, id uuid.UUID) (model.Instrument, error) {
	row, err := r.q.GetInstrument(ctx, id)
	if err != nil {
		return model.Instrument{}, mapError(err)
	}
	return toInstrument(row), nil
}

// List returns the instruments by code.
func (r *InstrumentRepo) List(ctx context.Context, includeArchived bool) ([]model.Instrument, error) {
	rows, err := r.q.ListInstruments(ctx, includeArchived)
	if err != nil {
		return nil, err
	}
	instruments := make([]model.Instrument, len(rows))
	for i, row := range rows {
		instruments[i] = toInstrument(row)
	}
	return instruments, nil
}

// Archive archives the instrument; the first time it was archived is kept.
func (r *InstrumentRepo) Archive(ctx context.Context, id uuid.UUID) (model.Instrument, error) {
	row, err := r.q.ArchiveInstrument(ctx, id)
	if err != nil {
		return model.Instrument{}, mapError(err)
	}
	return toInstrument(row), nil
}

func toInstrument(row dbgen.FinInstrument) model.Instrument {
	return model.Instrument{
		ID:         row.ID,
		Code:       row.Code,
		Name:       row.Name,
		Kind:       model.InstrumentKind(row.Kind),
		IsCurrency: row.IsCurrency,
		Decimals:   int(row.Decimals),
		CreatedAt:  row.CreatedAt,
		ArchivedAt: row.ArchivedAt,
	}
}
