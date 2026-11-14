package area_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/catalog/domain/model"
	"github.com/maxjkfc/maxos/app/catalog/usecase/area"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fakeRepo is an in-memory repo.AreaRepo. The method the area use cases do not
// call, GetByIDs, is left to the embedded nil interface.
type fakeRepo struct {
	repo.AreaRepo

	areas map[uuid.UUID]model.Area
	// err, when set, is what every method returns.
	err error
	// gotIncludeArchived is the flag List was last asked with.
	gotIncludeArchived bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{areas: map[uuid.UUID]model.Area{}}
}

func (f *fakeRepo) nameTaken(name string, except uuid.UUID) bool {
	for id, a := range f.areas {
		if a.Name == name && id != except {
			return true
		}
	}
	return false
}

func (f *fakeRepo) Insert(_ context.Context, a model.Area) (model.Area, error) {
	if f.err != nil {
		return model.Area{}, f.err
	}
	if f.nameTaken(a.Name, a.ID) {
		return model.Area{}, model.ErrAlreadyExists
	}
	a.CreatedAt = epoch.Add(time.Duration(len(f.areas)) * time.Second)
	f.areas[a.ID] = a
	return a, nil
}

func (f *fakeRepo) Get(_ context.Context, id uuid.UUID) (model.Area, error) {
	if f.err != nil {
		return model.Area{}, f.err
	}
	a, ok := f.areas[id]
	if !ok {
		return model.Area{}, model.ErrNotFound
	}
	return a, nil
}

func (f *fakeRepo) List(_ context.Context, includeArchived bool) ([]model.Area, error) {
	f.gotIncludeArchived = includeArchived
	if f.err != nil {
		return nil, f.err
	}
	var out []model.Area
	for _, a := range f.areas {
		if includeArchived || !a.IsArchived() {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fakeRepo) Rename(_ context.Context, id uuid.UUID, name string) (model.Area, error) {
	if f.err != nil {
		return model.Area{}, f.err
	}
	a, ok := f.areas[id]
	if !ok {
		return model.Area{}, model.ErrNotFound
	}
	if f.nameTaken(name, id) {
		return model.Area{}, model.ErrAlreadyExists
	}
	a.Name = name
	f.areas[id] = a
	return a, nil
}

func (f *fakeRepo) Archive(_ context.Context, id uuid.UUID) (model.Area, error) {
	if f.err != nil {
		return model.Area{}, f.err
	}
	a, ok := f.areas[id]
	if !ok {
		return model.Area{}, model.ErrNotFound
	}
	if a.ArchivedAt == nil {
		at := epoch.Add(time.Hour)
		a.ArchivedAt = &at
		f.areas[id] = a
	}
	return a, nil
}

// seed stores an area directly, as an earlier call would have.
func (f *fakeRepo) seed(t *testing.T, name string) uuid.UUID {
	t.Helper()
	a, err := model.NewArea(uuid.Must(uuid.NewV7()), name)
	if err != nil {
		t.Fatalf("NewArea: %v", err)
	}
	f.areas[a.ID] = a
	return a.ID
}

func TestCreateAreaStoresTheTrimmedName(t *testing.T) {
	r := newFakeRepo()

	got, err := area.New(r).CreateArea(context.Background(), usecase.CreateAreaInput{Name: "  玩樂 "})
	if err != nil {
		t.Fatalf("CreateArea: %v", err)
	}
	if got.Name != "玩樂" || got.ID == uuid.Nil || got.CreatedAt.IsZero() || got.ArchivedAt != nil {
		t.Fatalf("area = %+v, want the trimmed name, an id, the time the store assigned, and not archived", got)
	}
	if stored := r.areas[got.ID]; stored.Name != "玩樂" {
		t.Fatalf("stored = %+v, want the trimmed name", stored)
	}
}

func TestCreateAreaRejectsABlankNameWithoutStoring(t *testing.T) {
	r := newFakeRepo()

	_, err := area.New(r).CreateArea(context.Background(), usecase.CreateAreaInput{Name: " \t "})
	if !errors.Is(err, model.ErrNameEmpty) {
		t.Fatalf("err = %v, want ErrNameEmpty", err)
	}
	if len(r.areas) != 0 {
		t.Fatalf("stored %d areas, want 0", len(r.areas))
	}
}

func TestCreateAreaReportsATakenName(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	svc := area.New(r)
	if _, err := svc.CreateArea(ctx, usecase.CreateAreaInput{Name: "玩樂"}); err != nil {
		t.Fatalf("first CreateArea: %v", err)
	}

	_, err := svc.CreateArea(ctx, usecase.CreateAreaInput{Name: " 玩樂 "})
	if !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("err = %v, want ErrAlreadyExists", err)
	}
	if !strings.Contains(err.Error(), "玩樂") {
		t.Errorf("err = %q, want it to name the area", err)
	}
	if len(r.areas) != 1 {
		t.Fatalf("stored %d areas, want 1", len(r.areas))
	}
}

func TestRenameArea(t *testing.T) {
	ctx := context.Background()

	t.Run("stores the trimmed name", func(t *testing.T) {
		r := newFakeRepo()
		id := r.seed(t, "玩樂")

		got, err := area.New(r).RenameArea(ctx, usecase.RenameAreaInput{ID: id, Name: " 娛樂 "})
		if err != nil {
			t.Fatalf("RenameArea: %v", err)
		}
		if got.ID != id || got.Name != "娛樂" || r.areas[id].Name != "娛樂" {
			t.Fatalf("area = %+v, stored = %+v, want the new trimmed name", got, r.areas[id])
		}
	})

	t.Run("rejects a blank name and keeps the old one", func(t *testing.T) {
		r := newFakeRepo()
		id := r.seed(t, "玩樂")

		_, err := area.New(r).RenameArea(ctx, usecase.RenameAreaInput{ID: id, Name: "  "})
		if !errors.Is(err, model.ErrNameEmpty) {
			t.Fatalf("err = %v, want ErrNameEmpty", err)
		}
		if r.areas[id].Name != "玩樂" {
			t.Fatalf("name = %q, want it unchanged", r.areas[id].Name)
		}
	})

	t.Run("reports an unknown area", func(t *testing.T) {
		unknown := uuid.Must(uuid.NewV7())

		_, err := area.New(newFakeRepo()).RenameArea(ctx, usecase.RenameAreaInput{ID: unknown, Name: "玩樂"})
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if !strings.Contains(err.Error(), unknown.String()) {
			t.Errorf("err = %q, want it to name the id", err)
		}
	})

	t.Run("reports a taken name and keeps the old one", func(t *testing.T) {
		r := newFakeRepo()
		r.seed(t, "工作")
		id := r.seed(t, "玩樂")

		_, err := area.New(r).RenameArea(ctx, usecase.RenameAreaInput{ID: id, Name: "工作"})
		if !errors.Is(err, model.ErrAlreadyExists) {
			t.Fatalf("err = %v, want ErrAlreadyExists", err)
		}
		if r.areas[id].Name != "玩樂" {
			t.Fatalf("name = %q, want it unchanged", r.areas[id].Name)
		}
	})
}

func TestArchiveArea(t *testing.T) {
	ctx := context.Background()

	t.Run("archives", func(t *testing.T) {
		r := newFakeRepo()
		id := r.seed(t, "玩樂")

		got, err := area.New(r).ArchiveArea(ctx, id)
		if err != nil {
			t.Fatalf("ArchiveArea: %v", err)
		}
		if got.ArchivedAt == nil || !r.areas[id].IsArchived() {
			t.Fatalf("area = %+v, stored = %+v, want it archived", got, r.areas[id])
		}
	})

	t.Run("reports an unknown area", func(t *testing.T) {
		_, err := area.New(newFakeRepo()).ArchiveArea(ctx, uuid.Must(uuid.NewV7()))
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestListAreasKeepsArchivedOnesOutUnlessAsked(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	r.seed(t, "工作")
	r.seed(t, "玩樂")
	archived := r.seed(t, "舊的")
	if _, err := r.Archive(ctx, archived); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	svc := area.New(r)

	inUse, err := svc.ListAreas(ctx, usecase.ListAreasInput{})
	if err != nil {
		t.Fatalf("ListAreas: %v", err)
	}
	if r.gotIncludeArchived || len(inUse.Areas) != 2 {
		t.Fatalf("asked for archived = %v, got %d areas, want not asked and 2", r.gotIncludeArchived, len(inUse.Areas))
	}

	all, err := svc.ListAreas(ctx, usecase.ListAreasInput{IncludeArchived: true})
	if err != nil {
		t.Fatalf("ListAreas(IncludeArchived): %v", err)
	}
	if !r.gotIncludeArchived || len(all.Areas) != 3 {
		t.Fatalf("asked for archived = %v, got %d areas, want asked and 3", r.gotIncludeArchived, len(all.Areas))
	}
	for i := 1; i < len(all.Areas); i++ {
		if all.Areas[i-1].Name > all.Areas[i].Name {
			t.Fatalf("areas = %+v, want the order the repo gave (by name)", all.Areas)
		}
	}
}

func TestGetArea(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	id := r.seed(t, "玩樂")
	svc := area.New(r)

	got, err := svc.GetArea(ctx, id)
	if err != nil || got.ID != id || got.Name != "玩樂" {
		t.Fatalf("GetArea = %+v, %v, want the area", got, err)
	}

	if _, err := svc.GetArea(ctx, uuid.Must(uuid.NewV7())); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAreaUseCasesReportStoreFailures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("connection reset")
	r := newFakeRepo()
	id := r.seed(t, "玩樂")
	r.err = boom
	svc := area.New(r)

	calls := map[string]func() error{
		"CreateArea":  func() error { _, err := svc.CreateArea(ctx, usecase.CreateAreaInput{Name: "x"}); return err },
		"RenameArea":  func() error { _, err := svc.RenameArea(ctx, usecase.RenameAreaInput{ID: id, Name: "x"}); return err },
		"ArchiveArea": func() error { _, err := svc.ArchiveArea(ctx, id); return err },
		"ListAreas":   func() error { _, err := svc.ListAreas(ctx, usecase.ListAreasInput{}); return err },
		"GetArea":     func() error { _, err := svc.GetArea(ctx, id); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, boom) {
			t.Errorf("%s: err = %v, want the store failure", name, err)
		}
	}
}
