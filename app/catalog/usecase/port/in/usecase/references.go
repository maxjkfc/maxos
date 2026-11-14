package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// VerifyReferences checks, in one call, that the catalog entries another context
// is about to store the ids of exist and are not archived. Tables outside the
// catalog keep these ids as plain uuid columns, with no foreign key, so this
// check is what keeps them valid.
//
// It returns a *ReferenceError listing every id that is no good.
type VerifyReferences interface {
	VerifyReferences(ctx context.Context, in VerifyReferencesInput) error
}

// VerifyReferencesInput holds the ids to check; each list may be empty, and an id
// may appear more than once.
type VerifyReferencesInput struct {
	ProjectIDs []uuid.UUID
	AreaIDs    []uuid.UUID
	PartyIDs   []uuid.UUID
	TagIDs     []uuid.UUID
}

// ReferenceKind says which kind of catalog entry an id was meant to name.
type ReferenceKind string

// The kinds of catalog entry another context may refer to.
const (
	ReferenceProject ReferenceKind = "project"
	ReferenceArea    ReferenceKind = "area"
	ReferenceParty   ReferenceKind = "party"
	ReferenceTag     ReferenceKind = "tag"
)

// ReferenceProblem is one id that cannot be used.
type ReferenceProblem struct {
	Kind ReferenceKind
	ID   uuid.UUID
	// Archived is true when the entry exists but is archived; false when there
	// is no entry of that kind with the id.
	Archived bool
}

// ReferenceError is what VerifyReferences returns when ids cannot be used. It
// lists all of them, so that the caller can report everything wrong at once.
type ReferenceError struct {
	// Problems are in the order the ids were given: projects, areas, parties,
	// then tags.
	Problems []ReferenceProblem
}

func (e *ReferenceError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		reason := "not found"
		if p.Archived {
			reason = "archived"
		}
		parts[i] = fmt.Sprintf("%s %s: %s", p.Kind, p.ID, reason)
	}
	return "cannot use catalog entries: " + strings.Join(parts, "; ")
}

// AmbiguousProjectsError is what ResolveProjectDefaults returns when more than
// one project is active on the day.
type AmbiguousProjectsError struct {
	// Date is the day, at midnight UTC.
	Date time.Time
	// Projects are the projects active on Date, by the start of their active
	// period.
	Projects []Project
}

func (e *AmbiguousProjectsError) Error() string {
	parts := make([]string, len(e.Projects))
	for i, p := range e.Projects {
		parts[i] = fmt.Sprintf("%s (%s)", p.Name, p.ID)
	}
	return fmt.Sprintf("%d projects are active on %s: %s", len(e.Projects), e.Date.Format(time.DateOnly), strings.Join(parts, ", "))
}
