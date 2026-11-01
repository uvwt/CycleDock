package store

import (
	"context"

	"github.com/google/uuid"
)

func (s *Store) ListWorkspaceIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, "SELECT id FROM workspaces ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
