package institution_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/institution"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fakeRepo is an in-memory repo.InstitutionRepo.
type fakeRepo struct {
	repo.InstitutionRepo

	institutions map[uuid.UUID]model.Institution
	// err, when set, is what every method returns.
	err error
	// gotIncludeArchived is the flag List was last asked with.
	gotIncludeArchived bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{institutions: map[uuid.UUID]model.Institution{}}
}

func (f *fakeRepo) nameTaken(name string) bool {
	for _, i := range f.institutions {
		if i.Name == name {
			return true
		}
	}
	return false
}

func (f *fakeRepo) Insert(_ context.Context, i model.Institution) (model.Institution, error) {
	if f.err != nil {
		return model.Institution{}, f.err
	}
	if f.nameTaken(i.Name) {
		return model.Institution{}, model.ErrAlreadyExists
	}
	i.CreatedAt = epoch.Add(time.Duration(len(f.institutions)) * time.Second)
	f.institutions[i.ID] = i
	return i, nil
}

func (f *fakeRepo) Get(_ context.Context, id uuid.UUID) (model.Institution, error) {
	if f.err != nil {
		return model.Institution{}, f.err
	}
	i, ok := f.institutions[id]
	if !ok {
		return model.Institution{}, model.ErrNotFound
	}
	return i, nil
}

func (f *fakeRepo) List(_ context.Context, includeArchived bool) ([]model.Institution, error) {
	f.gotIncludeArchived = includeArchived
	if f.err != nil {
		return nil, f.err
	}
	var out []model.Institution
	for _, i := range f.institutions {
		if includeArchived || !i.IsArchived() {
			out = append(out, i)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out, nil
}

func (f *fakeRepo) Archive(_ context.Context, id uuid.UUID) (model.Institution, error) {
	if f.err != nil {
		return model.Institution{}, f.err
	}
	i, ok := f.institutions[id]
	if !ok {
		return model.Institution{}, model.ErrNotFound
	}
	if i.ArchivedAt == nil {
		at := epoch.Add(time.Hour)
		i.ArchivedAt = &at
		f.institutions[id] = i
	}
	return i, nil
}

// seed stores an institution directly, as an earlier call would have.
func (f *fakeRepo) seed(t *testing.T, name string) uuid.UUID {
	t.Helper()
	i, err := model.NewInstitution(uuid.Must(uuid.NewV7()), name)
	if err != nil {
		t.Fatalf("NewInstitution: %v", err)
	}
	f.institutions[i.ID] = i
	return i.ID
}

func TestCreateInstitutionStoresTheTrimmedName(t *testing.T) {
	r := newFakeRepo()

	got, err := institution.New(r).CreateInstitution(context.Background(), usecase.CreateInstitutionInput{Name: "  國泰世華 "})
	if err != nil {
		t.Fatalf("CreateInstitution: %v", err)
	}
	if got.Name != "國泰世華" || got.ID == uuid.Nil || got.CreatedAt.IsZero() || got.ArchivedAt != nil {
		t.Fatalf("institution = %+v, want the trimmed name, an id, the time the store assigned, and not archived", got)
	}
	if stored := r.institutions[got.ID]; stored.Name != "國泰世華" {
		t.Fatalf("stored = %+v, want the trimmed name", stored)
	}
}

func TestCreateInstitutionRejectsABlankNameWithoutStoring(t *testing.T) {
	r := newFakeRepo()

	_, err := institution.New(r).CreateInstitution(context.Background(), usecase.CreateInstitutionInput{Name: " \t "})
	if !errors.Is(err, model.ErrNameEmpty) {
		t.Fatalf("err = %v, want ErrNameEmpty", err)
	}
	if len(r.institutions) != 0 {
		t.Fatalf("stored %d institutions, want 0", len(r.institutions))
	}
}

func TestCreateInstitutionReportsATakenName(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	svc := institution.New(r)
	if _, err := svc.CreateInstitution(ctx, usecase.CreateInstitutionInput{Name: "國泰世華"}); err != nil {
		t.Fatalf("first CreateInstitution: %v", err)
	}

	_, err := svc.CreateInstitution(ctx, usecase.CreateInstitutionInput{Name: " 國泰世華 "})
	if !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("err = %v, want ErrAlreadyExists", err)
	}
	if !strings.Contains(err.Error(), "國泰世華") {
		t.Errorf("err = %q, want it to name the institution", err)
	}
	if len(r.institutions) != 1 {
		t.Fatalf("stored %d institutions, want 1", len(r.institutions))
	}
}

func TestArchiveInstitution(t *testing.T) {
	ctx := context.Background()

	t.Run("archives", func(t *testing.T) {
		r := newFakeRepo()
		id := r.seed(t, "國泰世華")

		got, err := institution.New(r).ArchiveInstitution(ctx, id)
		if err != nil {
			t.Fatalf("ArchiveInstitution: %v", err)
		}
		if got.ArchivedAt == nil || !r.institutions[id].IsArchived() {
			t.Fatalf("institution = %+v, stored = %+v, want it archived", got, r.institutions[id])
		}
	})

	t.Run("reports an unknown institution", func(t *testing.T) {
		unknown := uuid.Must(uuid.NewV7())

		_, err := institution.New(newFakeRepo()).ArchiveInstitution(ctx, unknown)
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if !strings.Contains(err.Error(), unknown.String()) {
			t.Errorf("err = %q, want it to name the id", err)
		}
	})
}

func TestListInstitutionsKeepsArchivedOnesOutUnlessAsked(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	r.seed(t, "幣安")
	r.seed(t, "國泰世華")
	archived := r.seed(t, "舊的")
	if _, err := r.Archive(ctx, archived); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	svc := institution.New(r)

	inUse, err := svc.ListInstitutions(ctx, usecase.ListInstitutionsInput{})
	if err != nil {
		t.Fatalf("ListInstitutions: %v", err)
	}
	if r.gotIncludeArchived || len(inUse.Institutions) != 2 {
		t.Fatalf("asked for archived = %v, got %d institutions, want not asked and 2", r.gotIncludeArchived, len(inUse.Institutions))
	}

	all, err := svc.ListInstitutions(ctx, usecase.ListInstitutionsInput{IncludeArchived: true})
	if err != nil {
		t.Fatalf("ListInstitutions(IncludeArchived): %v", err)
	}
	if !r.gotIncludeArchived || len(all.Institutions) != 3 {
		t.Fatalf("asked for archived = %v, got %d institutions, want asked and 3", r.gotIncludeArchived, len(all.Institutions))
	}
	for i := 1; i < len(all.Institutions); i++ {
		if all.Institutions[i-1].Name > all.Institutions[i].Name {
			t.Fatalf("institutions = %+v, want the order the repo gave (by name)", all.Institutions)
		}
	}
}

func TestGetInstitution(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	id := r.seed(t, "國泰世華")
	svc := institution.New(r)

	got, err := svc.GetInstitution(ctx, id)
	if err != nil || got.ID != id || got.Name != "國泰世華" {
		t.Fatalf("GetInstitution = %+v, %v, want the institution", got, err)
	}

	unknown := uuid.Must(uuid.NewV7())
	_, err = svc.GetInstitution(ctx, unknown)
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), unknown.String()) {
		t.Errorf("err = %q, want it to name the id", err)
	}
}

func TestInstitutionUseCasesReportStoreFailures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("connection reset")
	r := newFakeRepo()
	id := r.seed(t, "國泰世華")
	r.err = boom
	svc := institution.New(r)

	calls := map[string]func() error{
		"CreateInstitution": func() error {
			_, err := svc.CreateInstitution(ctx, usecase.CreateInstitutionInput{Name: "x"})
			return err
		},
		"ArchiveInstitution": func() error { _, err := svc.ArchiveInstitution(ctx, id); return err },
		"ListInstitutions":   func() error { _, err := svc.ListInstitutions(ctx, usecase.ListInstitutionsInput{}); return err },
		"GetInstitution":     func() error { _, err := svc.GetInstitution(ctx, id); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, boom) {
			t.Errorf("%s: err = %v, want the store failure", name, err)
		}
	}
}
