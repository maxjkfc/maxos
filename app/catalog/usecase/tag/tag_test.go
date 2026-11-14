package tag_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/catalog/domain/model"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
	"github.com/maxjkfc/maxos/app/catalog/usecase/tag"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fakeRepo is an in-memory repo.TagRepo. The method the tag use cases do not
// call, GetByIDs, is left to the embedded nil interface.
type fakeRepo struct {
	repo.TagRepo

	tags map[uuid.UUID]model.Tag
	// err, when set, is what every method returns.
	err error
	// gotIncludeArchived is the flag List was last asked with.
	gotIncludeArchived bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{tags: map[uuid.UUID]model.Tag{}}
}

func (f *fakeRepo) nameTaken(name string, except uuid.UUID) bool {
	for id, g := range f.tags {
		if g.Name == name && id != except {
			return true
		}
	}
	return false
}

func (f *fakeRepo) Insert(_ context.Context, g model.Tag) (model.Tag, error) {
	if f.err != nil {
		return model.Tag{}, f.err
	}
	if f.nameTaken(g.Name, g.ID) {
		return model.Tag{}, model.ErrAlreadyExists
	}
	g.CreatedAt = epoch.Add(time.Duration(len(f.tags)) * time.Second)
	f.tags[g.ID] = g
	return g, nil
}

func (f *fakeRepo) Get(_ context.Context, id uuid.UUID) (model.Tag, error) {
	if f.err != nil {
		return model.Tag{}, f.err
	}
	g, ok := f.tags[id]
	if !ok {
		return model.Tag{}, model.ErrNotFound
	}
	return g, nil
}

func (f *fakeRepo) List(_ context.Context, includeArchived bool) ([]model.Tag, error) {
	f.gotIncludeArchived = includeArchived
	if f.err != nil {
		return nil, f.err
	}
	var out []model.Tag
	for _, g := range f.tags {
		if includeArchived || !g.IsArchived() {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fakeRepo) Rename(_ context.Context, id uuid.UUID, name string) (model.Tag, error) {
	if f.err != nil {
		return model.Tag{}, f.err
	}
	g, ok := f.tags[id]
	if !ok {
		return model.Tag{}, model.ErrNotFound
	}
	if f.nameTaken(name, id) {
		return model.Tag{}, model.ErrAlreadyExists
	}
	g.Name = name
	f.tags[id] = g
	return g, nil
}

func (f *fakeRepo) Archive(_ context.Context, id uuid.UUID) (model.Tag, error) {
	if f.err != nil {
		return model.Tag{}, f.err
	}
	g, ok := f.tags[id]
	if !ok {
		return model.Tag{}, model.ErrNotFound
	}
	if g.ArchivedAt == nil {
		at := epoch.Add(time.Hour)
		g.ArchivedAt = &at
		f.tags[id] = g
	}
	return g, nil
}

// seed stores a tag directly, as an earlier call would have.
func (f *fakeRepo) seed(t *testing.T, name string) uuid.UUID {
	t.Helper()
	g, err := model.NewTag(uuid.Must(uuid.NewV7()), name)
	if err != nil {
		t.Fatalf("NewTag: %v", err)
	}
	f.tags[g.ID] = g
	return g.ID
}

func TestCreateTagStoresTheTrimmedName(t *testing.T) {
	r := newFakeRepo()

	got, err := tag.New(r).CreateTag(context.Background(), usecase.CreateTagInput{Name: "  b旅遊 "})
	if err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	if got.Name != "b旅遊" || got.ID == uuid.Nil || got.CreatedAt.IsZero() || got.ArchivedAt != nil {
		t.Fatalf("tag = %+v, want the trimmed name, an id, the time the store assigned, and not archived", got)
	}
	if stored := r.tags[got.ID]; stored.Name != "b旅遊" {
		t.Fatalf("stored = %+v, want the trimmed name", stored)
	}
}

func TestCreateTagRejectsABlankNameWithoutStoring(t *testing.T) {
	r := newFakeRepo()

	_, err := tag.New(r).CreateTag(context.Background(), usecase.CreateTagInput{Name: " \t "})
	if !errors.Is(err, model.ErrNameEmpty) {
		t.Fatalf("err = %v, want ErrNameEmpty", err)
	}
	if len(r.tags) != 0 {
		t.Fatalf("stored %d tags, want 0", len(r.tags))
	}
}

func TestCreateTagReportsATakenName(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	svc := tag.New(r)
	if _, err := svc.CreateTag(ctx, usecase.CreateTagInput{Name: "b旅遊"}); err != nil {
		t.Fatalf("first CreateTag: %v", err)
	}

	_, err := svc.CreateTag(ctx, usecase.CreateTagInput{Name: " b旅遊 "})
	if !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("err = %v, want ErrAlreadyExists", err)
	}
	if !strings.Contains(err.Error(), "b旅遊") {
		t.Errorf("err = %q, want it to name the tag", err)
	}
	if len(r.tags) != 1 {
		t.Fatalf("stored %d tags, want 1", len(r.tags))
	}
}

func TestRenameTag(t *testing.T) {
	ctx := context.Background()

	t.Run("stores the trimmed name", func(t *testing.T) {
		r := newFakeRepo()
		id := r.seed(t, "b旅遊")

		got, err := tag.New(r).RenameTag(ctx, usecase.RenameTagInput{ID: id, Name: " c出差 "})
		if err != nil {
			t.Fatalf("RenameTag: %v", err)
		}
		if got.ID != id || got.Name != "c出差" || r.tags[id].Name != "c出差" {
			t.Fatalf("tag = %+v, stored = %+v, want the new trimmed name", got, r.tags[id])
		}
	})

	t.Run("rejects a blank name and keeps the old one", func(t *testing.T) {
		r := newFakeRepo()
		id := r.seed(t, "b旅遊")

		_, err := tag.New(r).RenameTag(ctx, usecase.RenameTagInput{ID: id, Name: "  "})
		if !errors.Is(err, model.ErrNameEmpty) {
			t.Fatalf("err = %v, want ErrNameEmpty", err)
		}
		if r.tags[id].Name != "b旅遊" {
			t.Fatalf("name = %q, want it unchanged", r.tags[id].Name)
		}
	})

	t.Run("reports an unknown tag", func(t *testing.T) {
		unknown := uuid.Must(uuid.NewV7())

		_, err := tag.New(newFakeRepo()).RenameTag(ctx, usecase.RenameTagInput{ID: unknown, Name: "b旅遊"})
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if !strings.Contains(err.Error(), unknown.String()) {
			t.Errorf("err = %q, want it to name the id", err)
		}
	})

	t.Run("reports a taken name and keeps the old one", func(t *testing.T) {
		r := newFakeRepo()
		r.seed(t, "a報銷")
		id := r.seed(t, "b旅遊")

		_, err := tag.New(r).RenameTag(ctx, usecase.RenameTagInput{ID: id, Name: "a報銷"})
		if !errors.Is(err, model.ErrAlreadyExists) {
			t.Fatalf("err = %v, want ErrAlreadyExists", err)
		}
		if r.tags[id].Name != "b旅遊" {
			t.Fatalf("name = %q, want it unchanged", r.tags[id].Name)
		}
	})
}

func TestArchiveTag(t *testing.T) {
	ctx := context.Background()

	t.Run("archives", func(t *testing.T) {
		r := newFakeRepo()
		id := r.seed(t, "b旅遊")

		got, err := tag.New(r).ArchiveTag(ctx, id)
		if err != nil {
			t.Fatalf("ArchiveTag: %v", err)
		}
		if got.ArchivedAt == nil || !r.tags[id].IsArchived() {
			t.Fatalf("tag = %+v, stored = %+v, want it archived", got, r.tags[id])
		}
	})

	t.Run("reports an unknown tag", func(t *testing.T) {
		_, err := tag.New(newFakeRepo()).ArchiveTag(ctx, uuid.Must(uuid.NewV7()))
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestListTagsKeepsArchivedOnesOutUnlessAsked(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	r.seed(t, "a報銷")
	r.seed(t, "b旅遊")
	archived := r.seed(t, "舊的")
	if _, err := r.Archive(ctx, archived); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	svc := tag.New(r)

	inUse, err := svc.ListTags(ctx, usecase.ListTagsInput{})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	if r.gotIncludeArchived || len(inUse.Tags) != 2 {
		t.Fatalf("asked for archived = %v, got %d tags, want not asked and 2", r.gotIncludeArchived, len(inUse.Tags))
	}

	all, err := svc.ListTags(ctx, usecase.ListTagsInput{IncludeArchived: true})
	if err != nil {
		t.Fatalf("ListTags(IncludeArchived): %v", err)
	}
	if !r.gotIncludeArchived || len(all.Tags) != 3 {
		t.Fatalf("asked for archived = %v, got %d tags, want asked and 3", r.gotIncludeArchived, len(all.Tags))
	}
	for i := 1; i < len(all.Tags); i++ {
		if all.Tags[i-1].Name > all.Tags[i].Name {
			t.Fatalf("tags = %+v, want the order the repo gave (by name)", all.Tags)
		}
	}
}

func TestGetTag(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	id := r.seed(t, "b旅遊")
	svc := tag.New(r)

	got, err := svc.GetTag(ctx, id)
	if err != nil || got.ID != id || got.Name != "b旅遊" {
		t.Fatalf("GetTag = %+v, %v, want the tag", got, err)
	}

	if _, err := svc.GetTag(ctx, uuid.Must(uuid.NewV7())); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestTagUseCasesReportStoreFailures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("connection reset")
	r := newFakeRepo()
	id := r.seed(t, "b旅遊")
	r.err = boom
	svc := tag.New(r)

	calls := map[string]func() error{
		"CreateTag":  func() error { _, err := svc.CreateTag(ctx, usecase.CreateTagInput{Name: "x"}); return err },
		"RenameTag":  func() error { _, err := svc.RenameTag(ctx, usecase.RenameTagInput{ID: id, Name: "x"}); return err },
		"ArchiveTag": func() error { _, err := svc.ArchiveTag(ctx, id); return err },
		"ListTags":   func() error { _, err := svc.ListTags(ctx, usecase.ListTagsInput{}); return err },
		"GetTag":     func() error { _, err := svc.GetTag(ctx, id); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, boom) {
			t.Errorf("%s: err = %v, want the store failure", name, err)
		}
	}
}
