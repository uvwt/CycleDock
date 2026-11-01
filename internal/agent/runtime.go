package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/session"

	"github.com/uvwt/CycleDock/internal/modelauth"
	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/nexusdock"
	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
)

type RunnerConfig struct {
	HeartbeatInterval time.Duration
	LeaseDuration     time.Duration
	SessionService    session.Service
	ArtifactService   artifact.Service
	ModelAuth         *modelauth.Service
	NexusDock         *nexusdock.Service
	Live              *realtime.Hub
}

type Runner struct {
	store                    *store.Store
	model                    *modelclient.Client
	sessionService           session.Service
	artifactService          artifact.Service
	modelAuth                *modelauth.Service
	nexusDock                *nexusdock.Service
	live                     *realtime.Hub
	logger                   *slog.Logger
	heartbeatInterval        time.Duration
	leaseDuration            time.Duration
	compactionTokenThreshold int
	compactionEventRetention int
}

func NewRunner(
	dataStore *store.Store,
	model *modelclient.Client,
	logger *slog.Logger,
	config RunnerConfig,
) *Runner {
	if model == nil {
		model = modelclient.New()
	}
	if logger == nil {
		logger = slog.Default()
	}
	if config.HeartbeatInterval <= 0 {
		config.HeartbeatInterval = 30 * time.Second
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = store.DefaultClaimLease
	}
	return &Runner{
		store:                    dataStore,
		model:                    model,
		logger:                   logger,
		sessionService:           config.SessionService,
		artifactService:          config.ArtifactService,
		modelAuth:                config.ModelAuth,
		nexusDock:                config.NexusDock,
		live:                     config.Live,
		heartbeatInterval:        config.HeartbeatInterval,
		leaseDuration:            config.LeaseDuration,
		compactionTokenThreshold: defaultCompactionTokenThreshold,
		compactionEventRetention: defaultCompactionEventRetention,
	}
}

func isRetryableModelRouteError(ctx context.Context, err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, modelauth.ErrUnavailable) ||
		errors.Is(err, modelauth.ErrNotConnected) ||
		errors.Is(err, modelauth.ErrReauthRequired) ||
		errors.Is(err, modelauth.ErrCredentialRefresh) ||
		errors.Is(err, modelclient.ErrMalformedResponse) ||
		errors.Is(err, modelclient.ErrEmptyResponse) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var apiErr *modelclient.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 401 ||
			// 402 在订阅型模型供应商中通常表示当前账号额度/计费不可用，
			// 属于 Route 自身故障；后续 Route 可能使用另一账号或供应商，应继续切换。
			apiErr.StatusCode == 402 ||
			apiErr.StatusCode == 403 ||
			apiErr.StatusCode == 404 ||
			apiErr.StatusCode == 408 ||
			apiErr.StatusCode == 429 ||
			apiErr.StatusCode >= 500
	}
	var networkErr net.Error
	return errors.As(err, &networkErr)
}

// 同路由只重试短暂的请求故障；认证、额度和路由不存在等问题直接交给备用路由。
func isRetryableModelRequestError(ctx context.Context, err error) bool {
	if !isRetryableModelRouteError(ctx, err) {
		return false
	}
	var apiErr *modelclient.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 408 || apiErr.StatusCode == 429 || apiErr.StatusCode >= 500
	}
	var networkErr net.Error
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, modelclient.ErrMalformedResponse) ||
		errors.Is(err, modelclient.ErrEmptyResponse) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.As(err, &networkErr)
}

func runnableAgentSettings(settings store.AgentSettings) bool {
	for _, route := range settings.Routes {
		if route.Enabled {
			return true
		}
	}
	return false
}

func secretFromEnv(name, label string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s environment variable %s is not set", label, name)
	}
	return value, nil
}
