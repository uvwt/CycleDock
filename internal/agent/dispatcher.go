package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/session"

	"github.com/uvwt/CycleDock/internal/attachments"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelauth"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/nexusdock"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
)

const (
	DefaultMaxConcurrent             = 32
	DefaultMaxConcurrentPerWorkspace = 10
	// shutdownInterruptTimeout 约束优雅停机时将单个运行中 Attempt 原子交接为
	// recovery queued 状态的数据库超时，避免停机收尾被单次写库无限阻塞。
	shutdownInterruptTimeout = 5 * time.Second
)

type DispatcherConfig struct {
	MaxConcurrentPerWorkspace int
	PollInterval              time.Duration
	MaintenanceEvery          time.Duration
	MaxConcurrent             int
	LeaseDuration             time.Duration
	SessionService            session.Service
	ArtifactService           artifact.Service
	Attachments               *attachments.Service
	ModelAuth                 *modelauth.Service
	NexusDock                 *nexusdock.Service
	Live                      *realtime.Hub
}

type Dispatcher struct {
	store                     *store.Store
	runner                    *Runner
	live                      *realtime.Hub
	logger                    *slog.Logger
	pollInterval              time.Duration
	maintenanceEvery          time.Duration
	maxConcurrentPerWorkspace int
	maxConcurrent             int
	leaseDuration             time.Duration
	// wake 只有一个缓冲。pending 补上「信号还在缓冲里、新提交只能丢掉发送」的窗口：
	// 消费方查完库后若发现期间又有提交，会再查一次。
	wake    chan struct{}
	pending atomic.Bool
}

func NewDispatcher(
	dataStore *store.Store,
	model *modelclient.Client,
	logger *slog.Logger,
	config DispatcherConfig,
) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.MaintenanceEvery <= 0 {
		config.MaintenanceEvery = 5 * time.Second
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = DefaultMaxConcurrent
	}
	if config.MaxConcurrentPerWorkspace <= 0 {
		config.MaxConcurrentPerWorkspace = DefaultMaxConcurrentPerWorkspace
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = store.DefaultClaimLease
	}
	runner := NewRunner(dataStore, model, logger, RunnerConfig{
		HeartbeatInterval: config.LeaseDuration / 4,
		LeaseDuration:     config.LeaseDuration,
		SessionService:    config.SessionService,
		ArtifactService:   config.ArtifactService,
		Attachments:       config.Attachments,
		ModelAuth:         config.ModelAuth,
		NexusDock:         config.NexusDock,
		Live:              config.Live,
	})
	return &Dispatcher{
		store: dataStore, runner: runner, live: config.Live, logger: logger,
		pollInterval: config.PollInterval, maintenanceEvery: config.MaintenanceEvery,
		maxConcurrent: config.MaxConcurrent, leaseDuration: config.LeaseDuration,
		maxConcurrentPerWorkspace: config.MaxConcurrentPerWorkspace,
		wake:                      make(chan struct{}, 1),
	}
}

// Wake 让 Dispatcher 立刻尝试认领。提交评论、回复、重试或 MCP followup 后调用。
// 认领仍然由数据库决定；卡上已有 current Run 时，这次唤醒只会空查一次。
func (d *Dispatcher) Wake() {
	d.pending.Store(true)
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *Dispatcher) Run(ctx context.Context) error {
	poll := time.NewTicker(d.pollInterval)
	defer poll.Stop()
	maintenance := time.NewTicker(d.maintenanceEvery)
	defer maintenance.Stop()

	slots := make(chan struct{}, d.maxConcurrent)
	var workers sync.WaitGroup

	// 标题生成独立于主 Attempt；退出时等待后台任务响应取消。
	metadataDone := make(chan struct{})
	go func() {
		defer close(metadataDone)
		d.runDiscussionMetadata(ctx)
	}()
	defer func() { <-metadataDone }()

	if err := d.maintain(ctx); err != nil {
		d.logger.Error("Cycle Agent maintenance failed", "error", err)
	}
	d.drainReady(ctx, slots, &workers)

	for {
		select {
		case <-ctx.Done():
			workers.Wait()
			return nil
		case <-maintenance.C:
			if err := d.maintain(ctx); err != nil && !errors.Is(err, context.Canceled) {
				d.logger.Error("Cycle Agent maintenance failed", "error", err)
			}
			d.drainReady(ctx, slots, &workers)
		case <-poll.C:
			d.drainReady(ctx, slots, &workers)
		case <-d.wake:
			d.drainReady(ctx, slots, &workers)
		}
	}
}

// drainReady 先清掉 pending，再认领。查询期间若又有提交，pending 会重新变为 true，于是再查一次。
func (d *Dispatcher) drainReady(ctx context.Context, slots chan struct{}, workers *sync.WaitGroup) {
	for {
		d.pending.Store(false)
		d.dispatchAvailable(ctx, slots, workers)
		if !d.pending.Load() {
			return
		}
	}
}

func (d *Dispatcher) maintain(ctx context.Context) error {
	workspaceIDs, err := d.store.ListWorkspaceIDs(ctx)
	if err != nil {
		return fmt.Errorf("list workspaces: %w", err)
	}
	now := time.Now().UTC()
	for _, workspaceID := range workspaceIDs {
		if _, err := d.store.ProcessDueScheduleTurns(ctx, workspaceID, now, 100); err != nil {
			return fmt.Errorf("process schedule turns for workspace %s: %w", workspaceID, err)
		}
		if _, err := d.store.ProcessDueCardWatchers(ctx, workspaceID, now, 100); err != nil {
			return fmt.Errorf("process card watchers for workspace %s: %w", workspaceID, err)
		}
		if _, err := d.store.ProcessBuiltInProactiveTurns(ctx, workspaceID, now); err != nil {
			return fmt.Errorf("process built-in proactive turns for workspace %s: %w", workspaceID, err)
		}
		if _, err := d.store.ExpireAndRecoverBuiltInAttempts(ctx, workspaceID, now, 100); err != nil {
			return fmt.Errorf("recover expired attempts for workspace %s: %w", workspaceID, err)
		}
	}
	return nil
}

func (d *Dispatcher) dispatchAvailable(
	ctx context.Context,
	slots chan struct{},
	workers *sync.WaitGroup,
) {
	for len(slots) < cap(slots) && ctx.Err() == nil {
		claim, err := d.claimNextAttempt(ctx)
		if errors.Is(err, domain.ErrNotFound) {
			return
		}
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				d.logger.Error("Cycle Agent attempt claim failed", "error", err)
			}
			return
		}

		slots <- struct{}{}
		workers.Add(1)
		go func(claim store.AttemptClaim) {
			defer func() {
				<-slots
				workers.Done()
				d.Wake()
			}()
			d.runAttempt(ctx, claim)
		}(*claim)
	}
}

func (d *Dispatcher) claimNextAttempt(ctx context.Context) (*store.AttemptClaim, error) {
	return d.store.ClaimNextBuiltInAttempt(ctx, d.maxConcurrentPerWorkspace, time.Now().UTC(), d.leaseDuration)
}

func (d *Dispatcher) runAttempt(ctx context.Context, claim store.AttemptClaim) {
	err := d.runner.RunAttempt(ctx, claim)
	if err == nil || errors.Is(err, domain.ErrAttemptNotCurrent) {
		return
	}
	// 服务收到 SIGTERM/SIGINT 优雅停机时，Dispatcher 的根 ctx 会被显式取消（context.Canceled）。
	// 仅在 context.Canceled 时交接为非故障重启续跑；若是 context.DeadlineExceeded（如单次模型超时），
	// 仍应按真实失败落盘并汇报错误摘要。
	if errors.Is(ctx.Err(), context.Canceled) {
		d.interruptAttemptForShutdown(ctx, claim)
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	summary := publicRunError(err)
	logADKAttemptFailure(d.logger, claim, err)
	if failErr := d.store.FinishAttempt(
		context.WithoutCancel(ctx),
		claim.Attempt.WorkspaceID,
		claim.Attempt.CardID,
		claim.Attempt.ID,
		domain.AttemptStatusFailed,
		summary,
		time.Now().UTC(),
	); failErr != nil && !errors.Is(failErr, domain.ErrAttemptNotCurrent) {
		d.logger.Error(
			"Cycle Agent failure could not be persisted",
			"workspace_id", claim.Attempt.WorkspaceID,
			"attempt_id", claim.Attempt.ID,
			"error", failErr,
		)
	}
}

func (d *Dispatcher) interruptAttemptForShutdown(ctx context.Context, claim store.AttemptClaim) {
	handoffCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownInterruptTimeout)
	defer cancel()
	result, err := d.store.InterruptAttemptForShutdown(
		handoffCtx,
		claim.Attempt.WorkspaceID,
		claim.Attempt.CardID,
		claim.Attempt.ID,
		time.Now().UTC(),
	)
	if err != nil {
		d.logger.Error(
			"Cycle Agent shutdown handoff failed; lease recovery will take over",
			"workspace_id", claim.Attempt.WorkspaceID,
			"card_id", claim.Attempt.CardID,
			"attempt_id", claim.Attempt.ID,
			"error", err,
		)
		return
	}
	if result.Interrupted {
		d.logger.Info(
			"Cycle Agent attempt handed off for graceful restart",
			"workspace_id", claim.Attempt.WorkspaceID,
			"card_id", claim.Attempt.CardID,
			"attempt_id", claim.Attempt.ID,
			"recovery_queued", result.RecoveryAttempt != nil,
		)
		if d.live != nil {
			d.live.Publish(claim.Attempt.CardID, realtime.Event{Type: "card.updated"})
		}
	}
}

func publicRunError(err error) string {
	if err == nil {
		return ""
	}
	var multiRouteErr *MultiRouteError
	if errors.As(err, &multiRouteErr) && len(multiRouteErr.Entries) > 0 {
		if summary := classifyPublicMultiRouteError(multiRouteErr); summary != "" {
			return summary
		}
	}
	return classifySingleRunError(err)
}

func classifySingleRunError(err error) string {
	var apiErr *modelclient.APIError
	switch {
	case errors.Is(err, ErrRouteContextCapacityExceeded),
		errors.Is(err, ErrRouteContextCapacityUnknown),
		modelclient.IsContextTooLargeError(err):
		return "Model context window is insufficient for the current session size."
	case errors.As(err, &apiErr):
		return fmt.Sprintf("Model provider request failed with HTTP %d.", apiErr.StatusCode)
	case errors.Is(err, context.DeadlineExceeded):
		return "Built-in Agent operation timed out."
	case errors.Is(err, modelclient.ErrMalformedResponse),
		errors.Is(err, modelclient.ErrEmptyResponse):
		return "Model provider returned an invalid response."
	case errors.Is(err, modelclient.ErrResponseTooLarge):
		return "Model provider response exceeded the safety limit."
	case errors.Is(err, errRunawayToolLoop):
		return "Built-in Agent stopped after 8 consecutive identical tool rounds with unchanged results."
	case strings.Contains(err.Error(), "environment variable"):
		return "Built-in Agent secret environment variable is not configured."
	case errors.Is(err, modelauth.ErrNotConnected), errors.Is(err, modelauth.ErrReauthRequired):
		return "Built-in Agent model account must be connected again."
	case errors.Is(err, modelauth.ErrCredentialRefresh):
		return "Built-in Agent model account refresh failed."
	case errors.Is(err, modelauth.ErrUnavailable):
		return "Built-in Agent model account authentication is not configured."
	case strings.Contains(err.Error(), "NexusDock"):
		return "Built-in Agent NexusDock connection or tool call failed."
	case strings.Contains(err.Error(), "MCP"):
		return "Built-in Agent MCP connection or tool call failed."
	default:
		return "Built-in Agent run failed before completion."
	}
}

func isCapacityRunError(err error) bool {
	return errors.Is(err, ErrRouteContextCapacityExceeded) ||
		errors.Is(err, ErrRouteContextCapacityUnknown) ||
		modelclient.IsContextTooLargeError(err)
}

// classifyPublicMultiRouteError 对多路由失败链生成不误导用户的公开提示：
//   - 若全部路由都因上下文窗口不足失败，或仅剩多条小/未知容量备用路由均因窗口不足跳过，报上下文窗口不足；
//   - 若主路由本身容量充足但因上游超时/HTTP 报错失败，而后续备用路由仅因自身窗口较小被预检跳过，
//     在展示真实主路由失败原因的同时补充说明备用路由因上下文窗口无法接管，避免把上游故障伪装成会话超限。
func classifyPublicMultiRouteError(multiErr *MultiRouteError) string {
	if multiErr == nil || len(multiErr.Entries) == 0 {
		return ""
	}
	var primaryNonCapacityErr error
	capacitySkipCount := 0
	for _, entry := range multiErr.Entries {
		if entry.Err == nil {
			continue
		}
		if isCapacityRunError(entry.Err) {
			capacitySkipCount++
			continue
		}
		if primaryNonCapacityErr == nil {
			primaryNonCapacityErr = entry.Err
		}
	}
	if primaryNonCapacityErr == nil && capacitySkipCount > 0 {
		return "Model context window is insufficient for the current session size."
	}
	if primaryNonCapacityErr != nil && capacitySkipCount == 0 {
		return classifySingleRunError(primaryNonCapacityErr)
	}
	if primaryNonCapacityErr != nil && capacitySkipCount > 0 {
		if capacitySkipCount >= 2 {
			return "Model context window is insufficient for the current session size."
		}
		return fmt.Sprintf(
			"%s (backup route context window could not accommodate failover).",
			strings.TrimSuffix(classifySingleRunError(primaryNonCapacityErr), "."),
		)
	}
	return ""
}
