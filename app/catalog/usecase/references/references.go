// Package references implements the check other contexts make before they store
// the id of a catalog entry: that the project, area, party or tag exists and is
// not archived.
package references

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
)

// Service implements usecase.VerifyReferences.
type Service struct {
	projects repo.ProjectRepo
	areas    repo.AreaRepo
	parties  repo.PartyRepo
	tags     repo.TagRepo
}

var _ usecase.VerifyReferences = (*Service)(nil)

// lookup reports, for the ids that exist, whether the entry is archived.
type lookup func(ctx context.Context, ids []uuid.UUID) (archivedByID map[uuid.UUID]bool, err error)

// VerifyReferences asks each repo once, for the ids of its kind, and skips the
// kinds that have none. Every id that is no good is reported, not only the first.
func (s *Service) VerifyReferences(ctx context.Context, in usecase.VerifyReferencesInput) error {
	checks := []struct {
		kind   usecase.ReferenceKind
		ids    []uuid.UUID
		lookup lookup
	}{
		{usecase.ReferenceProject, in.ProjectIDs, s.lookupProjects},
		{usecase.ReferenceArea, in.AreaIDs, s.lookupAreas},
		{usecase.ReferenceParty, in.PartyIDs, s.lookupParties},
		{usecase.ReferenceTag, in.TagIDs, s.lookupTags},
	}

	var problems []usecase.ReferenceProblem
	for _, check := range checks {
		ids := unique(check.ids)
		if len(ids) == 0 {
			continue
		}
		archivedByID, err := check.lookup(ctx, ids)
		if err != nil {
			return fmt.Errorf("look up %s ids: %w", check.kind, err)
		}
		for _, id := range ids {
			archived, exists := archivedByID[id]
			if exists && !archived {
				continue
			}
			// An entry that is not there at all is "not found"; one that is there
			// and archived is "archived".
			problems = append(problems, usecase.ReferenceProblem{Kind: check.kind, ID: id, Archived: exists})
		}
	}
	if len(problems) > 0 {
		return &usecase.ReferenceError{Problems: problems}
	}
	return nil
}

func (s *Service) lookupProjects(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	projects, err := s.projects.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	archivedByID := make(map[uuid.UUID]bool, len(projects))
	for _, p := range projects {
		archivedByID[p.ID] = p.IsArchived()
	}
	return archivedByID, nil
}

func (s *Service) lookupAreas(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	areas, err := s.areas.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	archivedByID := make(map[uuid.UUID]bool, len(areas))
	for _, a := range areas {
		archivedByID[a.ID] = a.IsArchived()
	}
	return archivedByID, nil
}

func (s *Service) lookupParties(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	parties, err := s.parties.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	archivedByID := make(map[uuid.UUID]bool, len(parties))
	for _, p := range parties {
		archivedByID[p.ID] = p.IsArchived()
	}
	return archivedByID, nil
}

func (s *Service) lookupTags(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	tags, err := s.tags.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	archivedByID := make(map[uuid.UUID]bool, len(tags))
	for _, t := range tags {
		archivedByID[t.ID] = t.IsArchived()
	}
	return archivedByID, nil
}

// unique returns ids without the repeats, in the order of their first
// appearance.
func unique(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, repeat := seen[id]; repeat {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
