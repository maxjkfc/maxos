// Package capture implements the Capture use case: it stores a raw note once
// per idempotency key.
package capture

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/inbox/domain/model"
	"github.com/maxjkfc/maxos/app/inbox/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/inbox/usecase/port/out/repo"
)

// Service implements usecase.Capture.
type Service struct {
	repo repo.InboxRepo
}

var _ usecase.Capture = (*Service)(nil)

// Capture validates the note and stores it. When the idempotency key was used
// before, it writes nothing and returns the note stored under that key, marked
// as a duplicate.
func (s *Service) Capture(ctx context.Context, in usecase.CaptureInput) (usecase.CaptureOutput, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return usecase.CaptureOutput{}, fmt.Errorf("generate inbox id: %w", err)
	}
	note, err := model.NewNote(model.NewNoteParams{
		ID:             id,
		Source:         in.Source,
		Actor:          in.Actor,
		IdempotencyKey: in.IdempotencyKey,
		Text:           in.Text,
	})
	if err != nil {
		return usecase.CaptureOutput{}, err
	}

	stored, err := s.repo.Insert(ctx, note)
	if err == nil {
		return output(stored, false), nil
	}
	if !errors.Is(err, repo.ErrDuplicateKey) {
		return usecase.CaptureOutput{}, fmt.Errorf("insert inbox entry: %w", err)
	}

	// The key was committed by an earlier call (a concurrent insert blocks until
	// that call commits), so this read sees it.
	existing, err := s.repo.GetByKey(ctx, note.IdempotencyKey)
	if err != nil {
		return usecase.CaptureOutput{}, fmt.Errorf("get inbox entry by key: %w", err)
	}
	return output(existing, true), nil
}

func output(item model.InboxItem, duplicate bool) usecase.CaptureOutput {
	return usecase.CaptureOutput{
		ID:         item.ID,
		ReceivedAt: item.ReceivedAt,
		Text:       item.Text(),
		Duplicate:  duplicate,
	}
}
