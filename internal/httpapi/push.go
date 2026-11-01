package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/webpush"
)

type pushSender interface {
	PublicKey() string
	Send(ctx context.Context, endpoint, p256dh, auth, topic string, payload []byte) error
}

type pushPayload struct {
	Type        string `json:"type"`
	WorkspaceID string `json:"workspaceID"`
	CardID      string `json:"cardID"`
	Revision    string `json:"revision"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	URL         string `json:"url"`
}

func (api *API) getPushConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"public_key": api.pushPublicKey()})
}

func (api *API) pushPublicKey() string {
	if api.push == nil {
		return ""
	}
	return api.push.PublicKey()
}

type pushSubscriptionRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func (api *API) savePushSubscription(w http.ResponseWriter, r *http.Request) {
	if api.pushPublicKey() == "" {
		writeError(w, http.StatusServiceUnavailable, "push_unconfigured", "push is not configured")
		return
	}
	var request pushSubscriptionRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	endpoint, ok := normalizePushEndpoint(w, request.Endpoint)
	if !ok {
		return
	}
	if request.Keys.P256dh == "" || request.Keys.Auth == "" || len(request.Keys.P256dh) > 200 || len(request.Keys.Auth) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_subscription", "subscription keys are required")
		return
	}
	identity := identityFromRequest(r)
	err := api.store.SavePushSubscription(r.Context(), identity.WorkspaceID, identity.HumanAccountID, store.PushSubscription{
		Endpoint: endpoint, P256dh: request.Keys.P256dh, AuthSecret: request.Keys.Auth,
	})
	if err != nil {
		api.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type pushEndpointRequest struct {
	Endpoint string `json:"endpoint"`
}

func (api *API) deletePushSubscription(w http.ResponseWriter, r *http.Request) {
	var request pushEndpointRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	endpoint, ok := normalizePushEndpoint(w, request.Endpoint)
	if !ok {
		return
	}
	identity := identityFromRequest(r)
	if err := api.store.DeletePushSubscription(r.Context(), identity.WorkspaceID, identity.HumanAccountID, endpoint); err != nil {
		api.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) touchPushPresence(w http.ResponseWriter, r *http.Request) {
	api.setPushPresence(w, r, true)
}

func (api *API) idlePushSubscription(w http.ResponseWriter, r *http.Request) {
	api.setPushPresence(w, r, false)
}

func (api *API) setPushPresence(w http.ResponseWriter, r *http.Request, active bool) {
	var request pushEndpointRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	endpoint, ok := normalizePushEndpoint(w, request.Endpoint)
	if !ok {
		return
	}
	identity := identityFromRequest(r)
	if active {
		found, err := api.store.TouchPushSubscription(r.Context(), identity.WorkspaceID, identity.HumanAccountID, endpoint)
		if err != nil {
			api.handleError(w, err)
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "subscription_not_found", "push subscription was not found")
			return
		}
	} else if err := api.store.IdlePushSubscription(r.Context(), identity.WorkspaceID, identity.HumanAccountID, endpoint); err != nil {
		api.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func normalizePushEndpoint(w http.ResponseWriter, raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		writeError(w, http.StatusBadRequest, "invalid_subscription", "push endpoint must be an https URL")
		return "", false
	}
	if len(parsed.String()) > 2000 {
		writeError(w, http.StatusBadRequest, "invalid_subscription", "push endpoint is too long")
		return "", false
	}
	return parsed.String(), true
}

// DeliverPushes 在进程存活期间把已关闭页面的卡片变化推出去。
// 多个副本会各自推送；浏览器按卡片合并同一条未读通知。
func DeliverPushes(ctx context.Context, pool *pgxpool.Pool, dataStore *store.Store, sender pushSender, logger *slog.Logger) {
	if sender == nil || sender.PublicKey() == "" {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	api := &API{pool: pool, store: dataStore, push: sender, logger: logger}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := api.deliverPushes(ctx); err != nil && ctx.Err() == nil {
				logger.Error("push delivery failed", "error", err)
			}
		}
	}
}

func (api *API) deliverPushes(ctx context.Context) error {
	if api.push == nil || api.push.PublicKey() == "" {
		return nil
	}
	workspaces, err := api.store.ListPushWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, workspaceID := range workspaces {
		if err := api.deliverWorkspacePushes(ctx, workspaceID); err != nil && api.logger != nil {
			api.logger.Error("workspace push delivery failed", "workspace_id", workspaceID, "error", err)
		}
	}
	return nil
}

func (api *API) deliverWorkspacePushes(ctx context.Context, workspaceID uuid.UUID) error {
	updates, err := api.cardUpdates(ctx, workspaceID)
	if err != nil {
		return err
	}
	snapshots, err := api.store.ListPushCardSnapshots(ctx, workspaceID)
	if err != nil {
		return err
	}
	known := make(map[uuid.UUID]store.PushCardSnapshot, len(snapshots))
	for _, snapshot := range snapshots {
		known[snapshot.CardID] = snapshot
	}
	idle, err := api.store.ListIdlePushSubscriptions(ctx, workspaceID)
	if err != nil {
		return err
	}
	for _, update := range updates {
		cardID, err := uuid.Parse(update.ID)
		if err != nil {
			continue
		}
		previous, seen := known[cardID]
		if seen && previous.Revision == update.Revision {
			continue
		}
		// 第一次见到只建立基线。页面还开着时不记版本，等页面确认看过；
		// 否则标签刚关掉、心跳还没过期时，这次更新会被记成已读，之后再也推不出去。
		if !seen {
			if err := api.savePushSnapshot(ctx, workspaceID, cardID, update); err != nil {
				return err
			}
			continue
		}
		// 置顶、改标题、日程和执行状态都会改 revision，但不是新消息。记下来，关闭页面后也不补推。
		if !newCardMessage(update, previous) {
			if err := api.savePushSnapshot(ctx, workspaceID, cardID, update); err != nil {
				return err
			}
			continue
		}
		if len(idle) == 0 {
			continue
		}
		if err := api.sendCardPush(ctx, workspaceID, update, idle); err != nil {
			// 保留旧版本，下一轮重试。已经退订的浏览器会在发送时被删掉。
			return err
		}
		if err := api.savePushSnapshot(ctx, workspaceID, cardID, update); err != nil {
			return err
		}
	}
	return nil
}

func (api *API) savePushSnapshot(ctx context.Context, workspaceID, cardID uuid.UUID, update cardUpdate) error {
	return api.store.SavePushCardSnapshot(ctx, workspaceID, store.PushCardSnapshot{
		CardID: cardID, Revision: update.Revision, EventSequence: update.EventSequence,
		MessagePreview: update.MessagePreview, AttemptStatus: update.AttemptStatus, Title: update.Title,
	})
}

type pushSeenRequest struct {
	Cards []struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	} `json:"cards"`
}

func (api *API) ackPushCards(w http.ResponseWriter, r *http.Request) {
	var request pushSeenRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	if len(request.Cards) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_revision", "too many cards")
		return
	}
	identity := identityFromRequest(r)
	updates, err := api.cardUpdates(r.Context(), identity.WorkspaceID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	byID := make(map[string]cardUpdate, len(updates))
	for _, update := range updates {
		byID[update.ID] = update
	}
	for _, card := range request.Cards {
		update, ok := byID[card.ID]
		if !ok || update.Revision != card.Revision {
			continue
		}
		cardID, err := uuid.Parse(card.ID)
		if err != nil {
			continue
		}
		if err := api.savePushSnapshot(r.Context(), identity.WorkspaceID, cardID, update); err != nil {
			api.handleError(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (api *API) sendCardPush(ctx context.Context, workspaceID uuid.UUID, update cardUpdate, subs []store.PushSubscription) error {
	title := update.Title
	if title == "" {
		title = "新消息"
	}
	section := "cards"
	if update.Kind == "discussion" {
		section = "chat"
	}
	payload, err := json.Marshal(pushPayload{
		Type: "card.updated", WorkspaceID: workspaceID.String(), CardID: update.ID, Revision: update.Revision,
		Title: title, Body: cardPushBody(update),
		URL: "/" + section + "/" + url.PathEscape(update.ProjectID) + "/" + url.PathEscape(update.ID),
	})
	if err != nil {
		return err
	}
	topic := strings.ReplaceAll(update.ID, "-", "")
	var sendErr error
	for _, sub := range subs {
		err := api.push.Send(ctx, sub.Endpoint, sub.P256dh, sub.AuthSecret, topic, payload)
		if errors.Is(err, webpush.ErrGone) {
			if deleteErr := api.store.DeletePushSubscriptionByEndpoint(ctx, sub.Endpoint); deleteErr != nil && sendErr == nil {
				sendErr = deleteErr
			}
			continue
		}
		if err != nil && sendErr == nil {
			sendErr = err
		}
	}
	return sendErr
}

func newCardMessage(current cardUpdate, previous store.PushCardSnapshot) bool {
	return current.EventSequence != previous.EventSequence && current.MessagePreview != ""
}

func cardPushBody(current cardUpdate) string {
	return current.MessagePreview
}
