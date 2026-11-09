package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store/db"
)

type modelCallResponse struct {
	ID                    string `json:"id"`
	CallIndex             int32  `json:"call_index"`
	Purpose               string `json:"purpose"`
	ModelRouteID          string `json:"model_route_id,omitempty"`
	ModelID               string `json:"model_id"`
	Provider              string `json:"provider"`
	StartedAt             string `json:"started_at"`
	FinishedAt            string `json:"finished_at"`
	DurationMS            int32  `json:"duration_ms"`
	Status                string `json:"status"`
	InputTokens           int32  `json:"input_tokens"`
	OutputTokens          int32  `json:"output_tokens"`
	CachedInputTokens     int32  `json:"cached_input_tokens"`
	ReasoningOutputTokens int32  `json:"reasoning_output_tokens"`
	UsageSource           string `json:"usage_source"`
	ErrorCode             string `json:"error_code,omitempty"`
	ErrorSummary          string `json:"error_summary,omitempty"`
}

type traceOverviewResponse struct {
	AttemptID     string              `json:"attempt_id"`
	TurnID        string              `json:"turn_id"`
	Status        string              `json:"status"`
	StartedAt     *time.Time          `json:"started_at,omitempty"`
	FinishedAt    *time.Time          `json:"finished_at,omitempty"`
	ErrorSummary  string              `json:"error_summary,omitempty"`
	ModelCalls    []modelCallResponse `json:"model_calls"`
	ModelCallCount int                `json:"model_call_count"`
	CompactionCount int               `json:"compaction_count"`
	TotalInputTokens int              `json:"total_input_tokens"`
	TotalOutputTokens int             `json:"total_output_tokens"`
}

func (api *API) getAttemptTrace(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	attemptID, ok := parseUUIDParam(w, r, "attemptID")
	if !ok {
		return
	}

	attempt, err := api.store.GetAttemptWithCard(r.Context(), identity.WorkspaceID, cardID, attemptID)
	if err != nil {
		api.handleError(w, err)
		return
	}

	calls, err := api.store.ListAttemptModelCalls(r.Context(), identity.WorkspaceID, cardID, attemptID)
	if err != nil {
		api.handleError(w, err)
		return
	}

	response := traceOverviewResponse{
		AttemptID:    attempt.ID.String(),
		TurnID:       attempt.TurnID.String(),
		Status:       string(attempt.Status),
		StartedAt:    attempt.StartedAt,
		FinishedAt:   attempt.FinishedAt,
		ErrorSummary: attempt.ErrorSummary,
		ModelCalls:   make([]modelCallResponse, 0, len(calls)),
	}
	for _, call := range calls {
		response.ModelCalls = append(response.ModelCalls, modelCallToResponse(call))
		response.ModelCallCount++
		if call.Purpose == "compaction" {
			response.CompactionCount++
		}
		response.TotalInputTokens += int(call.InputTokens)
		response.TotalOutputTokens += int(call.OutputTokens)
	}
	writeJSON(w, http.StatusOK, response)
}

func modelCallToResponse(call db.AgentModelCall) modelCallResponse {
	out := modelCallResponse{
		ID:                    call.ID.String(),
		CallIndex:             call.CallIndex,
		Purpose:               call.Purpose,
		ModelID:               call.ModelID,
		Provider:              call.Provider,
		DurationMS:            call.DurationMs,
		Status:                call.Status,
		InputTokens:           call.InputTokens,
		OutputTokens:          call.OutputTokens,
		CachedInputTokens:     call.CachedInputTokens,
		ReasoningOutputTokens: call.ReasoningOutputTokens,
		UsageSource:           call.UsageSource,
		ErrorCode:             call.ErrorCode,
		ErrorSummary:          call.ErrorSummary,
	}
	if call.ModelRouteID.Valid {
		out.ModelRouteID = call.ModelRouteID.UUID.String()
	}
	if call.StartedAt.Valid {
		out.StartedAt = call.StartedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	if call.FinishedAt.Valid {
		out.FinishedAt = call.FinishedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func (api *API) getAttemptTraceEvents(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	if !requireWorkspaceOwner(w, identity.Role) {
		return
	}
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	attemptID, ok := parseUUIDParam(w, r, "attemptID")
	if !ok {
		return
	}

	// 校验归属
	attempt, err := api.store.GetAttemptWithCard(r.Context(), identity.WorkspaceID, cardID, attemptID)
	if err != nil {
		api.handleError(w, err)
		return
	}

	appName, userID, sessionID := agentsession.Identity(identity.WorkspaceID, cardID)
	rows, err := api.pool.Query(r.Context(), `
		SELECT sequence, id, invocation_id, occurred_at, event
		FROM agent_session_events
		WHERE app_name = $1 AND user_id = $2 AND session_id = $3
		ORDER BY sequence ASC
		LIMIT 1000
	`, appName, userID, sessionID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	defer rows.Close()

	type traceEvent struct {
		Sequence     int64           `json:"sequence"`
		ID           string          `json:"id"`
		InvocationID string          `json:"invocation_id"`
		OccurredAt   time.Time       `json:"occurred_at"`
		Event        json.RawMessage `json:"event"`
	}
	events := make([]traceEvent, 0)
	for rows.Next() {
		var item traceEvent
		if err := rows.Scan(&item.Sequence, &item.ID, &item.InvocationID, &item.OccurredAt, &item.Event); err != nil {
			api.handleError(w, err)
			return
		}
		// 如果 Attempt 绑定了 invocation_id，则只返回该 invocation 的事件或 compaction 事件
		if attempt.InvocationID != "" {
			if item.InvocationID != attempt.InvocationID {
				// 检查是否是 compaction 事件（通常 compaction 拥有独立 invocation_id）
				// 如果 event 中包含 compaction 动作，则保留
				var check struct {
					Actions struct {
						Compaction json.RawMessage `json:"compaction"`
					} `json:"actions"`
				}
				if err := json.Unmarshal(item.Event, &check); err == nil && check.Actions.Compaction != nil {
					events = append(events, item)
				}
				continue
			}
		}
		events = append(events, item)
	}
	if err := rows.Err(); err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events": events,
	})
}

// 避免编译未使用
var _ = domain.ErrNotFound
