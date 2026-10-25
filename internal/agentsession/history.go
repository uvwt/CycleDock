package agentsession

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/session"
)

const (
	maxHistoryQueryChars = 240
	maxHistoryHitChars   = 6000
)

type HistoryHit struct {
	EventID      string
	InvocationID string
	OccurredAt   time.Time
	Author       string
	Content      string
}

func (s *PostgresService) SearchHistory(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
	query string,
	limit int,
) ([]HistoryHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("history query is required")
	}
	if len([]rune(query)) > maxHistoryQueryChars {
		return nil, fmt.Errorf("history query exceeds %d characters", maxHistoryQueryChars)
	}
	if limit <= 0 {
		limit = 8
	}
	if limit > 12 {
		limit = 12
	}

	appName, userID, sessionID := Identity(workspaceID, cardID)
	rows, err := s.pool.Query(ctx, `
		SELECT id, invocation_id, occurred_at, event
		FROM agent_session_events
		WHERE app_name=$1 AND user_id=$2 AND session_id=$3
		  AND strpos(lower(event::text), lower($4)) > 0
		ORDER BY occurred_at DESC, sequence DESC
		LIMIT $5
	`, appName, userID, sessionID, query, limit*4)
	if err != nil {
		return nil, fmt.Errorf("search session history: %w", err)
	}
	defer rows.Close()

	hits := make([]HistoryHit, 0, limit)
	for rows.Next() {
		var eventID, invocationID string
		var occurredAt time.Time
		var raw []byte
		if err := rows.Scan(&eventID, &invocationID, &occurredAt, &raw); err != nil {
			return nil, fmt.Errorf("scan session history: %w", err)
		}
		var event session.Event
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, fmt.Errorf("decode session history event %q: %w", eventID, err)
		}
		// Compaction summaries are prompt machinery, not the exact historical
		// evidence this tool is for. Raw events remain in PostgreSQL and are
		// preferred even after ADK stops including them in the normal prompt.
		if event.Actions.Compaction != nil {
			continue
		}
		rendered := renderHistoryEvent(&event)
		if rendered == "" {
			continue
		}
		hits = append(hits, HistoryHit{
			EventID: eventID, InvocationID: invocationID, OccurredAt: occurredAt,
			Author: event.Author, Content: rendered,
		})
		if len(hits) == limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search session history: %w", err)
	}
	return hits, nil
}

func renderHistoryEvent(event *session.Event) string {
	if event == nil || event.Content == nil {
		return ""
	}
	var lines []string
	for _, part := range event.Content.Parts {
		if part == nil || part.Thought {
			continue
		}
		if text := strings.TrimSpace(part.Text); text != "" {
			lines = append(lines, text)
		}
		if part.FunctionCall != nil {
			args, _ := json.Marshal(part.FunctionCall.Args)
			lines = append(lines, fmt.Sprintf(
				"tool call %s(%s)", part.FunctionCall.Name, args,
			))
		}
		if part.FunctionResponse != nil {
			response, _ := json.Marshal(part.FunctionResponse.Response)
			lines = append(lines, fmt.Sprintf(
				"tool response %s: %s", part.FunctionResponse.Name, response,
			))
		}
	}
	rendered := strings.Join(lines, "\n")
	runes := []rune(rendered)
	if len(runes) > maxHistoryHitChars {
		rendered = string(runes[:maxHistoryHitChars]) + "…"
	}
	return rendered
}
