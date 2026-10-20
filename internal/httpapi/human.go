package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/store/db"
)

func (api *API) mountHumanRoutes(r chi.Router) {
	r.Get("/projects", api.listProjects)
	r.Post("/projects", api.createProject)
	r.Get("/projects/{projectID}", api.getProject)
	r.Patch("/projects/{projectID}", api.updateProject)
	r.Get("/projects/{projectID}/cards", api.listProjectCards)
	r.Post("/projects/{projectID}/cards", api.createCard)

	r.Get("/cards/{cardID}", api.getCard)
	r.Patch("/cards/{cardID}", api.updateCard)
	r.Post("/cards/{cardID}/move", api.moveCard)
	r.Post("/cards/{cardID}/ongoing-state", api.setOngoingState)
	r.Post("/cards/{cardID}/comments", api.submitComment)
	r.Post("/cards/{cardID}/reply", api.replyAndContinue)

	r.Get("/cards/{cardID}/schedules", api.listSchedules)
	r.Post("/cards/{cardID}/schedules", api.createSchedule)
	r.Put("/cards/{cardID}/schedules/{scheduleID}", api.updateSchedule)
	r.Delete("/cards/{cardID}/schedules/{scheduleID}", api.deleteSchedule)

	r.Get("/agent/settings", api.getAgentSettings)
	r.Put("/agent/settings", api.saveAgentSettings)
	r.Get("/agent/mcp-connections", api.listAgentMCPConnections)
	r.Post("/agent/mcp-connections", api.createAgentMCPConnection)
	r.Put("/agent/mcp-connections/{connectionID}", api.updateAgentMCPConnection)
	r.Delete("/agent/mcp-connections/{connectionID}", api.deleteAgentMCPConnection)
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
	ID               string    `json:"id"`
	ProjectID        string    `json:"project_id"`
	Kind             string    `json:"kind"`
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	NormalColumn     string    `json:"normal_column,omitempty"`
	OngoingState     string    `json:"ongoing_state,omitempty"`
	Health           string    `json:"health,omitempty"`
	CurrentRunStatus string    `json:"current_run_status,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type runResponse struct {
	ID              string    `json:"id"`
	Status          string    `json:"status"`
	CurrentStage    string    `json:"current_stage,omitempty"`
	ProgressSummary string    `json:"progress_summary,omitempty"`
	ResultSummary   string    `json:"result_summary,omitempty"`
	ErrorSummary    string    `json:"error_summary,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type eventResponse struct {
	ID            string    `json:"id"`
	Sequence      int64     `json:"sequence"`
	Type          string    `json:"type"`
	Content       string    `json:"content"`
	CheckinStatus string    `json:"checkin_status,omitempty"`
	ActorName     string    `json:"actor_name"`
	ActorKind     string    `json:"actor_kind"`
	RunID         string    `json:"run_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type queueItemResponse struct {
	ID           string    `json:"id"`
	Sequence     int64     `json:"sequence"`
	Cause        string    `json:"cause"`
	DispatchMode string    `json:"dispatch_mode"`
	Content      string    `json:"content"`
	CreatedAt    time.Time `json:"created_at"`
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

type cardDetailResponse struct {
	Card        cardResponse        `json:"card"`
	CurrentRun  *runResponse        `json:"current_run"`
	Timeline    []eventResponse     `json:"timeline"`
	Queue       []queueItemResponse `json:"queue"`
	Schedules   []scheduleResponse  `json:"schedules"`
	Health      string              `json:"health"`
	LastCheckin *eventResponse      `json:"last_checkin"`
}

type commentResultResponse struct {
	EventID   string `json:"event_id"`
	TriggerID string `json:"trigger_id"`
	RunID     string `json:"run_id,omitempty"`
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

func (api *API) createCard(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseUUIDParam(w, r, "projectID")
	if !ok {
		return
	}
	var request struct {
		Kind        string `json:"kind"`
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
	if _, err := api.store.GetProject(r.Context(), identity.WorkspaceID, projectID); err != nil {
		api.handleError(w, err)
		return
	}
	var card db.Card
	var err error
	switch domain.CardKind(request.Kind) {
	case domain.CardKindNormal:
		card, err = api.store.CreateNormalCard(
			r.Context(), identity.WorkspaceID, projectID, identity.SourceID,
			request.Title, strings.TrimSpace(request.Description),
		)
	case domain.CardKindOngoing:
		card, err = api.store.CreateOngoingCard(
			r.Context(), identity.WorkspaceID, projectID, identity.SourceID,
			request.Title, strings.TrimSpace(request.Description),
		)
	default:
		writeError(w, http.StatusBadRequest, "invalid_kind", "card kind must be normal or ongoing")
		return
	}
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
	currentRun, err := api.store.CurrentRun(r.Context(), identity.WorkspaceID, cardID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	timeline, err := api.store.ListCardTimeline(r.Context(), identity.WorkspaceID, cardID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	queue, err := api.store.ListPendingQueue(r.Context(), identity.WorkspaceID, cardID)
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
		Card:      cardToResponse(card),
		Timeline:  timelineToResponse(timeline),
		Queue:     queueToResponse(queue),
		Schedules: schedulesToResponse(schedules),
		Health:    string(health),
	}
	if currentRun != nil {
		run := runToResponse(*currentRun)
		response.CurrentRun = &run
		response.Card.CurrentRunStatus = currentRun.Status
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
		Column string `json:"column"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	column := domain.NormalColumn(request.Column)
	switch column {
	case domain.NormalColumnDiscussion, domain.NormalColumnTodo, domain.NormalColumnWorking,
		domain.NormalColumnReview, domain.NormalColumnDone:
	default:
		writeError(w, http.StatusBadRequest, "invalid_column", "unknown normal card column")
		return
	}
	identity := identityFromRequest(r)
	if err := api.store.MoveNormalCard(
		r.Context(), identity.WorkspaceID, cardID, identity.SourceID,
		column,
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
	result, err := api.store.SubmitComment(r.Context(), store.SubmitCommentParams{
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
	result, err := api.store.ReplyAndContinue(r.Context(), store.SubmitCommentParams{
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
	if card.NormalColumn.Valid {
		response.NormalColumn = card.NormalColumn.String
	}
	if card.OngoingState.Valid {
		response.OngoingState = card.OngoingState.String
	}
	return response
}

func cardSummaryToResponse(card db.ListProjectCardSummariesRow) cardResponse {
	response := cardResponse{
		ID: card.ID.String(), ProjectID: card.ProjectID.String(), Kind: card.Kind,
		Title: card.Title, Description: card.Description,
		Health: card.Health, CreatedAt: card.CreatedAt.Time, UpdatedAt: card.UpdatedAt.Time,
	}
	if card.NormalColumn.Valid {
		response.NormalColumn = card.NormalColumn.String
	}
	if card.OngoingState.Valid {
		response.OngoingState = card.OngoingState.String
	}
	if card.CurrentRunStatus.Valid {
		response.CurrentRunStatus = card.CurrentRunStatus.String
	}
	return response
}

func runToResponse(run db.AgentRun) runResponse {
	return runResponse{
		ID: run.ID.String(), Status: run.Status, CurrentStage: run.CurrentStage,
		ProgressSummary: run.ProgressSummary, ResultSummary: run.ResultSummary,
		ErrorSummary: run.ErrorSummary, CreatedAt: run.CreatedAt.Time,
	}
}

func timelineToResponse(rows []db.ListCardTimelineRow) []eventResponse {
	response := make([]eventResponse, 0, len(rows))
	for _, row := range rows {
		item := eventResponse{
			ID: row.ID.String(), Sequence: row.Sequence.Int64, Type: row.EventType,
			Content: row.Content, ActorName: row.ActorName, ActorKind: row.ActorKind,
			CreatedAt: row.CreatedAt.Time,
		}
		if row.CheckinStatus.Valid {
			item.CheckinStatus = row.CheckinStatus.String
		}
		if row.RunID.Valid {
			item.RunID = row.RunID.UUID.String()
		}
		response = append(response, item)
	}
	return response
}

func queueToResponse(items []store.PendingTrigger) []queueItemResponse {
	response := make([]queueItemResponse, 0, len(items))
	for _, item := range items {
		response = append(response, queueItemResponse{
			ID: item.ID.String(), Sequence: item.Sequence, Cause: string(item.Cause),
			DispatchMode: string(item.DispatchMode), Content: item.Content, CreatedAt: item.CreatedAt,
		})
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
	micros := item.TimeOfDay.Microseconds
	response := scheduleResponse{
		ID: item.ID.String(), Enabled: item.Enabled, Frequency: item.Frequency,
		Hour:     int(micros / int64(time.Hour/time.Microsecond)),
		Minute:   int((micros % int64(time.Hour/time.Microsecond)) / int64(time.Minute/time.Microsecond)),
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

func commentResultToResponse(result store.SubmitCommentResult) commentResultResponse {
	response := commentResultResponse{
		EventID: result.EventID.String(), TriggerID: result.TriggerID.String(), Reused: result.Reused,
	}
	if result.RunID != nil {
		response.RunID = result.RunID.String()
	}
	return response
}
