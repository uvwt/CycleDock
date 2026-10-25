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

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelauth"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/nexusdock"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
)

type DispatcherConfig struct {
	PollInterval     time.Duration
	MaintenanceEvery time.Duration
	MaxConcurrent    int
	LeaseDuration    time.Duration
	SessionService   session.Service
	ArtifactService  artifact.Service
	ModelAuth        *modelauth.Service
	NexusDock        *nexusdock.Service
	Live             *realtime.Hub
}

type Dispatcher struct {
	store            *store.Store
	runner           *Runner
	logger           *slog.Logger
	pollInterval     time.Duration
	maintenanceEvery time.Duration
	maxConcurrent    int
	leaseDuration    time.Duration
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
		config.MaxConcurrent = 4
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = store.DefaultClaimLease
	}
	runner := NewRunner(dataStore, model, logger, RunnerConfig{
		HeartbeatInterval: config.LeaseDuration / 4,
		LeaseDuration:     config.LeaseDuration,
		SessionService:    config.SessionService,
		ArtifactService:   config.ArtifactService,
		ModelAuth:         config.ModelAuth,
		NexusDock:         config.NexusDock,
		Live:              config.Live,
	})
	return &Dispatcher{
		store: dataStore, runner: runner, logger: logger,
		pollInterval: config.PollInterval, maintenanceEvery: config.MaintenanceEvery,
		maxConcurrent: config.MaxConcurrent, leaseDuration: config.LeaseDuration,
		wake: make(chan struct{}, 1),
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
	workspaceIDs, err := d.store.ListWorkspaceIDs(ctx)
	if err != nil {
		return nil, err
	}
	for _, workspaceID := range workspaceIDs {
		settings, err := d.store.GetAgentSettings(ctx, workspaceID)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !runnableAgentSettings(settings) {
			continue
		}
		claim, err := d.store.ClaimNextTurnAttempt(
			ctx,
			workspaceID,
			settings.SourceID,
			time.Now().UTC(),
			d.leaseDuration,
		)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return claim, nil
	}
	return nil, domain.ErrNotFound
}

func (d *Dispatcher) runAttempt(ctx context.Context, claim store.AttemptClaim) {
	err := d.runner.RunAttempt(ctx, claim)
	if err == nil ||
		errors.Is(err, domain.ErrAttemptNotCurrent) ||
		errors.Is(err, context.Canceled) {
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

func publicRunError(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *modelclient.APIError
	switch {
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
		return "Built-in Agent stopped an unusually long tool loop before completion."
	case strings.Contains(err.Error(), "environment variable"):
		return "Built-in Agent secret environment variable is not configured."
	case errors.Is(err, modelauth.ErrNotConnected), errors.Is(err, modelauth.ErrReauthRequired):
		return "Built-in Agent model account must be connected again."
	case errors.Is(err, modelauth.ErrCredentialRefresh):
		return "Built-in Agent model account refresh failed."
	case errors.Is(err, modelauth.ErrUnavailable):
		return "Built-in Agent model account authentication is not configured."
	case strings.Contains(err.Error(), "MCP"):
		return "Built-in Agent MCP connection or tool call failed."
	default:
		return "Built-in Agent run failed before completion."
	}
}
