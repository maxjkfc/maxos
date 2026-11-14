package party_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/catalog/domain/model"
	"github.com/maxjkfc/maxos/app/catalog/usecase/party"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fakeRepo is an in-memory repo.PartyRepo. The method the party use cases do not
// call, GetByIDs, is left to the embedded nil interface.
type fakeRepo struct {
	repo.PartyRepo

	parties map[uuid.UUID]model.Party
	// err, when set, is what every method returns.
	err error
	// gotIncludeArchived is the flag List was last asked with.
	gotIncludeArchived bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{parties: map[uuid.UUID]model.Party{}}
}

func (f *fakeRepo) nameTaken(name string, except uuid.UUID) bool {
	for id, p := range f.parties {
		if p.Name == name && id != except {
			return true
		}
	}
	return false
}

func (f *fakeRepo) Insert(_ context.Context, p model.Party) (model.Party, error) {
	if f.err != nil {
		return model.Party{}, f.err
	}
	if f.nameTaken(p.Name, p.ID) {
		return model.Party{}, model.ErrAlreadyExists
	}
	p.CreatedAt = epoch.Add(time.Duration(len(f.parties)) * time.Second)
	f.parties[p.ID] = p
	return p, nil
}

func (f *fakeRepo) Get(_ context.Context, id uuid.UUID) (model.Party, error) {
	if f.err != nil {
		return model.Party{}, f.err
	}
	p, ok := f.parties[id]
	if !ok {
		return model.Party{}, model.ErrNotFound
	}
	return p, nil
}

func (f *fakeRepo) List(_ context.Context, includeArchived bool) ([]model.Party, error) {
	f.gotIncludeArchived = includeArchived
	if f.err != nil {
		return nil, f.err
	}
	var out []model.Party
	for _, p := range f.parties {
		if includeArchived || !p.IsArchived() {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fakeRepo) Rename(_ context.Context, id uuid.UUID, name string) (model.Party, error) {
	if f.err != nil {
		return model.Party{}, f.err
	}
	p, ok := f.parties[id]
	if !ok {
		return model.Party{}, model.ErrNotFound
	}
	if f.nameTaken(name, id) {
		return model.Party{}, model.ErrAlreadyExists
	}
	p.Name = name
	f.parties[id] = p
	return p, nil
}

func (f *fakeRepo) Archive(_ context.Context, id uuid.UUID) (model.Party, error) {
	if f.err != nil {
		return model.Party{}, f.err
	}
	p, ok := f.parties[id]
	if !ok {
		return model.Party{}, model.ErrNotFound
	}
	if p.ArchivedAt == nil {
		at := epoch.Add(time.Hour)
		p.ArchivedAt = &at
		f.parties[id] = p
	}
	return p, nil
}

// seed stores a party directly, as an earlier call would have.
func (f *fakeRepo) seed(t *testing.T, name string) uuid.UUID {
	t.Helper()
	p, err := model.NewParty(uuid.Must(uuid.NewV7()), name)
	if err != nil {
		t.Fatalf("NewParty: %v", err)
	}
	f.parties[p.ID] = p
	return p.ID
}

func TestCreatePartyStoresTheTrimmedName(t *testing.T) {
	r := newFakeRepo()

	got, err := party.New(r).CreateParty(context.Background(), usecase.CreatePartyInput{Name: "  B 朋友 "})
	if err != nil {
		t.Fatalf("CreateParty: %v", err)
	}
	if got.Name != "B 朋友" || got.ID == uuid.Nil || got.CreatedAt.IsZero() || got.ArchivedAt != nil {
		t.Fatalf("party = %+v, want the trimmed name, an id, the time the store assigned, and not archived", got)
	}
	if stored := r.parties[got.ID]; stored.Name != "B 朋友" {
		t.Fatalf("stored = %+v, want the trimmed name", stored)
	}
}

func TestCreatePartyRejectsABlankNameWithoutStoring(t *testing.T) {
	r := newFakeRepo()

	_, err := party.New(r).CreateParty(context.Background(), usecase.CreatePartyInput{Name: " \t "})
	if !errors.Is(err, model.ErrNameEmpty) {
		t.Fatalf("err = %v, want ErrNameEmpty", err)
	}
	if len(r.parties) != 0 {
		t.Fatalf("stored %d parties, want 0", len(r.parties))
	}
}

func TestCreatePartyReportsATakenName(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	svc := party.New(r)
	if _, err := svc.CreateParty(ctx, usecase.CreatePartyInput{Name: "B 朋友"}); err != nil {
		t.Fatalf("first CreateParty: %v", err)
	}

	_, err := svc.CreateParty(ctx, usecase.CreatePartyInput{Name: " B 朋友 "})
	if !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("err = %v, want ErrAlreadyExists", err)
	}
	if !strings.Contains(err.Error(), "B 朋友") {
		t.Errorf("err = %q, want it to name the party", err)
	}
	if len(r.parties) != 1 {
		t.Fatalf("stored %d parties, want 1", len(r.parties))
	}
}

func TestRenameParty(t *testing.T) {
	ctx := context.Background()

	t.Run("stores the trimmed name", func(t *testing.T) {
		r := newFakeRepo()
		id := r.seed(t, "B 朋友")

		got, err := party.New(r).RenameParty(ctx, usecase.RenamePartyInput{ID: id, Name: " C 家人 "})
		if err != nil {
			t.Fatalf("RenameParty: %v", err)
		}
		if got.ID != id || got.Name != "C 家人" || r.parties[id].Name != "C 家人" {
			t.Fatalf("party = %+v, stored = %+v, want the new trimmed name", got, r.parties[id])
		}
	})

	t.Run("rejects a blank name and keeps the old one", func(t *testing.T) {
		r := newFakeRepo()
		id := r.seed(t, "B 朋友")

		_, err := party.New(r).RenameParty(ctx, usecase.RenamePartyInput{ID: id, Name: "  "})
		if !errors.Is(err, model.ErrNameEmpty) {
			t.Fatalf("err = %v, want ErrNameEmpty", err)
		}
		if r.parties[id].Name != "B 朋友" {
			t.Fatalf("name = %q, want it unchanged", r.parties[id].Name)
		}
	})

	t.Run("reports an unknown party", func(t *testing.T) {
		unknown := uuid.Must(uuid.NewV7())

		_, err := party.New(newFakeRepo()).RenameParty(ctx, usecase.RenamePartyInput{ID: unknown, Name: "B 朋友"})
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if !strings.Contains(err.Error(), unknown.String()) {
			t.Errorf("err = %q, want it to name the id", err)
		}
	})

	t.Run("reports a taken name and keeps the old one", func(t *testing.T) {
		r := newFakeRepo()
		r.seed(t, "A 公司")
		id := r.seed(t, "B 朋友")

		_, err := party.New(r).RenameParty(ctx, usecase.RenamePartyInput{ID: id, Name: "A 公司"})
		if !errors.Is(err, model.ErrAlreadyExists) {
			t.Fatalf("err = %v, want ErrAlreadyExists", err)
		}
		if r.parties[id].Name != "B 朋友" {
			t.Fatalf("name = %q, want it unchanged", r.parties[id].Name)
		}
	})
}

func TestArchiveParty(t *testing.T) {
	ctx := context.Background()

	t.Run("archives", func(t *testing.T) {
		r := newFakeRepo()
		id := r.seed(t, "B 朋友")

		got, err := party.New(r).ArchiveParty(ctx, id)
		if err != nil {
			t.Fatalf("ArchiveParty: %v", err)
		}
		if got.ArchivedAt == nil || !r.parties[id].IsArchived() {
			t.Fatalf("party = %+v, stored = %+v, want it archived", got, r.parties[id])
		}
	})

	t.Run("reports an unknown party", func(t *testing.T) {
		_, err := party.New(newFakeRepo()).ArchiveParty(ctx, uuid.Must(uuid.NewV7()))
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestListPartiesKeepsArchivedOnesOutUnlessAsked(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	r.seed(t, "A 公司")
	r.seed(t, "B 朋友")
	archived := r.seed(t, "舊的")
	if _, err := r.Archive(ctx, archived); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	svc := party.New(r)

	inUse, err := svc.ListParties(ctx, usecase.ListPartiesInput{})
	if err != nil {
		t.Fatalf("ListParties: %v", err)
	}
	if r.gotIncludeArchived || len(inUse.Parties) != 2 {
		t.Fatalf("asked for archived = %v, got %d parties, want not asked and 2", r.gotIncludeArchived, len(inUse.Parties))
	}

	all, err := svc.ListParties(ctx, usecase.ListPartiesInput{IncludeArchived: true})
	if err != nil {
		t.Fatalf("ListParties(IncludeArchived): %v", err)
	}
	if !r.gotIncludeArchived || len(all.Parties) != 3 {
		t.Fatalf("asked for archived = %v, got %d parties, want asked and 3", r.gotIncludeArchived, len(all.Parties))
	}
	for i := 1; i < len(all.Parties); i++ {
		if all.Parties[i-1].Name > all.Parties[i].Name {
			t.Fatalf("parties = %+v, want the order the repo gave (by name)", all.Parties)
		}
	}
}

func TestGetParty(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	id := r.seed(t, "B 朋友")
	svc := party.New(r)

	got, err := svc.GetParty(ctx, id)
	if err != nil || got.ID != id || got.Name != "B 朋友" {
		t.Fatalf("GetParty = %+v, %v, want the party", got, err)
	}

	if _, err := svc.GetParty(ctx, uuid.Must(uuid.NewV7())); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestPartyUseCasesReportStoreFailures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("connection reset")
	r := newFakeRepo()
	id := r.seed(t, "B 朋友")
	r.err = boom
	svc := party.New(r)

	calls := map[string]func() error{
		"CreateParty":  func() error { _, err := svc.CreateParty(ctx, usecase.CreatePartyInput{Name: "x"}); return err },
		"RenameParty":  func() error { _, err := svc.RenameParty(ctx, usecase.RenamePartyInput{ID: id, Name: "x"}); return err },
		"ArchiveParty": func() error { _, err := svc.ArchiveParty(ctx, id); return err },
		"ListParties":  func() error { _, err := svc.ListParties(ctx, usecase.ListPartiesInput{}); return err },
		"GetParty":     func() error { _, err := svc.GetParty(ctx, id); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, boom) {
			t.Errorf("%s: err = %v, want the store failure", name, err)
		}
	}
}
