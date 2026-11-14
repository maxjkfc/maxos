// Package project implements the project use cases: create, rename, archive,
// list and get, setting the periods and the default area, and finding the
// project of a day.
package project

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/catalog/domain/model"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
)

// Service implements usecase.CreateProject, RenameProject, ArchiveProject,
// ListProjects, GetProject, SetProjectPeriod, SetProjectActivePeriod,
// SetProjectDefaultArea and ResolveProjectDefaults.
type Service struct {
	projects repo.ProjectRepo
	areas    repo.AreaRepo
}

var (
	_ usecase.CreateProject          = (*Service)(nil)
	_ usecase.RenameProject          = (*Service)(nil)
	_ usecase.ArchiveProject         = (*Service)(nil)
	_ usecase.ListProjects           = (*Service)(nil)
	_ usecase.GetProject             = (*Service)(nil)
	_ usecase.SetProjectPeriod       = (*Service)(nil)
	_ usecase.SetProjectActivePeriod = (*Service)(nil)
	_ usecase.SetProjectDefaultArea  = (*Service)(nil)
	_ usecase.ResolveProjectDefaults = (*Service)(nil)
)

// CreateProject validates everything the project is given and stores it. The
// default area, when there is one, must exist and not be archived.
func (s *Service) CreateProject(ctx context.Context, in usecase.CreateProjectInput) (usecase.Project, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return usecase.Project{}, fmt.Errorf("generate project id: %w", err)
	}
	project, err := model.NewProject(id, in.Name)
	if err != nil {
		return usecase.Project{}, err
	}
	period, err := toPeriod(in.Period)
	if err != nil {
		return usecase.Project{}, fmt.Errorf("period: %w", err)
	}
	active, err := toPeriod(in.ActivePeriod)
	if err != nil {
		return usecase.Project{}, fmt.Errorf("active period: %w", err)
	}
	if err := project.SetPeriod(period); err != nil {
		return usecase.Project{}, err
	}
	if err := project.SetActivePeriod(active); err != nil {
		return usecase.Project{}, err
	}
	if in.DefaultAreaID != nil {
		if err := s.requireUsableArea(ctx, *in.DefaultAreaID); err != nil {
			return usecase.Project{}, err
		}
		areaID := *in.DefaultAreaID
		project.DefaultAreaID = &areaID
	}

	stored, err := s.projects.Insert(ctx, project)
	if err != nil {
		return usecase.Project{}, fmt.Errorf("create project %q: %w", project.Name, err)
	}
	return toProject(stored), nil
}

// RenameProject validates the new name and stores it.
func (s *Service) RenameProject(ctx context.Context, in usecase.RenameProjectInput) (usecase.Project, error) {
	project, err := s.projects.Get(ctx, in.ID)
	if err != nil {
		return usecase.Project{}, fmt.Errorf("rename project %s: %w", in.ID, err)
	}
	if err := project.Rename(in.Name); err != nil {
		return usecase.Project{}, err
	}
	stored, err := s.projects.Rename(ctx, in.ID, project.Name)
	if err != nil {
		return usecase.Project{}, fmt.Errorf("rename project %s: %w", in.ID, err)
	}
	return toProject(stored), nil
}

// ArchiveProject archives the project. An archived project stays archived:
// asking again returns it as it is.
func (s *Service) ArchiveProject(ctx context.Context, id uuid.UUID) (usecase.Project, error) {
	stored, err := s.projects.Archive(ctx, id)
	if err != nil {
		return usecase.Project{}, fmt.Errorf("archive project %s: %w", id, err)
	}
	return toProject(stored), nil
}

// ListProjects returns the projects by name.
func (s *Service) ListProjects(ctx context.Context, in usecase.ListProjectsInput) (usecase.ListProjectsOutput, error) {
	projects, err := s.projects.List(ctx, in.IncludeArchived)
	if err != nil {
		return usecase.ListProjectsOutput{}, fmt.Errorf("list projects: %w", err)
	}
	out := usecase.ListProjectsOutput{Projects: make([]usecase.Project, 0, len(projects))}
	for _, project := range projects {
		out.Projects = append(out.Projects, toProject(project))
	}
	return out, nil
}

// GetProject returns the project, archived or not.
func (s *Service) GetProject(ctx context.Context, id uuid.UUID) (usecase.Project, error) {
	project, err := s.projects.Get(ctx, id)
	if err != nil {
		return usecase.Project{}, fmt.Errorf("get project %s: %w", id, err)
	}
	return toProject(project), nil
}

// SetProjectPeriod checks the new period against the active period the project
// has and stores it.
func (s *Service) SetProjectPeriod(ctx context.Context, in usecase.SetProjectPeriodInput) (usecase.Project, error) {
	project, err := s.projects.Get(ctx, in.ID)
	if err != nil {
		return usecase.Project{}, fmt.Errorf("set period of project %s: %w", in.ID, err)
	}
	period, err := toPeriod(in.Period)
	if err != nil {
		return usecase.Project{}, fmt.Errorf("period: %w", err)
	}
	if err := project.SetPeriod(period); err != nil {
		return usecase.Project{}, err
	}
	stored, err := s.projects.SetPeriod(ctx, in.ID, project.Period)
	if err != nil {
		return usecase.Project{}, fmt.Errorf("set period of project %s: %w", in.ID, err)
	}
	return toProject(stored), nil
}

// SetProjectActivePeriod checks the new active period against the period the
// project has and stores it.
func (s *Service) SetProjectActivePeriod(ctx context.Context, in usecase.SetProjectActivePeriodInput) (usecase.Project, error) {
	project, err := s.projects.Get(ctx, in.ID)
	if err != nil {
		return usecase.Project{}, fmt.Errorf("set active period of project %s: %w", in.ID, err)
	}
	active, err := toPeriod(in.ActivePeriod)
	if err != nil {
		return usecase.Project{}, fmt.Errorf("active period: %w", err)
	}
	if err := project.SetActivePeriod(active); err != nil {
		return usecase.Project{}, err
	}
	stored, err := s.projects.SetActivePeriod(ctx, in.ID, project.ActivePeriod)
	if err != nil {
		return usecase.Project{}, fmt.Errorf("set active period of project %s: %w", in.ID, err)
	}
	return toProject(stored), nil
}

// SetProjectDefaultArea stores the area as the default of the project once it
// is known to exist and not be archived.
func (s *Service) SetProjectDefaultArea(ctx context.Context, in usecase.SetProjectDefaultAreaInput) (usecase.Project, error) {
	if err := s.requireUsableArea(ctx, in.AreaID); err != nil {
		return usecase.Project{}, err
	}
	stored, err := s.projects.SetDefaultArea(ctx, in.ID, in.AreaID)
	if err != nil {
		return usecase.Project{}, fmt.Errorf("set default area of project %s: %w", in.ID, err)
	}
	return toProject(stored), nil
}

// ResolveProjectDefaults finds the one project in use whose active period
// includes the day. The default area comes along only when it can still be
// used: an area archived after it was made the default is left out, so that a
// caller that takes the answer as it is does not stumble on an archived area it
// never chose.
func (s *Service) ResolveProjectDefaults(ctx context.Context, in usecase.ResolveProjectDefaultsInput) (usecase.ResolveProjectDefaultsOutput, error) {
	day := model.DateOf(in.Date)
	projects, err := s.projects.ListActiveOn(ctx, day)
	if err != nil {
		return usecase.ResolveProjectDefaultsOutput{}, fmt.Errorf("find the projects active on %s: %w", day.Format(time.DateOnly), err)
	}
	if len(projects) == 0 {
		return usecase.ResolveProjectDefaultsOutput{}, nil
	}
	if len(projects) > 1 {
		ambiguous := &usecase.AmbiguousProjectsError{Date: day, Projects: make([]usecase.Project, len(projects))}
		for i, project := range projects {
			ambiguous.Projects[i] = toProject(project)
		}
		return usecase.ResolveProjectDefaultsOutput{}, ambiguous
	}

	project := toProject(projects[0])
	out := usecase.ResolveProjectDefaultsOutput{Project: &project}
	if project.DefaultAreaID == nil {
		return out, nil
	}
	area, err := s.areas.Get(ctx, *project.DefaultAreaID)
	switch {
	case err != nil && !errors.Is(err, model.ErrNotFound):
		return usecase.ResolveProjectDefaultsOutput{}, fmt.Errorf("get default area %s of project %s: %w", *project.DefaultAreaID, project.ID, err)
	case err == nil && !area.IsArchived():
		a := toArea(area)
		out.DefaultArea = &a
	}
	return out, nil
}

// requireUsableArea returns an error unless the area exists and is not
// archived.
func (s *Service) requireUsableArea(ctx context.Context, id uuid.UUID) error {
	area, err := s.areas.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("default area %s: %w", id, err)
	}
	if area.IsArchived() {
		return fmt.Errorf("default area %s: %w", id, model.ErrArchived)
	}
	return nil
}

// toPeriod validates a period from outside; nil stays nil.
func toPeriod(p *usecase.Period) (*model.Period, error) {
	if p == nil {
		return nil, nil
	}
	period, err := model.NewPeriod(p.From, p.To)
	if err != nil {
		return nil, err
	}
	return &period, nil
}

func toProject(p model.Project) usecase.Project {
	return usecase.Project{
		ID:            p.ID,
		Name:          p.Name,
		Period:        fromPeriod(p.Period),
		ActivePeriod:  fromPeriod(p.ActivePeriod),
		DefaultAreaID: p.DefaultAreaID,
		CreatedAt:     p.CreatedAt,
		ArchivedAt:    p.ArchivedAt,
	}
}

func fromPeriod(p *model.Period) *usecase.Period {
	if p == nil {
		return nil
	}
	return &usecase.Period{From: p.From, To: p.To}
}

func toArea(a model.Area) usecase.Area {
	return usecase.Area{
		ID:         a.ID,
		Name:       a.Name,
		CreatedAt:  a.CreatedAt,
		ArchivedAt: a.ArchivedAt,
	}
}
