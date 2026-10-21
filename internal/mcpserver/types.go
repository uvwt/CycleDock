package mcpserver

type EmptyInput struct{}

type ListCardsInput struct{}

type GetCardInput struct {
	CardID     string `json:"card_id" jsonschema:"CycleDock card UUID"`
	EventLimit int32  `json:"event_limit,omitempty" jsonschema:"Recent durable timeline events to return, 1-100; default 50"`
}

type CreateDiscussionInput struct {
	ProjectID      string `json:"project_id" jsonschema:"Project UUID"`
	IdempotencyKey string `json:"idempotency_key" jsonschema:"Stable unique key for safe retries"`
}

type GenerateWorkInput struct {
	DiscussionID          string `json:"discussion_id" jsonschema:"Source Discussion UUID"`
	Kind                  string `json:"kind" jsonschema:"AI-classified work kind: todo or ongoing"`
	Title                 string `json:"title"`
	Description           string `json:"description,omitempty"`
	RequiredConfirmations int32  `json:"required_confirmations,omitempty" jsonschema:"Todo confirmation threshold; default 1"`
	IdempotencyKey        string `json:"idempotency_key" jsonschema:"Stable unique key for safe retries"`
}

type UpdateCardInput struct {
	CardID      string  `json:"card_id"`
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
}

type MoveCardInput struct {
	CardID string `json:"card_id"`
	State  string `json:"state" jsonschema:"Agent-visible Todo state: todo or working. Review requires confirm_card; Done requires a human."`
}

type AddCommentInput struct {
	CardID         string `json:"card_id"`
	Content        string `json:"content"`
	IdempotencyKey string `json:"idempotency_key"`
}

type CreateFollowupInput struct {
	CardID         string `json:"card_id"`
	Content        string `json:"content" jsonschema:"Durable new work direction added to the Card context"`
	IdempotencyKey string `json:"idempotency_key"`
}

type ClaimWorkInput struct {
	CardID string `json:"card_id"`
}

type HeartbeatRunInput struct {
	RunID string `json:"run_id"`
}

type ReportProgressInput struct {
	RunID          string `json:"run_id"`
	Stage          string `json:"stage"`
	Summary        string `json:"summary" jsonschema:"Current durable progress summary"`
	EventContent   string `json:"event_content,omitempty" jsonschema:"Only set when progress itself is a meaningful timeline fact"`
	IdempotencyKey string `json:"idempotency_key,omitempty" jsonschema:"Required when event_content is set"`
}

type AddFindingInput struct {
	RunID          string `json:"run_id"`
	Content        string `json:"content" jsonschema:"Durable finding worth carrying into future runs"`
	IdempotencyKey string `json:"idempotency_key"`
}

type CheckInInput struct {
	RunID          string `json:"run_id"`
	Status         string `json:"status" jsonschema:"healthy, warning, or critical"`
	Content        string `json:"content"`
	IdempotencyKey string `json:"idempotency_key"`
}

type FinishRunInput struct {
	RunID          string `json:"run_id"`
	Outcome        string `json:"outcome" jsonschema:"completed, failed, or waiting_human"`
	Result         string `json:"result,omitempty" jsonschema:"Durable final result when completed"`
	Stage          string `json:"stage,omitempty" jsonschema:"Current stage when waiting_human"`
	Summary        string `json:"summary,omitempty" jsonschema:"Failure or waiting_human summary"`
	IdempotencyKey string `json:"idempotency_key,omitempty" jsonschema:"Required for completed or failed outcomes"`
}

type ConfirmCardInput struct {
	CardID         string `json:"card_id"`
	Content        string `json:"content" jsonschema:"What was verified before handing the card to human Review"`
	IdempotencyKey string `json:"idempotency_key"`
}

type ProjectView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
}

type CardView struct {
	ID                    string `json:"id"`
	ProjectID             string `json:"project_id"`
	ProjectName           string `json:"project_name,omitempty"`
	Kind                  string `json:"kind"`
	Title                 string `json:"title"`
	Description           string `json:"description"`
	DiscussionState       string `json:"discussion_state,omitempty"`
	TodoState             string `json:"todo_state,omitempty"`
	OngoingState          string `json:"ongoing_state,omitempty"`
	SourceDiscussionID    string `json:"source_discussion_id,omitempty"`
	Health                string `json:"health,omitempty"`
	RequiredConfirmations int32  `json:"required_confirmations"`
	CurrentRunStatus      string `json:"current_run_status,omitempty"`
	PendingTriggerCount   int64  `json:"pending_trigger_count,omitempty"`
}

type EventView struct {
	ID            string `json:"id"`
	Sequence      int64  `json:"sequence"`
	Type          string `json:"type"`
	ActorName     string `json:"actor_name"`
	ActorKind     string `json:"actor_kind"`
	Content       string `json:"content"`
	CheckinStatus string `json:"checkin_status,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	CreatedAt     string `json:"created_at"`
}

type RunView struct {
	ID              string `json:"id"`
	CardID          string `json:"card_id"`
	TriggerID       string `json:"trigger_id"`
	Status          string `json:"status"`
	CurrentStage    string `json:"current_stage,omitempty"`
	ProgressSummary string `json:"progress_summary,omitempty"`
	ResultSummary   string `json:"result_summary,omitempty"`
	ErrorSummary    string `json:"error_summary,omitempty"`
}

type QueueView struct {
	ID           string `json:"id"`
	Sequence     int64  `json:"sequence"`
	Cause        string `json:"cause"`
	DispatchMode string `json:"dispatch_mode"`
	Content      string `json:"content"`
}

type ListProjectsOutput struct {
	Projects []ProjectView `json:"projects"`
}

type ListCardsOutput struct {
	Cards []CardView `json:"cards"`
}

type GetCardOutput struct {
	Card       CardView    `json:"card"`
	Timeline   []EventView `json:"timeline"`
	Queue      []QueueView `json:"queue"`
	CurrentRun *RunView    `json:"current_run,omitempty"`
}

type CardOutput struct {
	Card   CardView `json:"card"`
	Reused bool     `json:"reused,omitempty"`
}

type EventOutput struct {
	EventID string `json:"event_id"`
	Reused  bool   `json:"reused,omitempty"`
}

type FollowupOutput struct {
	EventID   string `json:"event_id"`
	TriggerID string `json:"trigger_id"`
	Reused    bool   `json:"reused,omitempty"`
}

type RunOutput struct {
	Run    RunView `json:"run"`
	Reused bool    `json:"reused,omitempty"`
}

type ProgressOutput struct {
	EventID string `json:"event_id,omitempty"`
	Reused  bool   `json:"reused,omitempty"`
}

type FinishRunOutput struct {
	Run RunView `json:"run"`
}

type ConfirmCardOutput struct {
	EventID           string `json:"event_id"`
	ConfirmationCount int64  `json:"confirmation_count"`
	MovedToReview     bool   `json:"moved_to_review"`
	Reused            bool   `json:"reused,omitempty"`
}
