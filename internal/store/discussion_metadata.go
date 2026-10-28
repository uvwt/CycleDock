package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
)

type DiscussionMetadataInput struct {
	CardID  uuid.UUID
	Content string
}

// ListDiscussionsNeedingMetadata 从已落库的首条输入恢复任务，进程重启也不会丢失生成请求。
func (s *Store) ListDiscussionsNeedingMetadata(ctx context.Context, workspaceID uuid.UUID) ([]DiscussionMetadataInput, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, first_turn.content
		FROM cards c
		JOIN LATERAL (
			SELECT content FROM card_turns
			WHERE workspace_id=c.workspace_id AND card_id=c.id
			ORDER BY sequence LIMIT 1
		) first_turn ON true
		WHERE c.workspace_id=$1 AND c.kind='discussion' AND c.discussion_state='active'
		  AND btrim(c.title)='' AND btrim(c.description)=''
		ORDER BY c.created_at, c.id
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var inputs []DiscussionMetadataInput
	for rows.Next() {
		var input DiscussionMetadataInput
		if err := rows.Scan(&input.CardID, &input.Content); err != nil {
			return nil, err
		}
		inputs = append(inputs, input)
	}
	return inputs, rows.Err()
}

func (s *Store) SaveGeneratedDiscussionMetadata(ctx context.Context, workspaceID, cardID uuid.UUID, title, description string) (bool, error) {
	title, description = strings.TrimSpace(title), strings.TrimSpace(description)
	if title == "" || description == "" {
		return false, fmt.Errorf("%w: generated discussion title and description are required", domain.ErrConflict)
	}
	// 生成期间用户可能已经编辑或归档卡片；条件更新保护人工内容，也让多实例重复生成只保存一次。
	command, err := s.pool.Exec(ctx, `
		UPDATE cards SET title=$3, description=$4, updated_at=now()
		WHERE workspace_id=$1 AND id=$2 AND kind='discussion' AND discussion_state='active'
		  AND btrim(title)='' AND btrim(description)=''
	`, workspaceID, cardID, title, description)
	return command.RowsAffected() == 1, err
}
