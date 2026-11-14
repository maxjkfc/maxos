package project_test

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
	"github.com/maxjkfc/maxos/app/catalog/usecase/project"
)

// day returns midnight UTC on the given day of December 2026.
func day(d int) time.Time {
	return time.Date(2026, time.December, d, 0, 0, 0, 0, time.UTC)
}

func period(from, to int) *usecase.Period {
	return &usecase.Period{From: day(from), To: day(to)}
}

// fakeProjects is an in-memory repo.ProjectRepo. The method the project use
// cases do not call, GetByIDs, is left to the embedded nil interface.
type fakeProjects struct {
	repo.ProjectRepo

	projects map[uuid.UUID]model.Project
	// err, when set, is what every method returns.
	err error

	// activeOn is what ListActiveOn answers, and gotDay the day it was asked for.
	activeOn []model.Project
	gotDay   time.Time
}

func newFakeProjects() *fakeProjects {
	return &fakeProjects{projects: map[uuid.UUID]model.Project{}}
}

func (f *fakeProjects) nameTaken(name string, except uuid.UUID) bool {
	for id, p := range f.projects {
		if p.Name == name && id != except {
			return true
		}
	}
	return false
}

func (f *fakeProjects) Insert(_ context.Context, p model.Project) (model.Project, error) {
	if f.err != nil {
		return model.Project{}, f.err
	}
	if f.nameTaken(p.Name, p.ID) {
		return model.Project{}, model.ErrAlreadyExists
	}
	p.CreatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f.projects[p.ID] = p
	return p, nil
}

func (f *fakeProjects) Get(_ context.Context, id uuid.UUID) (model.Project, error) {
	if f.err != nil {
		return model.Project{}, f.err
	}
	p, ok := f.projects[id]
	if !ok {
		return model.Project{}, model.ErrNotFound
	}
	return p, nil
}

func (f *fakeProjects) update(id uuid.UUID, change func(*model.Project)) (model.Project, error) {
	if f.err != nil {
		return model.Project{}, f.err
	}
	p, ok := f.projects[id]
	if !ok {
		return model.Project{}, model.ErrNotFound
	}
	change(&p)
	f.projects[id] = p
	return p, nil
}

func (f *fakeProjects) Rename(_ context.Context, id uuid.UUID, name string) (model.Project, error) {
	if f.nameTaken(name, id) {
		return model.Project{}, model.ErrAlreadyExists
	}
	return f.update(id, func(p *model.Project) { p.Name = name })
}

func (f *fakeProjects) SetPeriod(_ context.Context, id uuid.UUID, period *model.Period) (model.Project, error) {
	return f.update(id, func(p *model.Project) { p.Period = period })
}

func (f *fakeProjects) SetActivePeriod(_ context.Context, id uuid.UUID, period *model.Period) (model.Project, error) {
	return f.update(id, func(p *model.Project) { p.ActivePeriod = period })
}

func (f *fakeProjects) SetDefaultArea(_ context.Context, id uuid.UUID, areaID uuid.UUID) (model.Project, error) {
	return f.update(id, func(p *model.Project) { p.DefaultAreaID = &areaID })
}

func (f *fakeProjects) ListActiveOn(_ context.Context, d time.Time) ([]model.Project, error) {
	f.gotDay = d
	if f.err != nil {
		return nil, f.err
	}
	return f.activeOn, nil
}

func (f *fakeProjects) Archive(_ context.Context, id uuid.UUID) (model.Project, error) {
	return f.update(id, func(p *model.Project) {
		if p.ArchivedAt == nil {
			at := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
			p.ArchivedAt = &at
		}
	})
}

func (f *fakeProjects) List(_ context.Context, includeArchived bool) ([]model.Project, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []model.Project
	for _, p := range f.projects {
		if includeArchived || !p.IsArchived() {
			out = append(out, p)
		}
	}
	return out, nil
}

// seed stores a project directly, as an earlier call would have.
func (f *fakeProjects) seed(t *testing.T, name string) model.Project {
	t.Helper()
	p, err := model.NewProject(uuid.Must(uuid.NewV7()), name)
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}
	f.projects[p.ID] = p
	return p
}

// fakeAreas is an in-memory repo.AreaRepo of which the project use cases call
// Get only.
type fakeAreas struct {
	repo.AreaRepo

	areas map[uuid.UUID]model.Area
	err   error
}

func newFakeAreas() *fakeAreas {
	return &fakeAreas{areas: map[uuid.UUID]model.Area{}}
}

func (f *fakeAreas) Get(_ context.Context, id uuid.UUID) (model.Area, error) {
	if f.err != nil {
		return model.Area{}, f.err
	}
	a, ok := f.areas[id]
	if !ok {
		return model.Area{}, model.ErrNotFound
	}
	return a, nil
}

// seed stores an area, archived when archived is set.
func (f *fakeAreas) seed(t *testing.T, name string, archived bool) model.Area {
	t.Helper()
	a, err := model.NewArea(uuid.Must(uuid.NewV7()), name)
	if err != nil {
		t.Fatalf("NewArea: %v", err)
	}
	if archived {
		at := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		a.ArchivedAt = &at
	}
	f.areas[a.ID] = a
	return a
}

func TestCreateProjectStoresWhatItIsGiven(t *testing.T) {
	projects, areas := newFakeProjects(), newFakeAreas()
	area := areas.seed(t, "玩樂", false)
	svc := project.New(projects, areas)

	got, err := svc.CreateProject(context.Background(), usecase.CreateProjectInput{
		Name: "  日本旅行 ",
		// The times of day do not matter: only the day does.
		Period:        &usecase.Period{From: day(1).Add(15 * time.Hour), To: day(8).Add(2 * time.Hour)},
		ActivePeriod:  period(2, 7),
		DefaultAreaID: &area.ID,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if got.Name != "日本旅行" || got.ID == uuid.Nil || got.CreatedAt.IsZero() || got.ArchivedAt != nil {
		t.Fatalf("project = %+v, want the trimmed name, an id, the time the store assigned, and not archived", got)
	}
	if got.Period == nil || !got.Period.From.Equal(day(1)) || !got.Period.To.Equal(day(8)) {
		t.Errorf("Period = %+v, want the 1st to the 8th at midnight", got.Period)
	}
	if got.ActivePeriod == nil || !got.ActivePeriod.From.Equal(day(2)) || !got.ActivePeriod.To.Equal(day(7)) {
		t.Errorf("ActivePeriod = %+v, want the 2nd to the 7th", got.ActivePeriod)
	}
	if got.DefaultAreaID == nil || *got.DefaultAreaID != area.ID {
		t.Errorf("DefaultAreaID = %v, want %s", got.DefaultAreaID, area.ID)
	}
	if stored := projects.projects[got.ID]; stored.Name != "日本旅行" {
		t.Errorf("stored = %+v, want the project", stored)
	}
}

func TestCreateProjectNeedsOnlyAName(t *testing.T) {
	got, err := project.New(newFakeProjects(), newFakeAreas()).CreateProject(context.Background(), usecase.CreateProjectInput{Name: "尾牙"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if got.Period != nil || got.ActivePeriod != nil || got.DefaultAreaID != nil {
		t.Fatalf("project = %+v, want no periods and no default area", got)
	}
}

func TestCreateProjectRejectsWhatIsInvalidWithoutStoring(t *testing.T) {
	archivedAreaFor := func(areas *fakeAreas) *uuid.UUID {
		id := areas.seed(t, "舊的", true).ID
		return &id
	}
	unknownArea := uuid.Must(uuid.NewV7())

	tests := []struct {
		name  string
		input func(areas *fakeAreas) usecase.CreateProjectInput
		want  error
	}{
		{
			name:  "blank name",
			input: func(*fakeAreas) usecase.CreateProjectInput { return usecase.CreateProjectInput{Name: "  "} },
			want:  model.ErrNameEmpty,
		},
		{
			name: "period reversed",
			input: func(*fakeAreas) usecase.CreateProjectInput {
				return usecase.CreateProjectInput{Name: "x", Period: period(8, 1)}
			},
			want: model.ErrPeriodReversed,
		},
		{
			name: "active period reversed",
			input: func(*fakeAreas) usecase.CreateProjectInput {
				return usecase.CreateProjectInput{Name: "x", ActivePeriod: period(7, 2)}
			},
			want: model.ErrPeriodReversed,
		},
		{
			name: "active period outside the period",
			input: func(*fakeAreas) usecase.CreateProjectInput {
				return usecase.CreateProjectInput{Name: "x", Period: period(2, 8), ActivePeriod: period(1, 3)}
			},
			want: model.ErrActivePeriodOutsidePeriod,
		},
		{
			name: "default area that does not exist",
			input: func(*fakeAreas) usecase.CreateProjectInput {
				return usecase.CreateProjectInput{Name: "x", DefaultAreaID: &unknownArea}
			},
			want: model.ErrNotFound,
		},
		{
			name: "default area that is archived",
			input: func(areas *fakeAreas) usecase.CreateProjectInput {
				return usecase.CreateProjectInput{Name: "x", DefaultAreaID: archivedAreaFor(areas)}
			},
			want: model.ErrArchived,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projects, areas := newFakeProjects(), newFakeAreas()

			_, err := project.New(projects, areas).CreateProject(context.Background(), tt.input(areas))
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if len(projects.projects) != 0 {
				t.Fatalf("stored %d projects, want 0", len(projects.projects))
			}
		})
	}
}

func TestCreateProjectReportsATakenName(t *testing.T) {
	projects := newFakeProjects()
	projects.seed(t, "日本旅行")

	_, err := project.New(projects, newFakeAreas()).CreateProject(context.Background(), usecase.CreateProjectInput{Name: " 日本旅行 "})
	if !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("err = %v, want ErrAlreadyExists", err)
	}
	if !strings.Contains(err.Error(), "日本旅行") {
		t.Errorf("err = %q, want it to name the project", err)
	}
}

func TestRenameProject(t *testing.T) {
	ctx := context.Background()

	t.Run("stores the trimmed name", func(t *testing.T) {
		projects := newFakeProjects()
		p := projects.seed(t, "日本旅行")

		got, err := project.New(projects, newFakeAreas()).RenameProject(ctx, usecase.RenameProjectInput{ID: p.ID, Name: " 東京旅行 "})
		if err != nil || got.Name != "東京旅行" || projects.projects[p.ID].Name != "東京旅行" {
			t.Fatalf("RenameProject = %+v, %v, want the new trimmed name stored", got, err)
		}
	})

	t.Run("rejects a blank name and keeps the old one", func(t *testing.T) {
		projects := newFakeProjects()
		p := projects.seed(t, "日本旅行")

		_, err := project.New(projects, newFakeAreas()).RenameProject(ctx, usecase.RenameProjectInput{ID: p.ID, Name: " "})
		if !errors.Is(err, model.ErrNameEmpty) || projects.projects[p.ID].Name != "日本旅行" {
			t.Fatalf("err = %v, name = %q, want ErrNameEmpty and the old name", err, projects.projects[p.ID].Name)
		}
	})

	t.Run("reports an unknown project", func(t *testing.T) {
		_, err := project.New(newFakeProjects(), newFakeAreas()).RenameProject(ctx, usecase.RenameProjectInput{ID: uuid.Must(uuid.NewV7()), Name: "x"})
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestSetProjectPeriod(t *testing.T) {
	ctx := context.Background()

	t.Run("sets and clears the period", func(t *testing.T) {
		projects := newFakeProjects()
		p := projects.seed(t, "日本旅行")
		svc := project.New(projects, newFakeAreas())

		got, err := svc.SetProjectPeriod(ctx, usecase.SetProjectPeriodInput{ID: p.ID, Period: period(1, 8)})
		if err != nil || got.Period == nil || !got.Period.To.Equal(day(8)) {
			t.Fatalf("SetProjectPeriod = %+v, %v, want the period", got, err)
		}
		got, err = svc.SetProjectPeriod(ctx, usecase.SetProjectPeriodInput{ID: p.ID})
		if err != nil || got.Period != nil {
			t.Fatalf("SetProjectPeriod(nil) = %+v, %v, want the period cleared", got, err)
		}
	})

	t.Run("rejects a reversed period", func(t *testing.T) {
		projects := newFakeProjects()
		p := projects.seed(t, "日本旅行")

		_, err := project.New(projects, newFakeAreas()).SetProjectPeriod(ctx, usecase.SetProjectPeriodInput{ID: p.ID, Period: period(8, 1)})
		if !errors.Is(err, model.ErrPeriodReversed) || projects.projects[p.ID].Period != nil {
			t.Fatalf("err = %v, want ErrPeriodReversed and no period stored", err)
		}
	})

	t.Run("refuses a period that leaves the active period outside, and keeps the old one", func(t *testing.T) {
		projects := newFakeProjects()
		p := projects.seed(t, "日本旅行")
		svc := project.New(projects, newFakeAreas())
		if _, err := svc.SetProjectPeriod(ctx, usecase.SetProjectPeriodInput{ID: p.ID, Period: period(1, 8)}); err != nil {
			t.Fatalf("SetProjectPeriod: %v", err)
		}
		if _, err := svc.SetProjectActivePeriod(ctx, usecase.SetProjectActivePeriodInput{ID: p.ID, ActivePeriod: period(2, 7)}); err != nil {
			t.Fatalf("SetProjectActivePeriod: %v", err)
		}

		_, err := svc.SetProjectPeriod(ctx, usecase.SetProjectPeriodInput{ID: p.ID, Period: period(3, 8)})
		if !errors.Is(err, model.ErrActivePeriodOutsidePeriod) {
			t.Fatalf("err = %v, want ErrActivePeriodOutsidePeriod", err)
		}
		if got := projects.projects[p.ID].Period; got == nil || !got.From.Equal(day(1)) {
			t.Fatalf("Period = %v, want the old period kept", got)
		}
	})

	t.Run("reports an unknown project", func(t *testing.T) {
		_, err := project.New(newFakeProjects(), newFakeAreas()).SetProjectPeriod(ctx, usecase.SetProjectPeriodInput{ID: uuid.Must(uuid.NewV7()), Period: period(1, 8)})
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestSetProjectActivePeriod(t *testing.T) {
	ctx := context.Background()

	t.Run("sets and clears the active period", func(t *testing.T) {
		projects := newFakeProjects()
		p := projects.seed(t, "日本旅行")
		svc := project.New(projects, newFakeAreas())

		got, err := svc.SetProjectActivePeriod(ctx, usecase.SetProjectActivePeriodInput{ID: p.ID, ActivePeriod: period(2, 7)})
		if err != nil || got.ActivePeriod == nil || !got.ActivePeriod.From.Equal(day(2)) {
			t.Fatalf("SetProjectActivePeriod = %+v, %v, want the active period", got, err)
		}
		got, err = svc.SetProjectActivePeriod(ctx, usecase.SetProjectActivePeriodInput{ID: p.ID})
		if err != nil || got.ActivePeriod != nil {
			t.Fatalf("SetProjectActivePeriod(nil) = %+v, %v, want it cleared", got, err)
		}
	})

	t.Run("refuses an active period outside the period", func(t *testing.T) {
		projects := newFakeProjects()
		p := projects.seed(t, "日本旅行")
		svc := project.New(projects, newFakeAreas())
		if _, err := svc.SetProjectPeriod(ctx, usecase.SetProjectPeriodInput{ID: p.ID, Period: period(2, 8)}); err != nil {
			t.Fatalf("SetProjectPeriod: %v", err)
		}

		_, err := svc.SetProjectActivePeriod(ctx, usecase.SetProjectActivePeriodInput{ID: p.ID, ActivePeriod: period(1, 3)})
		if !errors.Is(err, model.ErrActivePeriodOutsidePeriod) || projects.projects[p.ID].ActivePeriod != nil {
			t.Fatalf("err = %v, want ErrActivePeriodOutsidePeriod and nothing stored", err)
		}
	})

	t.Run("rejects a reversed active period", func(t *testing.T) {
		projects := newFakeProjects()
		p := projects.seed(t, "日本旅行")

		_, err := project.New(projects, newFakeAreas()).SetProjectActivePeriod(ctx, usecase.SetProjectActivePeriodInput{ID: p.ID, ActivePeriod: period(7, 2)})
		if !errors.Is(err, model.ErrPeriodReversed) {
			t.Fatalf("err = %v, want ErrPeriodReversed", err)
		}
	})

	t.Run("reports an unknown project", func(t *testing.T) {
		_, err := project.New(newFakeProjects(), newFakeAreas()).SetProjectActivePeriod(ctx, usecase.SetProjectActivePeriodInput{ID: uuid.Must(uuid.NewV7()), ActivePeriod: period(1, 8)})
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestSetProjectDefaultArea(t *testing.T) {
	ctx := context.Background()

	t.Run("sets an area that is in use", func(t *testing.T) {
		projects, areas := newFakeProjects(), newFakeAreas()
		p := projects.seed(t, "日本旅行")
		area := areas.seed(t, "玩樂", false)

		got, err := project.New(projects, areas).SetProjectDefaultArea(ctx, usecase.SetProjectDefaultAreaInput{ID: p.ID, AreaID: area.ID})
		if err != nil || got.DefaultAreaID == nil || *got.DefaultAreaID != area.ID {
			t.Fatalf("SetProjectDefaultArea = %+v, %v, want the area", got, err)
		}
	})

	t.Run("refuses an area that does not exist", func(t *testing.T) {
		projects := newFakeProjects()
		p := projects.seed(t, "日本旅行")

		_, err := project.New(projects, newFakeAreas()).SetProjectDefaultArea(ctx, usecase.SetProjectDefaultAreaInput{ID: p.ID, AreaID: uuid.Must(uuid.NewV7())})
		if !errors.Is(err, model.ErrNotFound) || projects.projects[p.ID].DefaultAreaID != nil {
			t.Fatalf("err = %v, want ErrNotFound and nothing stored", err)
		}
	})

	t.Run("refuses an archived area", func(t *testing.T) {
		projects, areas := newFakeProjects(), newFakeAreas()
		p := projects.seed(t, "日本旅行")
		area := areas.seed(t, "舊的", true)

		_, err := project.New(projects, areas).SetProjectDefaultArea(ctx, usecase.SetProjectDefaultAreaInput{ID: p.ID, AreaID: area.ID})
		if !errors.Is(err, model.ErrArchived) || projects.projects[p.ID].DefaultAreaID != nil {
			t.Fatalf("err = %v, want ErrArchived and nothing stored", err)
		}
	})

	t.Run("reports an unknown project", func(t *testing.T) {
		areas := newFakeAreas()
		area := areas.seed(t, "玩樂", false)

		_, err := project.New(newFakeProjects(), areas).SetProjectDefaultArea(ctx, usecase.SetProjectDefaultAreaInput{ID: uuid.Must(uuid.NewV7()), AreaID: area.ID})
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestResolveProjectDefaults(t *testing.T) {
	ctx := context.Background()
	// active returns a stored-looking project that is active on the 5th.
	active := func(t *testing.T, name string, areaID *uuid.UUID) model.Project {
		t.Helper()
		p, err := model.NewProject(uuid.Must(uuid.NewV7()), name)
		if err != nil {
			t.Fatalf("NewProject: %v", err)
		}
		p.ActivePeriod = &model.Period{From: day(2), To: day(7)}
		p.DefaultAreaID = areaID
		return p
	}

	t.Run("no project is active", func(t *testing.T) {
		out, err := project.New(newFakeProjects(), newFakeAreas()).ResolveProjectDefaults(ctx, usecase.ResolveProjectDefaultsInput{Date: day(5)})
		if err != nil || out.Project != nil || out.DefaultArea != nil {
			t.Fatalf("out = %+v, %v, want nothing", out, err)
		}
	})

	t.Run("one project is active: it and its default area", func(t *testing.T) {
		projects, areas := newFakeProjects(), newFakeAreas()
		area := areas.seed(t, "玩樂", false)
		projects.activeOn = []model.Project{active(t, "日本旅行", &area.ID)}

		out, err := project.New(projects, areas).ResolveProjectDefaults(ctx, usecase.ResolveProjectDefaultsInput{Date: day(5)})
		if err != nil {
			t.Fatalf("ResolveProjectDefaults: %v", err)
		}
		if out.Project == nil || out.Project.Name != "日本旅行" {
			t.Fatalf("Project = %+v, want 日本旅行", out.Project)
		}
		if out.DefaultArea == nil || out.DefaultArea.ID != area.ID || out.DefaultArea.Name != "玩樂" {
			t.Fatalf("DefaultArea = %+v, want 玩樂", out.DefaultArea)
		}
	})

	t.Run("the project has no default area", func(t *testing.T) {
		projects := newFakeProjects()
		projects.activeOn = []model.Project{active(t, "日本旅行", nil)}

		out, err := project.New(projects, newFakeAreas()).ResolveProjectDefaults(ctx, usecase.ResolveProjectDefaultsInput{Date: day(5)})
		if err != nil || out.Project == nil || out.DefaultArea != nil {
			t.Fatalf("out = %+v, %v, want the project and no area", out, err)
		}
	})

	t.Run("the default area was archived since: left out", func(t *testing.T) {
		projects, areas := newFakeProjects(), newFakeAreas()
		area := areas.seed(t, "舊的", true)
		projects.activeOn = []model.Project{active(t, "日本旅行", &area.ID)}

		out, err := project.New(projects, areas).ResolveProjectDefaults(ctx, usecase.ResolveProjectDefaultsInput{Date: day(5)})
		if err != nil || out.Project == nil || out.DefaultArea != nil {
			t.Fatalf("out = %+v, %v, want the project and no area", out, err)
		}
	})

	t.Run("more than one project is active: an error that lists them", func(t *testing.T) {
		projects := newFakeProjects()
		projects.activeOn = []model.Project{active(t, "日本旅行", nil), active(t, "尾牙", nil)}

		out, err := project.New(projects, newFakeAreas()).ResolveProjectDefaults(ctx, usecase.ResolveProjectDefaultsInput{Date: day(5)})
		var ambiguous *usecase.AmbiguousProjectsError
		if !errors.As(err, &ambiguous) {
			t.Fatalf("err = %v, want an AmbiguousProjectsError", err)
		}
		if len(ambiguous.Projects) != 2 || ambiguous.Projects[0].Name != "日本旅行" || ambiguous.Projects[1].Name != "尾牙" || !ambiguous.Date.Equal(day(5)) {
			t.Fatalf("error = %+v, want both projects and the day", ambiguous)
		}
		if out.Project != nil {
			t.Fatalf("Project = %+v, want none when the answer is ambiguous", out.Project)
		}
		for _, name := range []string{"日本旅行", "尾牙", "2026-12-05"} {
			if !strings.Contains(err.Error(), name) {
				t.Errorf("err = %q, want it to mention %s", err, name)
			}
		}
	})

	t.Run("the day asked for is the calendar day of the date, in its own location", func(t *testing.T) {
		projects := newFakeProjects()
		taipei := time.FixedZone("UTC+8", 8*60*60)

		// 00:30 on the 5th in Taipei is still the 4th in UTC.
		_, err := project.New(projects, newFakeAreas()).ResolveProjectDefaults(ctx, usecase.ResolveProjectDefaultsInput{Date: time.Date(2026, time.December, 5, 0, 30, 0, 0, taipei)})
		if err != nil {
			t.Fatalf("ResolveProjectDefaults: %v", err)
		}
		if !projects.gotDay.Equal(day(5)) {
			t.Fatalf("asked the repo for %v, want %v", projects.gotDay, day(5))
		}
	})
}

func TestProjectUseCasesReportStoreFailures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("connection reset")
	projects, areas := newFakeProjects(), newFakeAreas()
	p := projects.seed(t, "日本旅行")
	area := areas.seed(t, "玩樂", false)
	projects.err = boom
	svc := project.New(projects, areas)

	calls := map[string]func() error{
		"CreateProject": func() error { _, err := svc.CreateProject(ctx, usecase.CreateProjectInput{Name: "x"}); return err },
		"RenameProject": func() error {
			_, err := svc.RenameProject(ctx, usecase.RenameProjectInput{ID: p.ID, Name: "x"})
			return err
		},
		"ArchiveProject":   func() error { _, err := svc.ArchiveProject(ctx, p.ID); return err },
		"ListProjects":     func() error { _, err := svc.ListProjects(ctx, usecase.ListProjectsInput{}); return err },
		"GetProject":       func() error { _, err := svc.GetProject(ctx, p.ID); return err },
		"SetProjectPeriod": func() error { _, err := svc.SetProjectPeriod(ctx, usecase.SetProjectPeriodInput{ID: p.ID}); return err },
		"SetProjectActivePeriod": func() error {
			_, err := svc.SetProjectActivePeriod(ctx, usecase.SetProjectActivePeriodInput{ID: p.ID})
			return err
		},
		"SetProjectDefaultArea": func() error {
			_, err := svc.SetProjectDefaultArea(ctx, usecase.SetProjectDefaultAreaInput{ID: p.ID, AreaID: area.ID})
			return err
		},
		"ResolveProjectDefaults": func() error {
			_, err := svc.ResolveProjectDefaults(ctx, usecase.ResolveProjectDefaultsInput{Date: day(5)})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, boom) {
			t.Errorf("%s: err = %v, want the store failure", name, err)
		}
	}
}
