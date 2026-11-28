package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/uvwt/CycleDock/internal/agentsession"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/oauth"
	"github.com/uvwt/CycleDock/internal/store"
)

const serverInstructions = "Use CycleDock as shared durable work state. A Card owns a persistent Agent Session. New work direction is a Turn; one real execution is an Attempt. When continuing work on a Card, default to get_card and get_card_session(mode=simple) for clean conversation history; use get_card_session(mode=full) or search_card_session only when you need to verify execution details, inspect tool calls/results, or troubleshoot prior turns. New work starts as a Discussion; only generate_work may derive a Todo or Ongoing Card from an active Discussion. Todo/Ongoing Cards never generate more Cards. Use list_schedules and create_schedule/update_schedule/delete_schedule to manage recurring times on Ongoing Cards; schedules only enqueue Turns. Create or claim Turns, report durable findings/results/check-ins through the owned Attempt, and use confirm_card to move verified Working Todo work to Review. Only a human can move Review to Done."

type Handlers struct {
	MCP                       http.Handler
	ProtectedResourceMetadata http.Handler
}

type Actor struct {
	WorkspaceID uuid.UUID
	SourceID    uuid.UUID
}

func New(dataStore *store.Store, sessionService *agentsession.Service, oauthService *oauth.Service, logger *slog.Logger) Handlers {
	if logger == nil {
		logger = slog.Default()
	}
	server := mcp.NewServer(&mcp.Implementation{
		Name:        "cycledock",
		Title:       "CycleDock",
		Description: "Shared project and task state for humans and AI agents.",
		Version:     "0.4.0",
	}, &mcp.ServerOptions{
		Instructions: serverInstructions,
	})
	registerTools(server, dataStore, sessionService)

	streamable := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{
			// 外部 MCP 客户端在完成 OAuth 后并发/无状态发起初始化与工具调用；
			// 统一采用无状态 JSON 响应，避免内存会话与 SSE 流经 CDN/反代时失效，并对齐 Gemini 等客户端习惯。
			Stateless:                    true,
			JSONResponse:                 true,
			PropagateRequestCancellation: true,
			// 公网 HTTPS 入口经 EdgeOne/Caddy 反向代理回源到本机 loopback，Host 仍保持公网域名；
			// 仅公网 HTTPS 入口关闭 go-sdk 的 localhost DNS-rebinding Host 检查，本地 HTTP 直连继续保留默认保护。
			DisableLocalhostProtection: strings.HasPrefix(oauthService.Issuer(), "https://"),
			CrossOriginProtection:      &http.CrossOriginProtection{},
		},
	)

	resourceMetadataURL := oauthService.Issuer() + "/.well-known/oauth-protected-resource/mcp"
	protected := mcpauth.RequireBearerToken(
		func(ctx context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
			identity, err := oauthService.AuthenticateAccessToken(ctx, token)
			if err != nil {
				if !errors.Is(err, oauth.ErrInvalidToken) {
					logger.Error("verify MCP access token", "error", err)
				}
				return nil, fmt.Errorf("%w: invalid access token", mcpauth.ErrInvalidToken)
			}
			return &mcpauth.TokenInfo{
				Scopes:     []string{oauth.ScopeMCP},
				Expiration: identity.ExpiresAt,
				UserID:     identity.WorkspaceID.String() + "/" + identity.SourceID.String(),
			}, nil
		},
		&mcpauth.RequireBearerTokenOptions{
			ResourceMetadataURL: resourceMetadataURL,
			Scopes:              []string{oauth.ScopeMCP},
		},
	)(streamable)

	metadata := mcpauth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
		Resource:               oauthService.Resource(),
		AuthorizationServers:   []string{oauthService.Issuer()},
		ScopesSupported:        []string{oauth.ScopeMCP},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "CycleDock MCP",
	})

	return Handlers{MCP: protected, ProtectedResourceMetadata: metadata}
}

func actorFromContext(ctx context.Context) (Actor, error) {
	tokenInfo := mcpauth.TokenInfoFromContext(ctx)
	if tokenInfo == nil {
		return Actor{}, errors.New("AUTH_REQUIRED: authenticated MCP identity is missing")
	}
	workspaceRaw, sourceRaw, ok := strings.Cut(tokenInfo.UserID, "/")
	if !ok {
		return Actor{}, errors.New("AUTH_REQUIRED: authenticated MCP identity is invalid")
	}
	workspaceID, err := uuid.Parse(workspaceRaw)
	if err != nil {
		return Actor{}, errors.New("AUTH_REQUIRED: authenticated workspace is invalid")
	}
	sourceID, err := uuid.Parse(sourceRaw)
	if err != nil {
		return Actor{}, errors.New("AUTH_REQUIRED: authenticated source is invalid")
	}
	return Actor{WorkspaceID: workspaceID, SourceID: sourceID}, nil
}

func parseUUID(raw, field string) (uuid.UUID, error) {
	value, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil, fmt.Errorf("INVALID_REQUEST: %s must be a UUID", field)
	}
	return value, nil
}

func stableToolError(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return errors.New("NOT_FOUND: resource is not accessible in this workspace")
	case errors.Is(err, domain.ErrIdempotencyMismatch):
		return errors.New("IDEMPOTENCY_CONFLICT: idempotency_key was reused for a different request")
	case errors.Is(err, domain.ErrAttemptNotCurrent):
		return errors.New("ATTEMPT_NOT_CURRENT: attempt is no longer current or is owned by another source")
	case errors.Is(err, domain.ErrHumanRequired):
		return errors.New("HUMAN_REQUIRED: this action is reserved for a human")
	case errors.Is(err, domain.ErrInvalidTransition):
		return errors.New("INVALID_TRANSITION: the requested state transition is not allowed")
	case errors.Is(err, domain.ErrConflict):
		return errors.New("CONFLICT: CycleDock state changed or current work is already claimed")
	default:
		slog.Error("MCP tool error", "error", err)
		return errors.New("INTERNAL_ERROR: CycleDock could not complete the operation")
	}
}
