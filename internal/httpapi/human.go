package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func (api *API) mountHumanRoutes(r chi.Router) {
	r.Get("/workspaces", api.listWorkspaces)
	r.Post("/workspaces/{workspaceID}/switch", api.switchWorkspace)

	r.Get("/projects", api.listProjects)
	r.Post("/projects", api.createProject)
	r.Get("/projects/{projectID}", api.getProject)
	r.Patch("/projects/{projectID}", api.updateProject)
	r.Get("/projects/{projectID}/cards", api.listProjectCards)
	r.Get("/projects/{projectID}/schedules", api.listProjectSchedules)
	r.Get("/projects/{projectID}/activity", api.listProjectActivity)
	r.Post("/projects/{projectID}/discussions", api.createDiscussion)
	r.Post("/projects/{projectID}/todos", api.createTodo)

	r.Get("/cards/{cardID}", api.getCard)
	r.Get("/cards/{cardID}/stream", api.streamCard)
	r.Patch("/cards/{cardID}", api.updateCard)
	r.Post("/cards/{cardID}/move", api.moveCard)
	r.Post("/cards/{cardID}/discussion-state", api.setDiscussionState)
	r.Delete("/cards/{cardID}", api.deleteDiscussion)
	r.Post("/cards/{cardID}/ongoing-state", api.setOngoingState)
	r.Post("/cards/{cardID}/comments", api.submitComment)
	r.Post("/cards/{cardID}/reply", api.replyAndContinue)
	r.Post("/cards/{cardID}/turns/{turnID}/cancel", api.cancelPendingTurn)
	r.Post("/cards/{cardID}/attempts/{attemptID}/retry", api.retryAttempt)
	r.Post("/cards/{cardID}/attempts/{attemptID}/cancel", api.cancelAttempt)

	r.Get("/cards/{cardID}/schedules", api.listSchedules)
	r.Post("/cards/{cardID}/schedules", api.createSchedule)
	r.Put("/cards/{cardID}/schedules/{scheduleID}", api.updateSchedule)
	r.Delete("/cards/{cardID}/schedules/{scheduleID}", api.deleteSchedule)

	r.Get("/agent/settings", api.getAgentSettings)
	r.Put("/agent/settings", api.saveAgentSettings)
	r.Post("/agent/provider-test", api.testAgentProvider)
	r.Get("/agent/model-auth", api.getModelAuthStatus)
	r.Post("/agent/model-auth/chatgpt/start", api.startChatGPTModelAuth)
	r.Post("/agent/model-auth/chatgpt/complete", api.completeChatGPTHostedTest)
	r.Post("/agent/model-auth/grok/start", api.startGrokModelAuth)
	r.Post("/agent/model-auth/grok/poll", api.pollGrokModelAuth)
	r.Delete("/agent/model-auth/{provider}/{credentialID}", api.disconnectModelAuth)
	r.Get("/agent/models", api.listAgentModels)
	r.Post("/agent/mcp-test", api.testAgentMCP)
	r.Get("/agent/mcp-connections", api.listAgentMCPConnections)
	r.Post("/agent/mcp-connections", api.createAgentMCPConnection)
	r.Put("/agent/mcp-connections/{connectionID}", api.updateAgentMCPConnection)
	r.Delete("/agent/mcp-connections/{connectionID}", api.deleteAgentMCPConnection)
	r.Get("/agent/nexusdock", api.getNexusDockConnection)
	r.Post("/agent/nexusdock/connect", api.connectNexusDock)
	r.Delete("/agent/nexusdock", api.disconnectNexusDock)
}

type projectResponse struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Instructions string    `json:"instructions"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type cardResponse struct {
	ID                   string    `json:"id"`
	ProjectID            string    `json:"project_id"`
	Kind                 string    `json:"kind"`
	Title                string    `json:"title"`
	Description          string    `json:"description"`
	DiscussionState      string    `json:"discussion_state,omitempty"`
	TodoState            string    `json:"todo_state,omitempty"`
	OngoingState         string    `json:"ongoing_state,omitempty"`
	SourceDiscussionID   string    `json:"source_discussion_id,omitempty"`
	Health               string    `json:"health,omitempty"`
	CurrentAttemptStatus string    `json:"current_attempt_status,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type attemptResponse struct {
	ID               string     `json:"id"`
	TurnID           string     `json:"turn_id"`
	SourceEventID    string     `json:"source_event_id,omitempty"`
	AttemptNumber    int        `json:"attempt_number"`
	Reason           string     `json:"reason"`
	RetryOfAttemptID string     `json:"retry_of_attempt_id,omitempty"`
	Status           string     `json:"status"`
	CurrentStage     string     `json:"current_stage,omitempty"`
	ProgressSummary  string     `json:"progress_summary,omitempty"`
	ResultSummary    string     `json:"result_summary,omitempty"`
	ErrorSummary     string     `json:"error_summary,omitempty"`
	CanRetry         bool       `json:"can_retry,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	FinishedAt       *time.Time `json:"finished_at,omitempty"`
}

type eventResponse struct {
	ID            string    `json:"id"`
	Sequence      int64     `json:"sequence"`
	Type          string    `json:"type"`
	AttemptID     string    `json:"attempt_id,omitempty"`
	Content       string    `json:"content"`
	CheckinStatus string    `json:"checkin_status,omitempty"`
	ActorName     string    `json:"actor_name"`
	ActorKind     string    `json:"actor_kind"`
	CreatedAt     time.Time `json:"created_at"`
}

type pendingTurnResponse struct {
	ID            string    `json:"id"`
	Sequence      int64     `json:"sequence"`
	Source        string    `json:"source"`
	DispatchMode  string    `json:"dispatch_mode"`
	SourceEventID string    `json:"source_event_id,omitempty"`
	Content       string    `json:"content"`
	CreatedAt     time.Time `json:"created_at"`
}

type scheduleResponse struct {
	ID              string     `json:"id"`
	Enabled         bool       `json:"enabled"`
	Frequency       string     `json:"frequency"`
	DayOfWeek       *int       `json:"day_of_week,omitempty"`
	DayOfMonth      *int       `json:"day_of_month,omitempty"`
	Hour            int        `json:"hour"`
	Minute          int        `json:"minute"`
	Timezone        string     `json:"timezone"`
	NextTriggerAt   time.Time  `json:"next_trigger_at"`
	LastTriggeredAt *time.Time `json:"last_triggered_at,omitempty"`
}

type projectScheduleResponse struct {
	ID                    string     `json:"id"`
	CardID                string     `json:"card_id"`
	CardTitle             string     `json:"card_title"`
	OngoingState          string     `json:"ongoing_state"`
	Enabled               bool       `json:"enabled"`
	Frequency             string     `json:"frequency"`
	DayOfWeek             *int       `json:"day_of_week,omitempty"`
	DayOfMonth            *int       `json:"day_of_month,omitempty"`
	Hour                  int        `json:"hour"`
	Minute                int        `json:"minute"`
	Timezone              string     `json:"timezone"`
	NextTriggerAt         time.Time  `json:"next_trigger_at"`
	LastTriggeredAt       *time.Time `json:"last_triggered_at,omitempty"`
	LastAttemptStatus     string     `json:"last_attempt_status,omitempty"`
	LastAttemptFinishedAt *time.Time `json:"last_attempt_finished_at,omitempty"`
}

type projectActivityResponse struct {
	ID           string    `json:"id"`
	CardID       string    `json:"card_id"`
	CardTitle    string    `json:"card_title"`
	CardKind     string    `json:"card_kind"`
	ActivityType string    `json:"activity_type"`
	Status       string    `json:"status"`
	Content      string    `json:"content"`
	ActorName    string    `json:"actor_name,omitempty"`
	OccurredAt   time.Time `json:"occurred_at"`
}

type cardDetailResponse struct {
	Card           cardResponse          `json:"card"`
	CurrentAttempt *attemptResponse      `json:"current_attempt"`
	Timeline       []eventResponse       `json:"timeline"`
	PendingTurns   []pendingTurnResponse `json:"pending_turns"`
	AttemptHistory []attemptResponse     `json:"attempt_history"`
	Schedules      []scheduleResponse    `json:"schedules"`
	Health         string                `json:"health"`
	LastCheckin    *eventResponse        `json:"last_checkin"`
}

type commentResultResponse struct {
	EventID string `json:"event_id"`
	TurnID  string `json:"turn_id"`
	Reused  bool   `json:"reused"`
}

type retryResultResponse struct {
	AttemptID string `json:"attempt_id"`
	Reused    bool   `json:"reused"`
}

func (api *API) listProjects(w http.ResponseWriter, r *http.Request) {
	identity := identityFromRequest(r)
	projects, err := api.store.ListProjects(r.Context(), identity.WorkspaceID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	response := make([]projectResponse, 0, len(projects))
	for _, project := range projects {
		response = append(response, projectToResponse(project))
	}
	writeJSON(w, http.StatusOK, response)
}

func (api *API) createProject(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		Instructions string `json:"instructions"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_name", "project name is required")
		return
	}
	identity := identityFromRequest(r)
	project, err := api.store.CreateProject(
		r.Context(), identity.WorkspaceID,
		request.Name, strings.TrimSpace(request.Description), strings.TrimSpace(request.Instructions),
	)
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, projectToResponse(project))
}

func (api *API) getProject(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDParam(w, r, "projectID")
	if !ok {
		return
	}
	identity := identityFromRequest(r)
	project, err := api.store.GetProject(r.Context(), identity.WorkspaceID, projectID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectToResponse(project))
}

func (api *API) updateProject(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDParam(w, r, "projectID")
	if !ok {
		return
	}
	var request struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		Instructions string `json:"instructions"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_name", "project name is required")
		return
	}
	identity := identityFromRequest(r)
	project, err := api.store.UpdateProject(
		r.Context(), identity.WorkspaceID, projectID,
		request.Name, strings.TrimSpace(request.Description), strings.TrimSpace(request.Instructions),
	)
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectToResponse(project))
}

func (api *API) listProjectCards(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDParam(w, r, "projectID")
	if !ok {
		return
	}
	identity := identityFromRequest(r)
	cards, err := api.store.ListProjectCardSummaries(r.Context(), identity.WorkspaceID, projectID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	response := make([]cardResponse, 0, len(cards))
	for _, card := range cards {
		response = append(response, cardSummaryToResponse(card))
	}
	writeJSON(w, http.StatusOK, response)
}

func (api *API) listProjectSchedules(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDParam(w, r, "projectID")
	if !ok {
		return
	}
	identity := identityFromRequest(r)
	rows, err := api.store.ListProjectSchedules(r.Context(), identity.WorkspaceID, projectID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	response := make([]projectScheduleResponse, 0, len(rows))
	for _, row := range rows {
		hour, minute := scheduleHourMinute(row.TimeOfDay.Microseconds)
		item := projectScheduleResponse{
			ID: row.ID.String(), CardID: row.CardID.String(), CardTitle: row.CardTitle,
			Enabled: row.Enabled, Frequency: row.Frequency, Hour: hour, Minute: minute,
			Timezone: row.Timezone, NextTriggerAt: row.NextTriggerAt.Time,
		}
		if row.OngoingState.Valid {
			item.OngoingState = row.OngoingState.String
		}
		if row.DayOfWeek.Valid {
			value := int(row.DayOfWeek.Int16)
			item.DayOfWeek = &value
		}
		if row.DayOfMonth.Valid {
			value := int(row.DayOfMonth.Int16)
			item.DayOfMonth = &value
		}
		if row.LastTriggeredAt.Valid {
			value := row.LastTriggeredAt.Time
			item.LastTriggeredAt = &value
		}
		if row.LastAttemptStatus.Valid {
			item.LastAttemptStatus = row.LastAttemptStatus.String
		}
		if row.LastAttemptFinishedAt.Valid {
			value := row.LastAttemptFinishedAt.Time
			item.LastAttemptFinishedAt = &value
		}
		response = append(response, item)
	}
	writeJSON(w, http.StatusOK, response)
}

func (api *API) listProjectActivity(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDParam(w, r, "projectID")
	if !ok {
		return
	}
	limit := int32(60)
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 100")
			return
		}
		limit = int32(value)
	}
	identity := identityFromRequest(r)
	rows, err := api.store.ListProjectActivity(r.Context(), identity.WorkspaceID, projectID, limit)
	if err != nil {
		api.handleError(w, err)
		return
	}
	response := make([]projectActivityResponse, 0, len(rows))
	for _, row := range rows {
		response = append(response, projectActivityResponse{
			ID: row.ID.String(), CardID: row.CardID.String(), CardTitle: row.CardTitle,
			CardKind: row.CardKind, ActivityType: row.ActivityType, Status: row.Status,
			Content: row.Content, ActorName: row.ActorName, OccurredAt: row.OccurredAt.Time,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

func (api *API) createDiscussion(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDParam(w, r, "projectID")
	if !ok {
		return
	}
	identity := identityFromRequest(r)
	if _, err := api.store.GetProject(r.Context(), identity.WorkspaceID, projectID); err != nil {
		api.handleError(w, err)
		return
	}
	card, err := api.store.CreateDiscussion(
		r.Context(), identity.WorkspaceID, projectID, identity.SourceID,
	)
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, cardToResponse(card))
}

func (api *API) createTodo(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDParam(w, r, "projectID")
	if !ok {
		return
	}
	var request struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Title = strings.TrimSpace(request.Title)
	if request.Title == "" {
		writeError(w, http.StatusBadRequest, "invalid_title", "todo title is required")
		return
	}

	identity := identityFromRequest(r)
	if _, err := api.store.GetProject(r.Context(), identity.WorkspaceID, projectID); err != nil {
		api.handleError(w, err)
		return
	}
	card, err := api.store.CreateTodoCard(
		r.Context(),
		identity.WorkspaceID,
		projectID,
		identity.SourceID,
		request.Title,
		strings.TrimSpace(request.Description),
		uuid.NullUUID{},
		1,
	)
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, cardToResponse(card))
}

func (api *API) getCard(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	identity := identityFromRequest(r)
	card, err := api.store.GetCard(r.Context(), identity.WorkspaceID, cardID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	currentAttempt, err := api.store.CurrentAttemptView(r.Context(), identity.WorkspaceID, cardID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	timeline, err := api.store.ListCardTimeline(r.Context(), identity.WorkspaceID, cardID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	pendingTurns, err := api.store.ListPendingTurns(r.Context(), identity.WorkspaceID, cardID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	attemptHistory, err := api.store.ListRecentAttempts(r.Context(), identity.WorkspaceID, cardID, 50)
	if err != nil {
		api.handleError(w, err)
		return
	}
	schedules, err := api.store.ListSchedules(r.Context(), identity.WorkspaceID, cardID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	health, err := api.store.Health(r.Context(), identity.WorkspaceID, cardID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	lastCheckin, err := api.store.LatestCheckin(r.Context(), identity.WorkspaceID, cardID)
	if err != nil {
		api.handleError(w, err)
		return
	}

	response := cardDetailResponse{
		Card:           cardToResponse(card),
		Timeline:       timelineToResponse(timeline),
		PendingTurns:   pendingTurnsToResponse(pendingTurns),
		AttemptHistory: attemptsToResponse(attemptHistory),
		Schedules:      schedulesToResponse(schedules),
		Health:         string(health),
	}
	if currentAttempt != nil {
		attempt := attemptToResponse(*currentAttempt)
		response.CurrentAttempt = &attempt
		response.Card.CurrentAttemptStatus = string(currentAttempt.Status)
	}
	if lastCheckin != nil {
		for i := len(response.Timeline) - 1; i >= 0; i-- {
			if response.Timeline[i].ID == lastCheckin.ID.String() {
				checkin := response.Timeline[i]
				response.LastCheckin = &checkin
				break
			}
		}
	}
	if card.Kind == string(domain.CardKindOngoing) {
		response.Card.Health = string(health)
	}
	writeJSON(w, http.StatusOK, response)
}

func (api *API) updateCard(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	var request struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Title = strings.TrimSpace(request.Title)
	if request.Title == "" {
		writeError(w, http.StatusBadRequest, "invalid_title", "card title is required")
		return
	}
	identity := identityFromRequest(r)
	card, err := api.store.UpdateCardText(
		r.Context(), identity.WorkspaceID, cardID,
		request.Title, strings.TrimSpace(request.Description),
	)
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cardToResponse(card))
}

func (api *API) moveCard(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	var request struct {
		State string `json:"state"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	state := domain.TodoState(request.State)
	switch state {
	case domain.TodoStateTodo, domain.TodoStateWorking, domain.TodoStateReview, domain.TodoStateDone:
	default:
		writeError(w, http.StatusBadRequest, "invalid_state", "unknown todo state")
		return
	}
	identity := identityFromRequest(r)
	if err := api.store.MoveTodo(
		r.Context(), identity.WorkspaceID, cardID, identity.SourceID, state,
	); err != nil {
		api.handleError(w, err)
		return
	}
	card, err := api.store.GetCard(r.Context(), identity.WorkspaceID, cardID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cardToResponse(card))
}

func (api *API) setDiscussionState(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	var request struct {
		State string `json:"state"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if request.State != string(domain.DiscussionStateArchived) {
		writeError(w, http.StatusBadRequest, "invalid_state", "discussion can only be archived")
		return
	}
	identity := identityFromRequest(r)
	if err := api.store.ArchiveDiscussion(
		r.Context(), identity.WorkspaceID, cardID, identity.SourceID,
	); err != nil {
		api.handleError(w, err)
		return
	}
	card, err := api.store.GetCard(r.Context(), identity.WorkspaceID, cardID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cardToResponse(card))
}

func (api *API) deleteDiscussion(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	identity := identityFromRequest(r)
	if err := api.store.DeleteDiscussion(
		r.Context(), identity.WorkspaceID, cardID, identity.SourceID,
	); err != nil {
		api.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) setOngoingState(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	var request struct {
		State string `json:"state"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	identity := identityFromRequest(r)
	if err := api.store.SetOngoingState(
		r.Context(), identity.WorkspaceID, cardID, identity.SourceID,
		domain.OngoingState(request.State), time.Now().UTC(),
	); err != nil {
		api.handleError(w, err)
		return
	}
	card, err := api.store.GetCard(r.Context(), identity.WorkspaceID, cardID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cardToResponse(card))
}

func (api *API) submitComment(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	var request struct {
		Content      string `json:"content"`
		DispatchMode string `json:"dispatch_mode"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Content = strings.TrimSpace(request.Content)
	if request.Content == "" {
		writeError(w, http.StatusBadRequest, "invalid_content", "comment content is required")
		return
	}
	if request.DispatchMode == "" {
		request.DispatchMode = string(domain.DispatchModeEnqueue)
	}
	if request.DispatchMode != string(domain.DispatchModeEnqueue) &&
		request.DispatchMode != string(domain.DispatchModeInterrupt) {
		writeError(w, http.StatusBadRequest, "invalid_dispatch_mode", "dispatch_mode must be enqueue or interrupt")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key header is required")
		return
	}

	identity := identityFromRequest(r)
	result, err := api.store.SubmitSessionComment(r.Context(), store.SubmitSessionCommentParams{
		WorkspaceID: identity.WorkspaceID, CardID: cardID,
		AuthorSourceID: identity.SourceID,
		Content:        request.Content, IdempotencyKey: idempotencyKey,
		DispatchMode: domain.DispatchMode(request.DispatchMode),
	})
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, commentResultToResponse(result))
}

func (api *API) replyAndContinue(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	var request struct {
		Content string `json:"content"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	request.Content = strings.TrimSpace(request.Content)
	if request.Content == "" {
		writeError(w, http.StatusBadRequest, "invalid_content", "reply content is required")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key header is required")
		return
	}

	identity := identityFromRequest(r)
	result, err := api.store.ReplySessionAndContinue(r.Context(), store.SubmitSessionCommentParams{
		WorkspaceID: identity.WorkspaceID, CardID: cardID,
		AuthorSourceID: identity.SourceID,
		Content:        request.Content, IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, commentResultToResponse(result))
}

func (api *API) cancelPendingTurn(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	turnID, ok := parseUUIDParam(w, r, "turnID")
	if !ok {
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	identity := identityFromRequest(r)
	if err := api.store.CancelPendingTurn(
		r.Context(), identity.WorkspaceID, cardID, turnID, identity.SourceID, request.Reason,
	); err != nil {
		api.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) cancelAttempt(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	attemptID, ok := parseUUIDParam(w, r, "attemptID")
	if !ok {
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	identity := identityFromRequest(r)
	if err := api.store.CancelAttempt(
		r.Context(), identity.WorkspaceID, cardID, attemptID, identity.SourceID, request.Reason, time.Now().UTC(),
	); err != nil {
		api.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) retryAttempt(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	attemptID, ok := parseUUIDParam(w, r, "attemptID")
	if !ok {
		return
	}
	var request struct{}
	if !decodeJSON(w, r, &request) {
		return
	}
	identity := identityFromRequest(r)
	settings, err := api.store.GetAgentSettings(r.Context(), identity.WorkspaceID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	result, err := api.store.RetryAttempt(
		r.Context(), identity.WorkspaceID, cardID, attemptID, settings.SourceID, domain.AttemptReasonRetry,
	)
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, retryResultResponse{
		AttemptID: result.Attempt.ID.String(),
		Reused:    result.Reused,
	})
}

type scheduleRequest struct {
	Enabled    *bool  `json:"enabled,omitempty"`
	Frequency  string `json:"frequency"`
	DayOfWeek  *int   `json:"day_of_week,omitempty"`
	DayOfMonth *int   `json:"day_of_month,omitempty"`
	Hour       int    `json:"hour"`
	Minute     int    `json:"minute"`
	Timezone   string `json:"timezone"`
}

func (api *API) listSchedules(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	identity := identityFromRequest(r)
	schedules, err := api.store.ListSchedules(r.Context(), identity.WorkspaceID, cardID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, schedulesToResponse(schedules))
}

func (api *API) createSchedule(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	var request scheduleRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	rule, ok := scheduleRuleFromRequest(w, request)
	if !ok {
		return
	}
	identity := identityFromRequest(r)
	schedule, err := api.store.CreateSchedule(
		r.Context(), identity.WorkspaceID, cardID, rule, time.Now().UTC(),
	)
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, scheduleFromStore(schedule))
}

func (api *API) updateSchedule(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	scheduleID, ok := parseUUIDParam(w, r, "scheduleID")
	if !ok {
		return
	}
	var request scheduleRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	rule, ok := scheduleRuleFromRequest(w, request)
	if !ok {
		return
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	identity := identityFromRequest(r)
	schedule, err := api.store.UpdateSchedule(r.Context(), store.UpdateScheduleParams{
		WorkspaceID: identity.WorkspaceID, CardID: cardID, ScheduleID: scheduleID,
		Rule: rule, Enabled: enabled, After: time.Now().UTC(),
	})
	if err != nil {
		api.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, scheduleFromStore(schedule))
}

func (api *API) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	cardID, ok := parseUUIDParam(w, r, "cardID")
	if !ok {
		return
	}
	scheduleID, ok := parseUUIDParam(w, r, "scheduleID")
	if !ok {
		return
	}
	identity := identityFromRequest(r)
	if err := api.store.DeleteSchedule(r.Context(), identity.WorkspaceID, cardID, scheduleID); err != nil {
		api.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func scheduleRuleFromRequest(w http.ResponseWriter, request scheduleRequest) (domain.ScheduleRule, bool) {
	rule := domain.ScheduleRule{
		Frequency: domain.ScheduleFrequency(request.Frequency),
		Hour:      request.Hour,
		Minute:    request.Minute,
		Timezone:  strings.TrimSpace(request.Timezone),
	}
	if request.DayOfWeek != nil {
		if *request.DayOfWeek < 0 || *request.DayOfWeek > 6 {
			writeError(w, http.StatusBadRequest, "invalid_schedule", "day_of_week must be 0..6")
			return domain.ScheduleRule{}, false
		}
		weekday := time.Weekday(*request.DayOfWeek)
		rule.DayOfWeek = &weekday
	}
	rule.DayOfMonth = request.DayOfMonth
	if err := rule.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_schedule", err.Error())
		return domain.ScheduleRule{}, false
	}
	return rule, true
}

func parseUUIDParam(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_id", "invalid "+name)
		return uuid.Nil, false
	}
	return id, true
}

func projectToResponse(project db.Project) projectResponse {
	return projectResponse{
		ID: project.ID.String(), Name: project.Name, Description: project.Description,
		Instructions: project.Instructions, CreatedAt: project.CreatedAt.Time, UpdatedAt: project.UpdatedAt.Time,
	}
}

func cardToResponse(card db.Card) cardResponse {
	response := cardResponse{
		ID: card.ID.String(), ProjectID: card.ProjectID.String(), Kind: card.Kind,
		Title: card.Title, Description: card.Description,
		CreatedAt: card.CreatedAt.Time, UpdatedAt: card.UpdatedAt.Time,
	}
	if card.DiscussionState.Valid {
		response.DiscussionState = card.DiscussionState.String
	}
	if card.TodoState.Valid {
		response.TodoState = card.TodoState.String
	}
	if card.OngoingState.Valid {
		response.OngoingState = card.OngoingState.String
	}
	if card.SourceDiscussionID.Valid {
		response.SourceDiscussionID = card.SourceDiscussionID.UUID.String()
	}
	return response
}

func cardSummaryToResponse(card db.ListProjectCardSummariesRow) cardResponse {
	response := cardResponse{
		ID: card.ID.String(), ProjectID: card.ProjectID.String(), Kind: card.Kind,
		Title: card.Title, Description: card.Description,
		Health: card.Health, CreatedAt: card.CreatedAt.Time, UpdatedAt: card.UpdatedAt.Time,
	}
	if card.DiscussionState.Valid {
		response.DiscussionState = card.DiscussionState.String
	}
	if card.TodoState.Valid {
		response.TodoState = card.TodoState.String
	}
	if card.OngoingState.Valid {
		response.OngoingState = card.OngoingState.String
	}
	if card.SourceDiscussionID.Valid {
		response.SourceDiscussionID = card.SourceDiscussionID.UUID.String()
	}
	if card.CurrentAttemptStatus.Valid {
		response.CurrentAttemptStatus = card.CurrentAttemptStatus.String
	}
	return response
}

func attemptToResponse(item store.SessionAttemptView) attemptResponse {
	response := attemptResponse{
		ID:              item.ID.String(),
		TurnID:          item.TurnID.String(),
		AttemptNumber:   item.AttemptNumber,
		Reason:          string(item.Reason),
		Status:          string(item.Status),
		CurrentStage:    item.CurrentStage,
		ProgressSummary: item.ProgressSummary,
		ResultSummary:   item.ResultSummary,
		ErrorSummary:    item.ErrorSummary,
		CanRetry:        item.CanRetry,
		CreatedAt:       item.CreatedAt,
		StartedAt:       item.StartedAt,
		FinishedAt:      item.FinishedAt,
	}
	if item.SourceEventID.Valid {
		response.SourceEventID = item.SourceEventID.UUID.String()
	}
	if item.RetryOfAttemptID.Valid {
		response.RetryOfAttemptID = item.RetryOfAttemptID.UUID.String()
	}
	return response
}

func attemptsToResponse(items []store.SessionAttemptView) []attemptResponse {
	response := make([]attemptResponse, 0, len(items))
	for _, item := range items {
		response = append(response, attemptToResponse(item))
	}
	return response
}

func timelineToResponse(rows []db.ListCardTimelineRow) []eventResponse {
	response := make([]eventResponse, 0, len(rows))
	for _, row := range rows {
		item := eventResponse{
			ID: row.ID.String(), Sequence: row.Sequence, Type: row.EventType,
			Content: row.Content, ActorName: row.ActorName, ActorKind: row.ActorKind,
			CreatedAt: row.CreatedAt.Time,
		}
		if row.CheckinStatus.Valid {
			item.CheckinStatus = row.CheckinStatus.String
		}
		if row.AttemptID.Valid {
			item.AttemptID = row.AttemptID.UUID.String()
		}
		response = append(response, item)
	}
	return response
}

func pendingTurnsToResponse(items []store.PendingTurnView) []pendingTurnResponse {
	response := make([]pendingTurnResponse, 0, len(items))
	for _, item := range items {
		entry := pendingTurnResponse{
			ID:           item.ID.String(),
			Sequence:     item.Sequence,
			Source:       string(item.Source),
			DispatchMode: string(item.DispatchMode),
			Content:      item.Content,
			CreatedAt:    item.CreatedAt,
		}
		if item.SourceEventID.Valid {
			entry.SourceEventID = item.SourceEventID.UUID.String()
		}
		response = append(response, entry)
	}
	return response
}

func schedulesToResponse(items []db.CardSchedule) []scheduleResponse {
	response := make([]scheduleResponse, 0, len(items))
	for _, item := range items {
		response = append(response, scheduleFromDB(item))
	}
	return response
}

func scheduleFromDB(item db.CardSchedule) scheduleResponse {
	hour, minute := scheduleHourMinute(item.TimeOfDay.Microseconds)
	response := scheduleResponse{
		ID: item.ID.String(), Enabled: item.Enabled, Frequency: item.Frequency,
		Hour: hour, Minute: minute,
		Timezone: item.Timezone, NextTriggerAt: item.NextTriggerAt.Time,
	}
	if item.DayOfWeek.Valid {
		value := int(item.DayOfWeek.Int16)
		response.DayOfWeek = &value
	}
	if item.DayOfMonth.Valid {
		value := int(item.DayOfMonth.Int16)
		response.DayOfMonth = &value
	}
	if item.LastTriggeredAt.Valid {
		value := item.LastTriggeredAt.Time
		response.LastTriggeredAt = &value
	}
	return response
}

func scheduleHourMinute(micros int64) (int, int) {
	hour := int(micros / int64(time.Hour/time.Microsecond))
	minute := int((micros % int64(time.Hour/time.Microsecond)) / int64(time.Minute/time.Microsecond))
	return hour, minute
}

func scheduleFromStore(item store.CardSchedule) scheduleResponse {
	response := scheduleResponse{
		ID: item.ID.String(), Enabled: item.Enabled, Frequency: string(item.Rule.Frequency),
		Hour: item.Rule.Hour, Minute: item.Rule.Minute, Timezone: item.Rule.Timezone,
		NextTriggerAt: item.NextTriggerAt, LastTriggeredAt: item.LastTriggered,
	}
	if item.Rule.DayOfWeek != nil {
		value := int(*item.Rule.DayOfWeek)
		response.DayOfWeek = &value
	}
	response.DayOfMonth = item.Rule.DayOfMonth
	return response
}

func commentResultToResponse(result store.SubmitSessionCommentResult) commentResultResponse {
	return commentResultResponse{
		EventID: result.EventID.String(),
		TurnID:  result.TurnID.String(),
		Reused:  result.Reused,
	}
}
