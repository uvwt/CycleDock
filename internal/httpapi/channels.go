package httpapi

import (
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
	ID                uuid.UUID                   `json:"id"`
	WorkspaceID       uuid.UUID                   `json:"workspace_id"`
	Name              string                      `json:"name"`
	Platform          domain.ChannelPlatform      `json:"platform"`
	Role              domain.ChannelRole          `json:"role"`
	Transport         domain.ChannelTransport     `json:"transport"`
	Enabled           bool                        `json:"enabled"`
	ProjectID         uuid.UUID                   `json:"project_id"`
	TargetCardID      *uuid.UUID                  `json:"target_card_id,omitempty"`
	PushFilter        domain.ChannelPushFilter    `json:"push_filter"`
	MirrorWebMessages bool                        `json:"mirror_web_messages"`
	GroupPolicy       domain.ChannelGroupPolicy   `json:"group_policy"`
	AllowFrom         []string                    `json:"allow_from"`
	Config            store.RemoteChannelConfig   `json:"config"`
	HasSecrets        bool                        `json:"has_secrets"`
	SecretHints       map[string]string           `json:"secret_hints"`
	RuntimeStatus     domain.ChannelRuntimeStatus `json:"runtime_status"`
	LastError         string                      `json:"last_error,omitempty"`
	LastHeartbeatAt   *time.Time                  `json:"last_heartbeat_at,omitempty"`
	LastInboundAt     *time.Time                  `json:"last_inbound_at,omitempty"`
	LastOutboundAt    *time.Time                  `json:"last_outbound_at,omitempty"`
	CreatedAt         time.Time                   `json:"created_at"`
	UpdatedAt         time.Time                   `json:"updated_at"`
}

func toRemoteChannelDTO(ch store.RemoteChannel) remoteChannelDTO {
	var targetCardID *uuid.UUID
	if ch.TargetCardID.Valid {
		id := ch.TargetCardID.UUID
		targetCardID = &id
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
		ID:                ch.ID,
		WorkspaceID:       ch.WorkspaceID,
		Name:              ch.Name,
		Platform:          ch.Platform,
		Role:              ch.Role,
		Transport:         ch.Transport,
		Enabled:           ch.Enabled,
		ProjectID:         ch.ProjectID,
		TargetCardID:      targetCardID,
		PushFilter:        ch.PushFilter,
		MirrorWebMessages: ch.MirrorWebMessages,
		GroupPolicy:       ch.GroupPolicy,
		AllowFrom:         allowFrom,
		Config:            ch.Config,
		HasSecrets:        ch.HasSecrets(),
		SecretHints:       hints,
		RuntimeStatus:     ch.RuntimeStatus,
		LastError:         ch.LastError,
		LastHeartbeatAt:   ch.LastHeartbeatAt,
		LastInboundAt:     ch.LastInboundAt,
		LastOutboundAt:    ch.LastOutboundAt,
		CreatedAt:         ch.CreatedAt,
		UpdatedAt:         ch.UpdatedAt,
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
