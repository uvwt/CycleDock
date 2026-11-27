package agentsession

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/v2/session"

	"github.com/uvwt/CycleDock/internal/sqldb"
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
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("session service is not initialized")
	}
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
	var (
		rows *sqldb.Rows
		err  error
	)
	if s.db.IsPostgres() {
		rows, err = s.db.Query(ctx, `
			SELECT id, invocation_id, occurred_at, event
			FROM agent_session_events
			WHERE app_name=$1 AND user_id=$2 AND session_id=$3
			  AND strpos(lower(event::text), lower($4)) > 0
			ORDER BY occurred_at DESC, sequence DESC
			LIMIT $5
		`, appName, userID, sessionID, query, limit*4)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT id, invocation_id, occurred_at, event
			FROM agent_session_events
			WHERE app_name=$1 AND user_id=$2 AND session_id=$3
			  AND instr(lower(event), lower($4)) > 0
			ORDER BY occurred_at DESC, sequence DESC
			LIMIT $5
		`, appName, userID, sessionID, query, limit*4)
	}
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

// SessionRound represents one inspected round or event in full mode.
type SessionRound struct {
	EventID      string           `json:"event_id"`
	InvocationID string           `json:"invocation_id,omitempty"`
	OccurredAt   time.Time        `json:"occurred_at"`
	Author       string           `json:"author"`
	Role         string           `json:"role,omitempty"`
	Text         string           `json:"text,omitempty"`
	Thinking     string           `json:"thinking,omitempty"`
	ToolCalls    []map[string]any `json:"tool_calls,omitempty"`
	ToolResults  []map[string]any `json:"tool_results,omitempty"`
}

// InspectFullSession loads all events for the card session and converts them into
// complete rounds including thinking, tool calls, and outputs.
func (s *Service) InspectFullSession(
	ctx context.Context,
	workspaceID, cardID uuid.UUID,
	limit int,
) ([]SessionRound, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("session service is not initialized")
	}
	appName, userID, sessionID := Identity(workspaceID, cardID)
	if limit <= 0 {
		limit = 50
	} else if limit > 200 {
		limit = 200
	}

	rows, err := s.db.Query(ctx, `
		SELECT id, invocation_id, occurred_at, event
		FROM agent_session_events
		WHERE app_name=$1 AND user_id=$2 AND session_id=$3
		ORDER BY occurred_at DESC, sequence DESC
		LIMIT $4
	`, appName, userID, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("inspect full session events: %w", err)
	}
	defer rows.Close()

	reverse := make([]SessionRound, 0)
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
		if event.Actions.Compaction != nil {
			continue
		}

		round := SessionRound{
			EventID:      eventID,
			InvocationID: invocationID,
			OccurredAt:   occurredAt.UTC(),
			Author:       event.Author,
		}
		if event.Content != nil {
			round.Role = event.Content.Role
			var texts []string
			var thoughts []string
			for _, part := range event.Content.Parts {
				if part == nil {
					continue
				}
				if part.Thought && strings.TrimSpace(part.Text) != "" {
					thoughts = append(thoughts, part.Text)
				} else if strings.TrimSpace(part.Text) != "" {
					texts = append(texts, part.Text)
				}
				if part.FunctionCall != nil {
					round.ToolCalls = append(round.ToolCalls, map[string]any{
						"id":   part.FunctionCall.ID,
						"name": part.FunctionCall.Name,
						"args": part.FunctionCall.Args,
					})
				}
				if part.FunctionResponse != nil {
					resp := part.FunctionResponse.Response
					if _, hasInline := resp["inline_images"]; hasInline {
						sanitized := make(map[string]any, len(resp))
						for k, v := range resp {
							if k != "inline_images" {
								sanitized[k] = v
							}
						}
						sanitized["inline_images"] = "[image payload omitted in history]"
						resp = sanitized
					}
					round.ToolResults = append(round.ToolResults, map[string]any{
						"id":       part.FunctionResponse.ID,
						"name":     part.FunctionResponse.Name,
						"response": resp,
					})
				}
				if part.InlineData != nil {
					texts = append(texts, fmt.Sprintf("[inline media: mimeType=%s, bytes=%d]", part.InlineData.MIMEType, len(part.InlineData.Data)))
				}
				if part.FileData != nil {
					texts = append(texts, fmt.Sprintf("[file media: mimeType=%s]", part.FileData.MIMEType))
				}
			}
			if len(texts) > 0 {
				round.Text = strings.Join(texts, "\n")
			}
			if len(thoughts) > 0 {
				round.Thinking = strings.Join(thoughts, "\n")
			}
		}
		reverse = append(reverse, round)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inspect session events: %w", err)
	}
	slices.Reverse(reverse)
	return reverse, nil
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
			resp := part.FunctionResponse.Response
			if _, hasInline := resp["inline_images"]; hasInline {
				sanitized := make(map[string]any, len(resp))
				for k, v := range resp {
					if k != "inline_images" {
						sanitized[k] = v
					}
				}
				sanitized["inline_images"] = "[image payload omitted in history]"
				resp = sanitized
			}
			response, _ := json.Marshal(resp)
			lines = append(lines, fmt.Sprintf(
				"tool response %s: %s", part.FunctionResponse.Name, response,
			))
		}
		if part.InlineData != nil {
			lines = append(lines, fmt.Sprintf("[inline media: mimeType=%s, bytes=%d]", part.InlineData.MIMEType, len(part.InlineData.Data)))
		}
		if part.FileData != nil {
			lines = append(lines, fmt.Sprintf("[file media: mimeType=%s]", part.FileData.MIMEType))
		}
	}
	rendered := strings.Join(lines, "\n")
	runes := []rune(rendered)
	if len(runes) > maxHistoryHitChars {
		rendered = string(runes[:maxHistoryHitChars]) + "…"
	}
	return rendered
}
