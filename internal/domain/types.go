package domain

import "errors"

type CardKind string

const (
	CardKindDiscussion CardKind = "discussion"
	CardKindTodo       CardKind = "todo"
	CardKindOngoing    CardKind = "ongoing"
)

type DiscussionState string

const (
	DiscussionStateActive   DiscussionState = "active"
	DiscussionStateArchived DiscussionState = "archived"
)

type TodoState string

const (
	TodoStateTodo    TodoState = "todo"
	TodoStateWorking TodoState = "working"
	TodoStateReview  TodoState = "review"
	TodoStateDone    TodoState = "done"
)

type OngoingState string

const (
	OngoingStateActive   OngoingState = "active"
	OngoingStatePaused   OngoingState = "paused"
	OngoingStateArchived OngoingState = "archived"
)

type SourceKind string

const (
	SourceKindHuman       SourceKind = "human"
	SourceKindCycleAgent  SourceKind = "cycle_agent"
	SourceKindOAuthClient SourceKind = "oauth_client"
)

type EventType string

const (
	EventTypeComment      EventType = "comment"
	EventTypeProgress     EventType = "progress"
	EventTypeFinding      EventType = "finding"
	EventTypeResult       EventType = "result"
	EventTypeCheckin      EventType = "checkin"
	EventTypeConfirm      EventType = "confirm"
	EventTypeReject       EventType = "reject"
	EventTypeStatusChange EventType = "status_change"
)

type CheckinStatus string

const (
	CheckinHealthy  CheckinStatus = "healthy"
	CheckinWarning  CheckinStatus = "warning"
	CheckinCritical CheckinStatus = "critical"
	CheckinUnknown  CheckinStatus = "unknown"
)

type DispatchMode string

const (
	DispatchModeEnqueue   DispatchMode = "enqueue"
	DispatchModeInterrupt DispatchMode = "interrupt"
)

type TurnSource string

const (
	TurnSourceHuman    TurnSource = "human"
	TurnSourceSchedule TurnSource = "schedule"
	TurnSourceSystem   TurnSource = "system"
)

type AttemptReason string

const (
	AttemptReasonInitial  AttemptReason = "initial"
	AttemptReasonRetry    AttemptReason = "retry"
	AttemptReasonRecovery AttemptReason = "recovery"
)

type AttemptStatus string

const (
	AttemptStatusQueued       AttemptStatus = "queued"
	AttemptStatusRunning      AttemptStatus = "running"
	AttemptStatusWaitingHuman AttemptStatus = "waiting_human"
	AttemptStatusCompleted    AttemptStatus = "completed"
	AttemptStatusFailed       AttemptStatus = "failed"
	AttemptStatusSuperseded   AttemptStatus = "superseded"
	AttemptStatusExpired      AttemptStatus = "expired"
	AttemptStatusCancelled    AttemptStatus = "cancelled"
)

type ScheduleFrequency string

const (
	ScheduleDaily   ScheduleFrequency = "daily"
	ScheduleWeekly  ScheduleFrequency = "weekly"
	ScheduleMonthly ScheduleFrequency = "monthly"
)

var (
	ErrNotFound            = errors.New("not found")
	ErrConflict            = errors.New("conflict")
	ErrIdempotencyMismatch = errors.New("idempotency key reused with different request")
	ErrRunNotCurrent       = errors.New("run is no longer current")
	ErrAttemptNotCurrent   = errors.New("attempt is no longer current")
	ErrHumanRequired       = errors.New("human source required")
	ErrInvalidTransition   = errors.New("invalid card state transition")
)
