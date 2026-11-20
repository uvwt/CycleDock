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
	EventTypeCheckinAck   EventType = "checkin_ack"
	EventTypeToolApproval EventType = "tool_approval"
)

type ToolGuardMode string

const (
	ToolGuardModeDisabled      ToolGuardMode = "disabled"
	ToolGuardModeBlockHighRisk ToolGuardMode = "block_high_risk"
	ToolGuardModeStrict        ToolGuardMode = "strict"
)

type ToolApprovalStatus string

const (
	ToolApprovalStatusPending    ToolApprovalStatus = "pending"
	ToolApprovalStatusApproved   ToolApprovalStatus = "approved"
	ToolApprovalStatusRejected   ToolApprovalStatus = "rejected"
	ToolApprovalStatusSuperseded ToolApprovalStatus = "superseded"
)

type CheckinStatus string

const (
	CheckinHealthy      CheckinStatus = "healthy"
	CheckinWarning      CheckinStatus = "warning"
	CheckinCritical     CheckinStatus = "critical"
	CheckinAcknowledged CheckinStatus = "acknowledged"
	CheckinUnknown      CheckinStatus = "unknown"
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
	TurnSourceTrigger  TurnSource = "trigger"
)

type TriggerProvider string

const (
	TriggerProviderGitHub   TriggerProvider = "github"
	TriggerProviderCircleCI TriggerProvider = "circleci"
	TriggerProviderGeneric  TriggerProvider = "generic"
)

type TriggerAuthMode string

const (
	TriggerAuthGitHubHMAC   TriggerAuthMode = "github_hmac_sha256"
	TriggerAuthCircleCIHMAC TriggerAuthMode = "circleci_hmac_sha256"
	TriggerAuthGenericHMAC  TriggerAuthMode = "generic_hmac_sha256"
	TriggerAuthBearer       TriggerAuthMode = "bearer"
)

type TriggerRoutingMode string

const (
	TriggerRoutingFixedCard     TriggerRoutingMode = "fixed_card"
	TriggerRoutingMatchWatchers TriggerRoutingMode = "match_watchers"
	TriggerRoutingCreateTodo    TriggerRoutingMode = "create_todo"
)

type TriggerDeliveryStatus string

const (
	TriggerDeliveryTriggered TriggerDeliveryStatus = "triggered"
	TriggerDeliveryFiltered  TriggerDeliveryStatus = "filtered"
	TriggerDeliveryIgnored   TriggerDeliveryStatus = "ignored"
	TriggerDeliveryCooldown  TriggerDeliveryStatus = "cooldown"
	TriggerDeliveryRejected  TriggerDeliveryStatus = "rejected"
)

type WatcherKind string

const (
	WatcherKindEvent  WatcherKind = "event"
	WatcherKindWakeup WatcherKind = "wakeup"
)

type WatcherStatus string

const (
	WatcherStatusArmed     WatcherStatus = "armed"
	WatcherStatusTriggered WatcherStatus = "triggered"
	WatcherStatusExpired   WatcherStatus = "expired"
	WatcherStatusCancelled WatcherStatus = "cancelled"
)

type AttemptReason string

const (
	AttemptReasonInitial  AttemptReason = "initial"
	AttemptReasonRetry    AttemptReason = "retry"
	AttemptReasonRecovery AttemptReason = "recovery"
)

type AttemptStatus string

const (
	AttemptStatusQueued     AttemptStatus = "queued"
	AttemptStatusRunning    AttemptStatus = "running"
	AttemptStatusCompleted  AttemptStatus = "completed"
	AttemptStatusFailed     AttemptStatus = "failed"
	AttemptStatusSuperseded AttemptStatus = "superseded"
	AttemptStatusExpired    AttemptStatus = "expired"
	AttemptStatusCancelled  AttemptStatus = "cancelled"
)

type ScheduleFrequency string

const (
	ScheduleDaily    ScheduleFrequency = "daily"
	ScheduleWeekly   ScheduleFrequency = "weekly"
	ScheduleMonthly  ScheduleFrequency = "monthly"
	ScheduleInterval ScheduleFrequency = "interval"
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
