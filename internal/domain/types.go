package domain

import "errors"

type CardKind string

const (
	CardKindNormal  CardKind = "normal"
	CardKindOngoing CardKind = "ongoing"
)

type NormalColumn string

const (
	NormalColumnDiscussion NormalColumn = "discussion"
	NormalColumnTodo       NormalColumn = "todo"
	NormalColumnWorking    NormalColumn = "working"
	NormalColumnReview     NormalColumn = "review"
	NormalColumnDone       NormalColumn = "done"
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

type TriggerCause string

const (
	TriggerCauseHumanComment      TriggerCause = "human_comment"
	TriggerCauseScheduledFollowup TriggerCause = "scheduled_followup"
	TriggerCauseManualRetry       TriggerCause = "manual_retry"
	TriggerCauseSystemFollowup    TriggerCause = "system_followup"
)

type DispatchMode string

const (
	DispatchModeEnqueue   DispatchMode = "enqueue"
	DispatchModeInterrupt DispatchMode = "interrupt"
)

type RunStatus string

const (
	RunStatusQueued       RunStatus = "queued"
	RunStatusRunning      RunStatus = "running"
	RunStatusWaitingHuman RunStatus = "waiting_human"
	RunStatusCompleted    RunStatus = "completed"
	RunStatusFailed       RunStatus = "failed"
	RunStatusSuperseded   RunStatus = "superseded"
	RunStatusExpired      RunStatus = "expired"
	RunStatusCancelled    RunStatus = "cancelled"
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
	ErrHumanRequired       = errors.New("human source required")
	ErrInvalidTransition   = errors.New("invalid card state transition")
)
