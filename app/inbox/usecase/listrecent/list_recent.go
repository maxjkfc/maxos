// Package listrecent implements the ListRecent use case: it lists the most
// recently captured inbox items.
package listrecent

import (
	"context"
	"fmt"

	"github.com/maxjkfc/maxos/app/inbox/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/inbox/usecase/port/out/repo"
)

const (
	defaultLimit = 10
	maxLimit     = 50
)

// Service implements usecase.ListRecent.
type Service struct {
	repo repo.InboxRepo
}

var _ usecase.ListRecent = (*Service)(nil)

// ListRecent returns the newest items first. An unset limit means the default;
// any other limit is brought into the range 1 to the maximum.
func (s *Service) ListRecent(ctx context.Context, in usecase.ListRecentInput) (usecase.ListRecentOutput, error) {
	limit := in.Limit
	if limit == 0 {
		limit = defaultLimit
	}
	limit = min(max(limit, 1), maxLimit)

	items, err := s.repo.ListRecent(ctx, limit)
	if err != nil {
		return usecase.ListRecentOutput{}, fmt.Errorf("list recent inbox entries: %w", err)
	}
	out := usecase.ListRecentOutput{Items: make([]usecase.RecentItem, 0, len(items))}
	for _, item := range items {
		out.Items = append(out.Items, usecase.RecentItem{
			ID:         item.ID,
			ReceivedAt: item.ReceivedAt,
			Source:     item.Source,
			Actor:      item.Actor,
			Kind:       string(item.Kind),
			Status:     string(item.Status),
			Text:       item.Text(),
		})
	}
	return out, nil
}
