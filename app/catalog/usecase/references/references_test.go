package references_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/catalog/domain/model"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
	"github.com/maxjkfc/maxos/app/catalog/usecase/references"
)

var archivedAt = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func newID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// The fakes hold the entries that exist, answer GetByIDs for them and record
// what they were asked, so that the tests can see how many calls were made and
// for which ids. A fake whose err is set fails every call with it.

type fakeProjects struct {
	repo.ProjectRepo
	byID  map[uuid.UUID]model.Project
	asked [][]uuid.UUID
	err   error
}

func (f *fakeProjects) GetByIDs(_ context.Context, ids []uuid.UUID) ([]model.Project, error) {
	f.asked = append(f.asked, ids)
	if f.err != nil {
		return nil, f.err
	}
	var out []model.Project
	for _, id := range ids {
		if p, ok := f.byID[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

type fakeAreas struct {
	repo.AreaRepo
	byID  map[uuid.UUID]model.Area
	asked [][]uuid.UUID
}

func (f *fakeAreas) GetByIDs(_ context.Context, ids []uuid.UUID) ([]model.Area, error) {
	f.asked = append(f.asked, ids)
	var out []model.Area
	for _, id := range ids {
		if a, ok := f.byID[id]; ok {
			out = append(out, a)
		}
	}
	return out, nil
}

type fakeParties struct {
	repo.PartyRepo
	byID  map[uuid.UUID]model.Party
	asked [][]uuid.UUID
}

func (f *fakeParties) GetByIDs(_ context.Context, ids []uuid.UUID) ([]model.Party, error) {
	f.asked = append(f.asked, ids)
	var out []model.Party
	for _, id := range ids {
		if p, ok := f.byID[id]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

type fakeTags struct {
	repo.TagRepo
	byID  map[uuid.UUID]model.Tag
	asked [][]uuid.UUID
}

func (f *fakeTags) GetByIDs(_ context.Context, ids []uuid.UUID) ([]model.Tag, error) {
	f.asked = append(f.asked, ids)
	var out []model.Tag
	for _, id := range ids {
		if tag, ok := f.byID[id]; ok {
			out = append(out, tag)
		}
	}
	return out, nil
}

// world is a catalog of one entry of each kind in use, and one archived entry of
// each kind, behind the fakes.
type world struct {
	projects *fakeProjects
	areas    *fakeAreas
	parties  *fakeParties
	tags     *fakeTags

	project, archivedProject uuid.UUID
	area, archivedArea       uuid.UUID
	party, archivedParty     uuid.UUID
	tag, archivedTag         uuid.UUID
}

func newWorld() *world {
	w := &world{
		projects: &fakeProjects{byID: map[uuid.UUID]model.Project{}},
		areas:    &fakeAreas{byID: map[uuid.UUID]model.Area{}},
		parties:  &fakeParties{byID: map[uuid.UUID]model.Party{}},
		tags:     &fakeTags{byID: map[uuid.UUID]model.Tag{}},

		project: newID(), archivedProject: newID(),
		area: newID(), archivedArea: newID(),
		party: newID(), archivedParty: newID(),
		tag: newID(), archivedTag: newID(),
	}
	w.projects.byID[w.project] = model.Project{ID: w.project}
	w.projects.byID[w.archivedProject] = model.Project{ID: w.archivedProject, ArchivedAt: &archivedAt}
	w.areas.byID[w.area] = model.Area{ID: w.area}
	w.areas.byID[w.archivedArea] = model.Area{ID: w.archivedArea, ArchivedAt: &archivedAt}
	w.parties.byID[w.party] = model.Party{ID: w.party}
	w.parties.byID[w.archivedParty] = model.Party{ID: w.archivedParty, ArchivedAt: &archivedAt}
	w.tags.byID[w.tag] = model.Tag{ID: w.tag}
	w.tags.byID[w.archivedTag] = model.Tag{ID: w.archivedTag, ArchivedAt: &archivedAt}
	return w
}

func (w *world) service() *references.Service {
	return references.New(w.projects, w.areas, w.parties, w.tags)
}

func TestVerifyReferencesAcceptsEntriesInUse(t *testing.T) {
	w := newWorld()

	err := w.service().VerifyReferences(context.Background(), usecase.VerifyReferencesInput{
		ProjectIDs: []uuid.UUID{w.project},
		AreaIDs:    []uuid.UUID{w.area},
		PartyIDs:   []uuid.UUID{w.party},
		TagIDs:     []uuid.UUID{w.tag, newTagInUse(w)},
	})
	if err != nil {
		t.Fatalf("VerifyReferences: %v", err)
	}
}

// newTagInUse adds a second tag in use and returns its id.
func newTagInUse(w *world) uuid.UUID {
	id := newID()
	w.tags.byID[id] = model.Tag{ID: id}
	return id
}

func TestVerifyReferencesChecksEachKindInOneCallAndSkipsEmptyOnes(t *testing.T) {
	w := newWorld()
	second := newTagInUse(w)

	err := w.service().VerifyReferences(context.Background(), usecase.VerifyReferencesInput{
		AreaIDs: []uuid.UUID{w.area},
		TagIDs:  []uuid.UUID{w.tag, second},
	})
	if err != nil {
		t.Fatalf("VerifyReferences: %v", err)
	}

	if len(w.projects.asked) != 0 || len(w.parties.asked) != 0 {
		t.Errorf("asked for projects %v and parties %v, want no call for a kind without ids", w.projects.asked, w.parties.asked)
	}
	if len(w.areas.asked) != 1 {
		t.Errorf("asked for areas %d times, want 1", len(w.areas.asked))
	}
	if len(w.tags.asked) != 1 || len(w.tags.asked[0]) != 2 {
		t.Errorf("asked for tags %v, want one call with both ids", w.tags.asked)
	}
}

func TestVerifyReferencesReportsEveryIDThatIsNoGood(t *testing.T) {
	w := newWorld()
	missingProject, missingParty := newID(), newID()

	err := w.service().VerifyReferences(context.Background(), usecase.VerifyReferencesInput{
		ProjectIDs: []uuid.UUID{w.project, missingProject},
		AreaIDs:    []uuid.UUID{w.archivedArea},
		PartyIDs:   []uuid.UUID{missingParty},
		TagIDs:     []uuid.UUID{w.tag, w.archivedTag},
	})

	var refErr *usecase.ReferenceError
	if !errors.As(err, &refErr) {
		t.Fatalf("err = %v, want a ReferenceError", err)
	}
	want := []usecase.ReferenceProblem{
		{Kind: usecase.ReferenceProject, ID: missingProject, Archived: false},
		{Kind: usecase.ReferenceArea, ID: w.archivedArea, Archived: true},
		{Kind: usecase.ReferenceParty, ID: missingParty, Archived: false},
		{Kind: usecase.ReferenceTag, ID: w.archivedTag, Archived: true},
	}
	if len(refErr.Problems) != len(want) {
		t.Fatalf("problems = %+v, want %+v", refErr.Problems, want)
	}
	for i := range want {
		if refErr.Problems[i] != want[i] {
			t.Errorf("problems[%d] = %+v, want %+v", i, refErr.Problems[i], want[i])
		}
	}

	// The message tells the caller which id is wrong and why.
	for _, part := range []string{
		"project " + missingProject.String() + ": not found",
		"area " + w.archivedArea.String() + ": archived",
		"tag " + w.archivedTag.String() + ": archived",
	} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("err = %q, want it to contain %q", err, part)
		}
	}
}

func TestVerifyReferencesAsksOnceForAnIDGivenTwice(t *testing.T) {
	w := newWorld()
	missing := newID()

	err := w.service().VerifyReferences(context.Background(), usecase.VerifyReferencesInput{
		AreaIDs: []uuid.UUID{missing, w.area, missing},
	})

	var refErr *usecase.ReferenceError
	if !errors.As(err, &refErr) || len(refErr.Problems) != 1 || refErr.Problems[0].ID != missing {
		t.Fatalf("err = %v, want one problem for the missing area", err)
	}
	if len(w.areas.asked) != 1 || len(w.areas.asked[0]) != 2 {
		t.Fatalf("asked for areas %v, want one call with the 2 distinct ids", w.areas.asked)
	}
}

func TestVerifyReferencesPassesOnAStoreFailure(t *testing.T) {
	w := newWorld()
	boom := errors.New("connection reset")
	w.projects.err = boom

	err := w.service().VerifyReferences(context.Background(), usecase.VerifyReferencesInput{
		ProjectIDs: []uuid.UUID{w.project},
	})
	var refErr *usecase.ReferenceError
	if !errors.Is(err, boom) || errors.As(err, &refErr) {
		t.Fatalf("err = %v, want the store failure, not a ReferenceError", err)
	}
}
