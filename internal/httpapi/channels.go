package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/channels"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
)

type remoteChannelDTO struct {
	ID                 uuid.UUID                   `json:"id"`
	WorkspaceID        uuid.UUID                   `json:"workspace_id"`
	Name               string                      `json:"name"`
	Platform           domain.ChannelPlatform      `json:"platform"`
	Role               domain.ChannelRole          `json:"role"`
	Transport          domain.ChannelTransport     `json:"transport"`
	Enabled            bool                        `json:"enabled"`
	ProjectID          uuid.UUID                   `json:"project_id"`
	TargetCardID       *uuid.UUID                  `json:"target_card_id,omitempty"`
	ConversationCardID *uuid.UUID                  `json:"conversation_card_id,omitempty"`
	PushFilter         domain.ChannelPushFilter    `json:"push_filter"`
	MirrorWebMessages  bool                        `json:"mirror_web_messages"`
	GroupPolicy        domain.ChannelGroupPolicy   `json:"group_policy"`
	AllowFrom          []string                    `json:"allow_from"`
	Config             store.RemoteChannelConfig   `json:"config"`
	HasSecrets         bool                        `json:"has_secrets"`
	SecretHints        map[string]string           `json:"secret_hints"`
	RuntimeStatus      domain.ChannelRuntimeStatus `json:"runtime_status"`
	LastError          string                      `json:"last_error,omitempty"`
	LastHeartbeatAt    *time.Time                  `json:"last_heartbeat_at,omitempty"`
	LastInboundAt      *time.Time                  `json:"last_inbound_at,omitempty"`
	LastOutboundAt     *time.Time                  `json:"last_outbound_at,omitempty"`
	CreatedAt          time.Time                   `json:"created_at"`
	UpdatedAt          time.Time                   `json:"updated_at"`
}

func toRemoteChannelDTO(ch store.RemoteChannel) remoteChannelDTO {
	var targetCardID *uuid.UUID
	if ch.TargetCardID.Valid {
		id := ch.TargetCardID.UUID
		targetCardID = &id
	}
	var conversationCardID *uuid.UUID
	if ch.ConversationCardID.Valid {
		id := ch.ConversationCardID.UUID
		conversationCardID = &id
	}
	allowFrom := ch.AllowFrom
	if allowFrom == nil {
		allowFrom = []string{}
	}
	hints := ch.SecretHints
	if hints == nil {
		hints = map[string]string{}
	}
	return remoteChannelDTO{
		ID:                 ch.ID,
		WorkspaceID:        ch.WorkspaceID,
		Name:               ch.Name,
		Platform:           ch.Platform,
		Role:               ch.Role,
		Transport:          ch.Transport,
		Enabled:            ch.Enabled,
		ProjectID:          ch.ProjectID,
		TargetCardID:       targetCardID,
		ConversationCardID: conversationCardID,
		PushFilter:         ch.PushFilter,
		MirrorWebMessages:  ch.MirrorWebMessages,
		GroupPolicy:        ch.GroupPolicy,
		AllowFrom:          allowFrom,
		Config:             ch.Config,
		HasSecrets:         ch.HasSecrets(),
		SecretHints:        hints,
		RuntimeStatus:      ch.RuntimeStatus,
		LastError:          ch.LastError,
		LastHeartbeatAt:    ch.LastHeartbeatAt,
		LastInboundAt:      ch.LastInboundAt,
		LastOutboundAt:     ch.LastOutboundAt,
		CreatedAt:          ch.CreatedAt,
		UpdatedAt:          ch.UpdatedAt,
	}
}

type remoteChannelUpsertRequest struct {
	Name              string                     `json:"name"`
	Platform          domain.ChannelPlatform     `json:"platform"`
	Role              domain.ChannelRole         `json:"role"`
	Transport         domain.ChannelTransport    `json:"transport"`
	Enabled           *bool                      `json:"enabled"`
	ProjectID         string                     `json:"project_id"`
	TargetCardID      *string                    `json:"target_card_id"`
	PushFilter        domain.ChannelPushFilter   `json:"push_filter"`
	MirrorWebMessages bool                       `json:"mirror_web_messages"`
	GroupPolicy       domain.ChannelGroupPolicy  `json:"group_policy"`
	AllowFrom         []string                   `json:"allow_from"`
	Config            store.RemoteChannelConfig  `json:"config"`
	Secrets           store.RemoteChannelSecrets `json:"secrets"`
	ClearSecrets      bool                       `json:"clear_secrets"`
}

func parseRemoteChannelUpsertRequest(w http.ResponseWriter, r *http.Request) (store.RemoteChannelInput, bool) {
	var req remoteChannelUpsertRequest
	if !decodeJSON(w, r, &req) {
		return store.RemoteChannelInput{}, false
	}
	projectID, err := uuid.Parse(strings.TrimSpace(req.ProjectID))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "valid project_id is required")
		return store.RemoteChannelInput{}, false
	}
	var targetCardID *uuid.UUID
	if req.TargetCardID != nil && strings.TrimSpace(*req.TargetCardID) != "" {
		parsed, err := uuid.Parse(strings.TrimSpace(*req.TargetCardID))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid target_card_id")
			return store.RemoteChannelInput{}, false
		}
		targetCardID = &parsed
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	return store.RemoteChannelInput{
		Name:              req.Name,
		Platform:          req.Platform,
		Role:              req.Role,
		Transport:         req.Transport,
		Enabled:           enabled,
		ProjectID:         projectID,
		TargetCardID:      targetCardID,
		PushFilter:        req.PushFilter,
		MirrorWebMessages: req.MirrorWebMessages,
		GroupPolicy:       req.GroupPolicy,
		AllowFrom:         req.AllowFrom,
		Config:            req.Config,
		Secrets:           req.Secrets,
		ClearSecrets:      req.ClearSecrets,
	}, true
}

func (a *API) listRemoteChannels(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	list, err := a.store.ListRemoteChannels(r.Context(), session.WorkspaceID)
	if err != nil {
		a.handleError(w, err)
		return
	}
	items := make([]remoteChannelDTO, 0, len(list))
	for _, ch := range list {
		items = append(items, toRemoteChannelDTO(ch))
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": items})
}

func (a *API) createRemoteChannel(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	input, ok := parseRemoteChannelUpsertRequest(w, r)
	if !ok {
		return
	}
	created, err := a.store.CreateRemoteChannel(r.Context(), session.WorkspaceID, session.SourceID, input)
	if err != nil {
		a.handleError(w, err)
		return
	}
	if a.channelManager != nil {
		_ = a.channelManager.ReloadChannel(r.Context(), session.WorkspaceID, created.ID)
		if refreshed, err := a.store.GetRemoteChannel(r.Context(), session.WorkspaceID, created.ID); err == nil {
			created = refreshed
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"channel": toRemoteChannelDTO(created)})
}

func (a *API) updateRemoteChannel(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	channelID, ok := parseUUIDParam(w, r, "channelID")
	if !ok {
		return
	}
	input, ok := parseRemoteChannelUpsertRequest(w, r)
	if !ok {
		return
	}
	updated, err := a.store.UpdateRemoteChannel(r.Context(), session.WorkspaceID, channelID, input)
	if err != nil {
		a.handleError(w, err)
		return
	}
	if a.channelManager != nil {
		_ = a.channelManager.ReloadChannel(r.Context(), session.WorkspaceID, updated.ID)
		if refreshed, err := a.store.GetRemoteChannel(r.Context(), session.WorkspaceID, updated.ID); err == nil {
			updated = refreshed
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel": toRemoteChannelDTO(updated)})
}

func (a *API) toggleRemoteChannel(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	channelID, ok := parseUUIDParam(w, r, "channelID")
	if !ok {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	updated, err := a.store.SetRemoteChannelEnabled(r.Context(), session.WorkspaceID, channelID, req.Enabled)
	if err != nil {
		a.handleError(w, err)
		return
	}
	if a.channelManager != nil {
		_ = a.channelManager.ReloadChannel(r.Context(), session.WorkspaceID, updated.ID)
		if refreshed, err := a.store.GetRemoteChannel(r.Context(), session.WorkspaceID, updated.ID); err == nil {
			updated = refreshed
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"channel": toRemoteChannelDTO(updated)})
}

func (a *API) deleteRemoteChannel(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	channelID, ok := parseUUIDParam(w, r, "channelID")
	if !ok {
		return
	}
	if err := a.store.DeleteRemoteChannel(r.Context(), session.WorkspaceID, channelID); err != nil {
		a.handleError(w, err)
		return
	}
	if a.channelManager != nil {
		a.channelManager.StopChannel(channelID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": channelID})
}

func (a *API) testRemoteChannel(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	channelID, ok := parseUUIDParam(w, r, "channelID")
	if !ok {
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	mgr := a.channelManager
	if mgr == nil {
		mgr = channels.NewManager(a.store, a.logger, a.publicBaseURL)
	}
	if err := mgr.TestChannel(r.Context(), session.WorkspaceID, channelID, req.Message); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			a.handleError(w, err)
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"ok":      false,
			"message": err.Error(),
		})
		return
	}
	ch, _ := a.store.GetRemoteChannel(r.Context(), session.WorkspaceID, channelID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "测试连接与发送成功",
		"channel": toRemoteChannelDTO(ch),
	})
}

func (a *API) startWeixinLogin(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	var req struct {
		ChannelID string `json:"channel_id"`
		BaseURL   string `json:"base_url"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}
	var channelID uuid.UUID
	if strings.TrimSpace(req.ChannelID) != "" {
		parsed, err := uuid.Parse(strings.TrimSpace(req.ChannelID))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid channel_id")
			return
		}
		ch, err := a.store.GetRemoteChannel(r.Context(), session.WorkspaceID, parsed)
		if err != nil {
			a.handleError(w, err)
			return
		}
		if ch.Platform != domain.ChannelPlatformWeixin {
			writeError(w, http.StatusConflict, "conflict", "only weixin channels support QR login")
			return
		}
		channelID = ch.ID
		if strings.TrimSpace(req.BaseURL) == "" {
			req.BaseURL = ch.Config.BaseURL
		}
	}
	loginStore := a.weixinLoginStore()
	sess, err := loginStore.Start(r.Context(), session.WorkspaceID, channelID, req.BaseURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "weixin_qr_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session": sess})
}

func (a *API) pollWeixinLogin(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	sessionID := strings.TrimSpace(chi.URLParam(r, "sessionID"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "session_id is required")
		return
	}
	verifyCode := strings.TrimSpace(r.URL.Query().Get("verify_code"))
	loginStore := a.weixinLoginStore()
	sess, err := loginStore.Poll(r.Context(), session.WorkspaceID, sessionID, verifyCode)
	if err != nil {
		a.handleError(w, err)
		return
	}
	var updatedDTO *remoteChannelDTO
	if sess.Status == "confirmed" && sess.ChannelID != uuid.Nil && strings.TrimSpace(sess.BotToken) != "" {
		updated, err := a.store.UpdateRemoteChannelSecretsAndConfig(
			r.Context(),
			session.WorkspaceID,
			sess.ChannelID,
			store.RemoteChannelSecrets{BotToken: sess.BotToken},
			func(cfg *store.RemoteChannelConfig) {
				if sess.BaseURL != "" {
					cfg.BaseURL = sess.BaseURL
				}
				if sess.BotID != "" {
					cfg.BotID = sess.BotID
				}
				if sess.WeixinUserID != "" {
					cfg.WeixinUserID = sess.WeixinUserID
					if cfg.TargetChatID == "" {
						cfg.TargetChatID = sess.WeixinUserID
					}
				}
			},
		)
		if err != nil {
			a.handleError(w, err)
			return
		}
		if a.channelManager != nil {
			_ = a.channelManager.ReloadChannel(r.Context(), session.WorkspaceID, updated.ID)
			if refreshed, err := a.store.GetRemoteChannel(r.Context(), session.WorkspaceID, updated.ID); err == nil {
				updated = refreshed
			}
		}
		dto := toRemoteChannelDTO(updated)
		updatedDTO = &dto
	}
	resp := map[string]any{"session": sess}
	if updatedDTO != nil {
		resp["channel"] = updatedDTO
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) weixinLoginStore() *channels.WeixinLoginStore {
	if a.channelManager != nil {
		return a.channelManager.WeixinLoginStore()
	}
	if a.fallbackWeixinAuth == nil {
		a.fallbackWeixinAuth = channels.NewWeixinLoginStore(nil, "")
	}
	return a.fallbackWeixinAuth
}

func (a *API) qrConnectStore() *channels.QRConnectStore {
	if a.channelManager != nil {
		return a.channelManager.QRConnectStore()
	}
	if a.fallbackQRAuth == nil {
		a.fallbackQRAuth = channels.NewQRConnectStore(nil, a.weixinLoginStore())
	}
	return a.fallbackQRAuth
}

func defaultQRChannelName(platform domain.ChannelPlatform, role domain.ChannelRole) string {
	label := string(platform)
	switch platform {
	case domain.ChannelPlatformWeixin:
		label = "微信"
	case domain.ChannelPlatformWeCom:
		label = "企业微信"
	case domain.ChannelPlatformDingTalk:
		label = "钉钉"
	case domain.ChannelPlatformFeishu:
		label = "飞书"
	case domain.ChannelPlatformQQ:
		label = "QQ 机器人"
	case domain.ChannelPlatformTelegram:
		label = "Telegram"
	}
	if role == domain.ChannelRoleScheduledPush {
		return label + " 定时推送"
	}
	return label
}

func (a *API) startChannelQRConnect(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	var req struct {
		ChannelID string                 `json:"channel_id"`
		Platform  domain.ChannelPlatform `json:"platform"`
		ProjectID string                 `json:"project_id"`
		Role      domain.ChannelRole     `json:"role"`
		Name      string                 `json:"name"`
		Domain    string                 `json:"domain"`
		BaseURL   string                 `json:"base_url"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeJSON(w, r, &req) {
			return
		}
	}

	var channelID uuid.UUID
	var projectID uuid.UUID
	platform := req.Platform
	role := req.Role
	name := strings.TrimSpace(req.Name)
	domainVal := strings.TrimSpace(req.Domain)
	baseURL := strings.TrimSpace(req.BaseURL)

	if strings.TrimSpace(req.ChannelID) != "" {
		parsed, err := uuid.Parse(strings.TrimSpace(req.ChannelID))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid channel_id")
			return
		}
		ch, err := a.store.GetRemoteChannel(r.Context(), session.WorkspaceID, parsed)
		if err != nil {
			a.handleError(w, err)
			return
		}
		if platform == "" {
			platform = ch.Platform
		} else if platform != ch.Platform {
			writeError(w, http.StatusConflict, "conflict", "platform does not match existing channel")
			return
		}
		channelID = ch.ID
		projectID = ch.ProjectID
		role = ch.Role
		if name == "" {
			name = ch.Name
		}
		if domainVal == "" {
			domainVal = ch.Config.Domain
		}
		if baseURL == "" {
			baseURL = ch.Config.BaseURL
		}
	} else {
		if platform == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "platform or channel_id is required")
			return
		}
		parsedProj, err := uuid.Parse(strings.TrimSpace(req.ProjectID))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "valid project_id is required when creating channel by QR")
			return
		}
		if _, err := a.store.GetProject(r.Context(), session.WorkspaceID, parsedProj); err != nil {
			a.handleError(w, err)
			return
		}
		projectID = parsedProj
		if role == "" {
			role = domain.ChannelRoleMainChat
		}
		if name == "" {
			name = defaultQRChannelName(platform, role)
		}
	}

	qrStore := a.qrConnectStore()
	sess, err := qrStore.Start(r.Context(), channels.QRConnectStartInput{
		WorkspaceID: session.WorkspaceID,
		ChannelID:   channelID,
		Platform:    platform,
		ProjectID:   projectID,
		Role:        role,
		Name:        name,
		Domain:      domainVal,
		BaseURL:     baseURL,
	})
	if err != nil {
		if errors.Is(err, domain.ErrConflict) {
			a.handleError(w, err)
			return
		}
		writeError(w, http.StatusBadGateway, "channel_qr_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session": sess})
}

func (a *API) pollChannelQRConnect(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	sessionID := strings.TrimSpace(chi.URLParam(r, "sessionID"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "session_id is required")
		return
	}
	verifyCode := strings.TrimSpace(r.URL.Query().Get("verify_code"))
	qrStore := a.qrConnectStore()
	sess, err := qrStore.Poll(r.Context(), session.WorkspaceID, sessionID, verifyCode)
	if err != nil {
		a.handleError(w, err)
		return
	}

	var channelDTO *remoteChannelDTO
	if sess.Status == "confirmed" {
		if sess.Committed && sess.ChannelID != uuid.Nil {
			if existing, err := a.store.GetRemoteChannel(r.Context(), session.WorkspaceID, sess.ChannelID); err == nil {
				dto := toRemoteChannelDTO(existing)
				channelDTO = &dto
			}
		} else {
			ch, err := a.commitQRConnectChannel(r.Context(), session.WorkspaceID, session.SourceID, sess)
			if err != nil {
				a.handleError(w, err)
				return
			}
			qrStore.MarkCommitted(sess.SessionID, ch.ID)
			sess.ChannelID = ch.ID
			if a.channelManager != nil {
				_ = a.channelManager.ReloadChannel(r.Context(), session.WorkspaceID, ch.ID)
				if refreshed, err := a.store.GetRemoteChannel(r.Context(), session.WorkspaceID, ch.ID); err == nil {
					ch = refreshed
				}
			}
			dto := toRemoteChannelDTO(ch)
			channelDTO = &dto
		}
	}

	resp := map[string]any{"session": sess}
	if channelDTO != nil {
		resp["channel"] = channelDTO
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) cancelChannelQRConnect(w http.ResponseWriter, r *http.Request) {
	session := identityFromRequest(r)
	sessionID := strings.TrimSpace(chi.URLParam(r, "sessionID"))
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "session_id is required")
		return
	}
	sess, err := a.qrConnectStore().Cancel(session.WorkspaceID, sessionID)
	if err != nil {
		a.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": sess})
}

func (a *API) commitQRConnectChannel(
	ctx context.Context,
	workspaceID, sourceID uuid.UUID,
	sess *channels.QRConnectSession,
) (store.RemoteChannel, error) {
	secrets, mutateCfg := buildQRCredentials(sess)

	// 已有通道：更新加密凭据与非敏感配置，并确保通道处于启用状态以便立即启动长连接。
	if sess.ChannelID != uuid.Nil {
		updated, err := a.store.UpdateRemoteChannelSecretsAndConfig(
			ctx,
			workspaceID,
			sess.ChannelID,
			secrets,
			mutateCfg,
		)
		if err != nil {
			return store.RemoteChannel{}, err
		}
		if !updated.Enabled {
			if enabledCh, err := a.store.SetRemoteChannelEnabled(ctx, workspaceID, updated.ID, true); err == nil {
				updated = enabledCh
			}
		}
		return updated, nil
	}

	// 新建通道：服务端自动推导最优长连接传输模式、补齐默认配置并加密落库。
	role := sess.Role
	if role == "" {
		role = domain.ChannelRoleMainChat
	}
	name := strings.TrimSpace(sess.Name)
	if name == "" {
		name = defaultQRChannelName(sess.Platform, role)
	}
	var cfg store.RemoteChannelConfig
	mutateCfg(&cfg)
	return a.store.CreateRemoteChannel(ctx, workspaceID, sourceID, store.RemoteChannelInput{
		Name:              name,
		Platform:          sess.Platform,
		Role:              role,
		Enabled:           true,
		ProjectID:         sess.ProjectID,
		PushFilter:        domain.ChannelPushFilterAll,
		MirrorWebMessages: false,
		GroupPolicy:       domain.ChannelGroupPolicyMentionOnly,
		Config:            cfg,
		Secrets:           secrets,
	})
}

func buildQRCredentials(sess *channels.QRConnectSession) (store.RemoteChannelSecrets, func(*store.RemoteChannelConfig)) {
	switch sess.Platform {
	case domain.ChannelPlatformWeixin:
		return store.RemoteChannelSecrets{BotToken: sess.BotToken}, func(cfg *store.RemoteChannelConfig) {
			if sess.BaseURL != "" {
				cfg.BaseURL = sess.BaseURL
			}
			if sess.BotID != "" {
				cfg.BotID = sess.BotID
			}
			if sess.WeixinUserID != "" {
				cfg.WeixinUserID = sess.WeixinUserID
				if cfg.TargetChatID == "" {
					cfg.TargetChatID = sess.WeixinUserID
				}
			}
		}

	case domain.ChannelPlatformWeCom:
		return store.RemoteChannelSecrets{Secret: sess.Secret}, func(cfg *store.RemoteChannelConfig) {
			if sess.BotID != "" {
				cfg.BotID = sess.BotID
			}
		}

	case domain.ChannelPlatformDingTalk:
		return store.RemoteChannelSecrets{ClientSecret: sess.ClientSecret}, func(cfg *store.RemoteChannelConfig) {
			if sess.ClientID != "" {
				cfg.ClientID = sess.ClientID
			}
		}

	case domain.ChannelPlatformFeishu:
		return store.RemoteChannelSecrets{AppSecret: sess.AppSecret}, func(cfg *store.RemoteChannelConfig) {
			if sess.Domain != "" {
				cfg.Domain = sess.Domain
			}
			if sess.AppID != "" {
				cfg.AppID = sess.AppID
			}
			if sess.OpenID != "" && cfg.TargetChatID == "" {
				cfg.TargetChatID = sess.OpenID
				if cfg.TargetChatType == "" {
					cfg.TargetChatType = "direct"
				}
			}
		}

	case domain.ChannelPlatformQQ:
		return store.RemoteChannelSecrets{Secret: sess.Secret}, func(cfg *store.RemoteChannelConfig) {
			if sess.AppID != "" {
				cfg.AppID = sess.AppID
			}
		}

	default:
		return store.RemoteChannelSecrets{}, func(*store.RemoteChannelConfig) {}
	}
}
