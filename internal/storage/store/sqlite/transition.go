package sqlite

import (
	"context"
	"fmt"

	"github.com/novitalabs/NovitaBox/internal/storage/store"
)

func (s *Store) ListTransitions(ctx context.Context, resourceType string, resourceID string) ([]store.TransitionRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, resource_type, resource_id, from_state, to_state, action, created_at
FROM state_transitions
WHERE resource_type = ? AND resource_id = ?
ORDER BY id ASC`, resourceType, resourceID)
	if err != nil {
		return nil, fmt.Errorf("list transitions for %s %q: %w", resourceType, resourceID, err)
	}
	defer rows.Close()

	var records []store.TransitionRecord
	for rows.Next() {
		var record store.TransitionRecord
		var createdAt int64
		if err := rows.Scan(
			&record.ID,
			&record.ResourceType,
			&record.ResourceID,
			&record.FromState,
			&record.ToState,
			&record.Action,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan transition: %w", err)
		}
		record.CreatedAt = unixTime(createdAt)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transitions: %w", err)
	}

	return records, nil
}
