package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/modelauth"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/nexusdock"
	"github.com/uvwt/CycleDock/internal/store"
)

type DispatcherConfig struct {
	PollInterval     time.Duration
	MaintenanceEvery time.Duration
	MaxConcurrent    int
	LeaseDuration    time.Duration
	ModelAuth        *modelauth.Service
	NexusDock        *nexusdock.Service
}

type Dispatcher struct {
	store            *store.Store
	runner           *Runner
	logger           *slog.Logger
	pollInterval     time.Duration
	maintenanceEvery time.Duration
	maxConcurrent    int
	leaseDuration    time.Duration
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
		ModelAuth:         config.ModelAuth,
		NexusDock:         config.NexusDock,
	})
	return &Dispatcher{
		store: dataStore, runner: runner, logger: logger,
		pollInterval: config.PollInterval, maintenanceEvery: config.MaintenanceEvery,
		maxConcurrent: config.MaxConcurrent, leaseDuration: config.LeaseDuration,
	}
}

func (d *Dispatcher) Run(ctx context.Context) error {
	poll := time.NewTicker(d.pollInterval)
	defer poll.Stop()
	maintenance := time.NewTicker(d.maintenanceEvery)
	defer maintenance.Stop()

	wake := make(chan struct{}, 1)
	slots := make(chan struct{}, d.maxConcurrent)
	var workers sync.WaitGroup

	if err := d.maintain(ctx); err != nil {
		d.logger.Error("Cycle Agent maintenance failed", "error", err)
	}
	d.dispatchAvailable(ctx, slots, &workers, wake)

	for {
		select {
		case <-ctx.Done():
			workers.Wait()
			return nil
		case <-maintenance.C:
			if err := d.maintain(ctx); err != nil && !errors.Is(err, context.Canceled) {
				d.logger.Error("Cycle Agent maintenance failed", "error", err)
			}
			d.dispatchAvailable(ctx, slots, &workers, wake)
		case <-poll.C:
			d.dispatchAvailable(ctx, slots, &workers, wake)
		case <-wake:
			d.dispatchAvailable(ctx, slots, &workers, wake)
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
		if _, err := d.store.ProcessDueSchedules(ctx, workspaceID, now, 100); err != nil {
			return fmt.Errorf("process schedules for workspace %s: %w", workspaceID, err)
		}
		if _, err := d.store.ExpireAndRecoverBuiltInLeases(ctx, workspaceID, now, 100); err != nil {
			return fmt.Errorf("recover expired leases for workspace %s: %w", workspaceID, err)
		}
	}
	return nil
}

func (d *Dispatcher) dispatchAvailable(
	ctx context.Context,
	slots chan struct{},
	workers *sync.WaitGroup,
	wake chan<- struct{},
) {
	for len(slots) < cap(slots) && ctx.Err() == nil {
		claim, err := d.store.ClaimNextBuiltInRun(ctx, time.Now().UTC(), d.leaseDuration)
		if errors.Is(err, domain.ErrNotFound) {
			return
		}
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				d.logger.Error("Cycle Agent claim failed", "error", err)
			}
			return
		}

		slots <- struct{}{}
		workers.Add(1)
		go func(claim store.BuiltInRunClaim) {
			defer func() {
				<-slots
				workers.Done()
				select {
				case wake <- struct{}{}:
				default:
				}
			}()
			d.runClaim(ctx, claim)
		}(*claim)
	}
}

func (d *Dispatcher) runClaim(ctx context.Context, claim store.BuiltInRunClaim) {
	err := d.runner.Run(ctx, claim)
	if err == nil || errors.Is(err, domain.ErrRunNotCurrent) || errors.Is(err, context.Canceled) {
		return
	}
	d.logger.Error(
		"Cycle Agent run failed",
		"workspace_id", claim.WorkspaceID,
		"card_id", claim.CardID,
		"run_id", claim.RunID,
		"summary", publicRunError(err),
	)
	summary := publicRunError(err)
	if failErr := d.store.FailRun(
		context.WithoutCancel(ctx),
		claim.WorkspaceID,
		claim.RunID,
		claim.SourceID,
		summary,
		"builtin-fail:"+claim.RunID.String(),
	); failErr != nil && !errors.Is(failErr, domain.ErrRunNotCurrent) {
		d.logger.Error(
			"Cycle Agent failure could not be persisted",
			"workspace_id", claim.WorkspaceID,
			"run_id", claim.RunID,
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
		errors.Is(err, modelclient.ErrEmptyResponse),
		errors.Is(err, errMalformedModelResult):
		return "Model provider returned an invalid response."
	case errors.Is(err, modelclient.ErrResponseTooLarge):
		return "Model provider response exceeded the safety limit."
	case errors.Is(err, errMaxToolRoundsExceeded):
		return "Built-in Agent reached the configured maximum tool rounds before completion."
	case strings.Contains(err.Error(), "environment variable"):
		return "Built-in Agent secret environment variable is not configured."
	case errors.Is(err, modelauth.ErrNotConnected), errors.Is(err, modelauth.ErrReauthRequired):
		return "Built-in Agent model account must be connected again."
	case errors.Is(err, modelauth.ErrUnavailable):
		return "Built-in Agent model account authentication is not configured."
	case strings.Contains(err.Error(), "MCP"):
		return "Built-in Agent MCP connection or tool call failed."
	default:
		return "Built-in Agent run failed before completion."
	}
}
