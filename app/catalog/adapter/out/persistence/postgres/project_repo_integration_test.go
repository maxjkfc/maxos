package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/maxjkfc/maxos/app/catalog/adapter/out/persistence/postgres"
	"github.com/maxjkfc/maxos/app/catalog/domain/model"
	"github.com/maxjkfc/maxos/internal/testpg"
)

// Postgres SQLSTATEs of the constraint violations the tests expect.
const (
	foreignKeyViolation = "23503"
	checkViolation      = "23514"
)

// day returns midnight UTC on the given day of December 2026.
func day(d int) time.Time {
	return time.Date(2026, time.December, d, 0, 0, 0, 0, time.UTC)
}

func period(t *testing.T, from, to int) *model.Period {
	t.Helper()
	p, err := model.NewPeriod(day(from), day(to))
	if err != nil {
		t.Fatalf("NewPeriod(%d, %d): %v", from, to, err)
	}
	return &p
}

// newProject returns a project ready to be stored, with no periods or area.
func newProject(t *testing.T, name string) model.Project {
	t.Helper()
	p, err := model.NewProject(uuid.Must(uuid.NewV7()), name)
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}
	return p
}

// insertProject stores p and returns it as stored.
func insertProject(t *testing.T, r *postgres.ProjectRepo, p model.Project) model.Project {
	t.Helper()
	stored, err := r.Insert(context.Background(), p)
	if err != nil {
		t.Fatalf("Insert project %q: %v", p.Name, err)
	}
	return stored
}

// wantConstraint fails the test unless err is the database rejecting a row for
// breaking the named constraint.
func wantConstraint(t *testing.T, err error, sqlState, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != sqlState || pgErr.ConstraintName != constraint {
		t.Fatalf("err = %v, want SQLSTATE %s on constraint %s", err, sqlState, constraint)
	}
}

func TestProjectRoundTripsItsPeriodsAndDefaultArea(t *testing.T) {
	ctx := context.Background()
	pool := testpg.New(t)
	areas, projects := postgres.NewAreaRepo(pool), postgres.NewProjectRepo(pool)
	area := insertArea(t, areas, "玩樂")

	p := newProject(t, "日本旅行")
	p.Period = period(t, 1, 8)
	p.ActivePeriod = period(t, 2, 7)
	p.DefaultAreaID = &area.ID
	stored := insertProject(t, projects, p)

	for name, got := range map[string]model.Project{"Insert": stored, "Get": mustGet(t, projects, p.ID)} {
		if got.ID != p.ID || got.Name != "日本旅行" || got.ArchivedAt != nil || got.CreatedAt.IsZero() {
			t.Errorf("%s = %+v, want the project, not archived, with the time the database assigned", name, got)
		}
		if got.Period == nil || got.Period.String() != "2026-12-01 to 2026-12-08" || got.Period.From.Location() != time.UTC {
			t.Errorf("%s: Period = %v, want 2026-12-01 to 2026-12-08 at midnight UTC", name, got.Period)
		}
		if got.ActivePeriod == nil || got.ActivePeriod.String() != "2026-12-02 to 2026-12-07" {
			t.Errorf("%s: ActivePeriod = %v, want 2026-12-02 to 2026-12-07", name, got.ActivePeriod)
		}
		if got.DefaultAreaID == nil || *got.DefaultAreaID != area.ID {
			t.Errorf("%s: DefaultAreaID = %v, want %s", name, got.DefaultAreaID, area.ID)
		}
	}

	bare := insertProject(t, projects, newProject(t, "什麼都沒設"))
	if bare.Period != nil || bare.ActivePeriod != nil || bare.DefaultAreaID != nil {
		t.Fatalf("project = %+v, want no periods and no default area", bare)
	}
	if _, err := projects.Get(ctx, bare.ID); err != nil {
		t.Fatalf("Get: %v", err)
	}
}

func mustGet(t *testing.T, r *postgres.ProjectRepo, id uuid.UUID) model.Project {
	t.Helper()
	p, err := r.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get project %s: %v", id, err)
	}
	return p
}

func TestProjectNamesAreUniqueEvenWhenArchived(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewProjectRepo(testpg.New(t))
	first := insertProject(t, r, newProject(t, "日本旅行"))

	if _, err := r.Insert(ctx, newProject(t, "日本旅行")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("second insert: err = %v, want ErrAlreadyExists", err)
	}
	if _, err := r.Archive(ctx, first.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if _, err := r.Insert(ctx, newProject(t, "日本旅行")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("insert next to an archived project: err = %v, want ErrAlreadyExists", err)
	}

	other := insertProject(t, r, newProject(t, "尾牙"))
	if _, err := r.Rename(ctx, other.ID, "日本旅行"); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("rename onto a taken name: err = %v, want ErrAlreadyExists", err)
	}
}

func TestProjectTableRefusesPeriodsThatBreakItsRules(t *testing.T) {
	pool := testpg.New(t)
	projects := postgres.NewProjectRepo(pool)
	tests := []struct {
		name       string
		period     *model.Period
		active     *model.Period
		constraint string
	}{
		{name: "period reversed", period: &model.Period{From: day(8), To: day(1)}, constraint: "catalog_project_period_order_check"},
		{name: "active period reversed", active: &model.Period{From: day(7), To: day(2)}, constraint: "catalog_project_active_period_order_check"},
		{name: "active period starts before the period", period: period(t, 2, 8), active: period(t, 1, 3), constraint: "catalog_project_active_within_period_check"},
		{name: "active period ends after the period", period: period(t, 2, 8), active: period(t, 3, 9), constraint: "catalog_project_active_within_period_check"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newProject(t, tt.name)
			p.Period, p.ActivePeriod = tt.period, tt.active

			_, err := projects.Insert(context.Background(), p)
			wantConstraint(t, err, checkViolation, tt.constraint)
		})
	}

	t.Run("a period needs both ends", func(t *testing.T) {
		_, err := pool.Exec(context.Background(),
			`INSERT INTO catalog_project (id, name, starts_on) VALUES ($1, 'only a start', $2)`, uuid.Must(uuid.NewV7()), day(1))
		wantConstraint(t, err, checkViolation, "catalog_project_period_ends_check")
	})
	t.Run("an active period needs both ends", func(t *testing.T) {
		_, err := pool.Exec(context.Background(),
			`INSERT INTO catalog_project (id, name, active_to) VALUES ($1, 'only an end', $2)`, uuid.Must(uuid.NewV7()), day(1))
		wantConstraint(t, err, checkViolation, "catalog_project_active_period_ends_check")
	})
}

func TestProjectSetPeriodAndSetActivePeriodSetAndClear(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewProjectRepo(testpg.New(t))
	p := insertProject(t, r, newProject(t, "日本旅行"))

	got, err := r.SetPeriod(ctx, p.ID, period(t, 1, 8))
	if err != nil || got.Period == nil || got.Period.String() != "2026-12-01 to 2026-12-08" {
		t.Fatalf("SetPeriod = %+v, %v, want the new period", got, err)
	}
	got, err = r.SetActivePeriod(ctx, p.ID, period(t, 2, 7))
	if err != nil || got.ActivePeriod == nil || got.ActivePeriod.String() != "2026-12-02 to 2026-12-07" || got.Period == nil {
		t.Fatalf("SetActivePeriod = %+v, %v, want the active period and the period kept", got, err)
	}

	// The table is the last guard of the rules the domain checks: a period that
	// would leave the active period outside is refused.
	_, err = r.SetPeriod(ctx, p.ID, period(t, 3, 8))
	wantConstraint(t, err, checkViolation, "catalog_project_active_within_period_check")

	got, err = r.SetPeriod(ctx, p.ID, nil)
	if err != nil || got.Period != nil || got.ActivePeriod == nil {
		t.Fatalf("SetPeriod(nil) = %+v, %v, want the period cleared and the active period kept", got, err)
	}
	got, err = r.SetActivePeriod(ctx, p.ID, nil)
	if err != nil || got.ActivePeriod != nil {
		t.Fatalf("SetActivePeriod(nil) = %+v, %v, want the active period cleared", got, err)
	}
}

func TestProjectDefaultAreaMustExist(t *testing.T) {
	ctx := context.Background()
	pool := testpg.New(t)
	areas, projects := postgres.NewAreaRepo(pool), postgres.NewProjectRepo(pool)
	area := insertArea(t, areas, "玩樂")
	unknownArea := uuid.Must(uuid.NewV7())

	p := newProject(t, "日本旅行")
	p.DefaultAreaID = &unknownArea
	_, err := projects.Insert(ctx, p)
	wantConstraint(t, err, foreignKeyViolation, "catalog_project_default_area_id_fkey")

	stored := insertProject(t, projects, newProject(t, "尾牙"))
	_, err = projects.SetDefaultArea(ctx, stored.ID, unknownArea)
	wantConstraint(t, err, foreignKeyViolation, "catalog_project_default_area_id_fkey")

	got, err := projects.SetDefaultArea(ctx, stored.ID, area.ID)
	if err != nil || got.DefaultAreaID == nil || *got.DefaultAreaID != area.ID {
		t.Fatalf("SetDefaultArea = %+v, %v, want the area", got, err)
	}
}

func TestProjectMethodsReportAnUnknownID(t *testing.T) {
	ctx := context.Background()
	pool := testpg.New(t)
	area := insertArea(t, postgres.NewAreaRepo(pool), "玩樂")
	r := postgres.NewProjectRepo(pool)
	unknown := uuid.Must(uuid.NewV7())

	calls := map[string]func() error{
		"Get":             func() error { _, err := r.Get(ctx, unknown); return err },
		"Rename":          func() error { _, err := r.Rename(ctx, unknown, "x"); return err },
		"SetPeriod":       func() error { _, err := r.SetPeriod(ctx, unknown, period(t, 1, 8)); return err },
		"SetActivePeriod": func() error { _, err := r.SetActivePeriod(ctx, unknown, period(t, 1, 8)); return err },
		"SetDefaultArea":  func() error { _, err := r.SetDefaultArea(ctx, unknown, area.ID); return err },
		"Archive":         func() error { _, err := r.Archive(ctx, unknown); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, model.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
}

func TestProjectArchiveKeepsTheTimeOfTheFirstArchive(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewProjectRepo(testpg.New(t))
	p := insertProject(t, r, newProject(t, "日本旅行"))

	first, err := r.Archive(ctx, p.ID)
	if err != nil || first.ArchivedAt == nil {
		t.Fatalf("first Archive = %+v, %v, want it archived", first, err)
	}
	second, err := r.Archive(ctx, p.ID)
	if err != nil || second.ArchivedAt == nil || !second.ArchivedAt.Equal(*first.ArchivedAt) {
		t.Fatalf("second Archive = %+v, %v, want the first time %v", second, err, first.ArchivedAt)
	}
}

func TestProjectListIsByNameAndHidesArchivedUnlessAsked(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewProjectRepo(testpg.New(t))
	insertProject(t, r, newProject(t, "日本旅行"))
	insertProject(t, r, newProject(t, "尾牙"))
	old := insertProject(t, r, newProject(t, "舊的"))
	if _, err := r.Archive(ctx, old.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	inUse, err := r.List(ctx, false)
	if err != nil || len(inUse) != 2 || inUse[0].Name != "尾牙" || inUse[1].Name != "日本旅行" {
		t.Fatalf("List(false) = %+v, %v, want [尾牙 日本旅行]", inUse, err)
	}
	all, err := r.List(ctx, true)
	if err != nil || len(all) != 3 {
		t.Fatalf("List(true) = %+v, %v, want 3 projects", all, err)
	}
}

func TestProjectGetByIDsReturnsTheOnesThatExist(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewProjectRepo(testpg.New(t))
	a := insertProject(t, r, newProject(t, "日本旅行"))
	b := insertProject(t, r, newProject(t, "舊的"))
	if _, err := r.Archive(ctx, b.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	got, err := r.GetByIDs(ctx, []uuid.UUID{a.ID, b.ID, uuid.Must(uuid.NewV7())})
	if err != nil || len(got) != 2 {
		t.Fatalf("GetByIDs = %+v, %v, want the 2 projects that exist, the archived one included", got, err)
	}
}

func TestProjectListActiveOn(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewProjectRepo(testpg.New(t))
	add := func(name string, active *model.Period) model.Project {
		p := newProject(t, name)
		p.ActivePeriod = active
		return insertProject(t, r, p)
	}
	add("A", period(t, 2, 7))
	add("B", period(t, 5, 10))
	archived := add("archived", period(t, 1, 31))
	if _, err := r.Archive(ctx, archived.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	add("no active period", nil)
	// A period alone does not make a project the project of a day.
	withPeriod := newProject(t, "period only")
	withPeriod.Period = period(t, 1, 31)
	insertProject(t, r, withPeriod)

	tests := []struct {
		day  int
		want []string
	}{
		{day: 1, want: nil},
		{day: 2, want: []string{"A"}},
		{day: 4, want: []string{"A"}},
		{day: 5, want: []string{"A", "B"}},
		{day: 7, want: []string{"A", "B"}},
		{day: 8, want: []string{"B"}},
		{day: 10, want: []string{"B"}},
		{day: 11, want: nil},
	}
	for _, tt := range tests {
		got, err := r.ListActiveOn(ctx, day(tt.day))
		if err != nil {
			t.Fatalf("ListActiveOn(%d): %v", tt.day, err)
		}
		var names []string
		for _, p := range got {
			names = append(names, p.Name)
		}
		if len(names) != len(tt.want) {
			t.Errorf("ListActiveOn(%d) = %v, want %v", tt.day, names, tt.want)
			continue
		}
		for i := range names {
			if names[i] != tt.want[i] {
				t.Errorf("ListActiveOn(%d) = %v, want %v", tt.day, names, tt.want)
				break
			}
		}
	}
}
