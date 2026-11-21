package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/uvwt/CycleDock/internal/realtime"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/webpush"
)

const (
	maxPushTitleRunes    = 80
	maxPushBodyRunes     = 240
	singlePushTimeout    = 15 * time.Second
	maxConcurrentPushes  = 8
	pushFallbackInterval = 15 * time.Second
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
	Tag         string `json:"tag"`
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
	subID, err := api.store.SavePushSubscription(r.Context(), identity.WorkspaceID, identity.HumanAccountID, store.PushSubscription{
		Endpoint: endpoint, P256dh: request.Keys.P256dh, AuthSecret: request.Keys.Auth,
	})
	if err != nil {
		api.handleError(w, err)
		return
	}
	// 新订阅（或切换到新工作区后尚无快照）立即用当前工作区快照建立基线，
	// 防止新设备刚订阅时把历史卡片一次性轰炸推送，同时确保订阅之后的下一条消息立刻可推。
	existing, err := api.store.ListPushSubscriptionCardSnapshots(r.Context(), identity.WorkspaceID, subID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	if len(existing) == 0 {
		updates, err := api.cardUpdates(r.Context(), identity.WorkspaceID)
		if err != nil {
			api.handleError(w, err)
			return
		}
		baseline := make([]store.PushCardSnapshot, 0, len(updates))
		for _, update := range updates {
			cardID, err := uuid.Parse(update.ID)
			if err != nil {
				continue
			}
			baseline = append(baseline, cardUpdateToPushSnapshot(subID, cardID, update))
		}
		if err := api.store.SavePushSubscriptionCardSnapshotsBatch(r.Context(), identity.WorkspaceID, subID, baseline); err != nil {
			api.handleError(w, err)
			return
		}
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

// DeliverPushes 在进程存活期间把已切后台或关闭页面的卡片变化推出去。
// 优先由 realtime.Hub 在消息/审批/状态落库后零延迟唤醒并只扫目标工作区；
// 同时保留低频全量兜底轮询，覆盖心跳过期、跨副本写入和上次失败重试。
func DeliverPushes(ctx context.Context, pool *pgxpool.Pool, dataStore *store.Store, sender pushSender, live *realtime.Hub, logger *slog.Logger) {
	if sender == nil || sender.PublicKey() == "" {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	api := &API{pool: pool, store: dataStore, push: sender, live: live, logger: logger}

	var wake <-chan uuid.UUID
	if live != nil {
		var unsubscribe func()
		wake, unsubscribe = live.SubscribeAllWorkspaces()
		defer unsubscribe()
	}

	interval := 3 * time.Second
	if wake != nil {
		interval = pushFallbackInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case workspaceID, ok := <-wake:
			if !ok {
				return
			}
			// 合并瞬间连续写入（如一次回复 + 状态更新），避免同一毫秒对同一工作区重复查询。
			pending := map[uuid.UUID]struct{}{workspaceID: {}}
		drain:
			for {
				select {
				case nextID, ok := <-wake:
					if !ok {
						break drain
					}
					pending[nextID] = struct{}{}
				default:
					break drain
				}
			}
			for wsID := range pending {
				if err := api.deliverWorkspacePushes(ctx, wsID); err != nil && ctx.Err() == nil {
					logger.Error("workspace push delivery failed", "workspace_id", wsID, "error", err)
				}
			}
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

type pushJob struct {
	sub     store.PushSubscription
	cardID  uuid.UUID
	update  cardUpdate
	payload []byte
	topic   string
}

func (api *API) deliverWorkspacePushes(ctx context.Context, workspaceID uuid.UUID) error {
	idle, err := api.store.ListIdlePushSubscriptions(ctx, workspaceID)
	if err != nil {
		return err
	}
	// 无空闲订阅时直接跳过，避免无谓执行全工作区 cardUpdates SQL；
	// 活跃设备由前端 SSE + /seen 维护各自快照，切后台或心跳过期后即进入 idle 队列。
	if len(idle) == 0 {
		return nil
	}
	updates, err := api.cardUpdates(ctx, workspaceID)
	if err != nil {
		return err
	}
	allSnapshots, err := api.store.ListWorkspacePushSubscriptionCardSnapshots(ctx, workspaceID)
	if err != nil {
		return err
	}
	knownBySub := make(map[uuid.UUID]map[uuid.UUID]store.PushCardSnapshot, len(idle))
	for _, snap := range allSnapshots {
		byCard := knownBySub[snap.SubscriptionID]
		if byCard == nil {
			byCard = make(map[uuid.UUID]store.PushCardSnapshot)
			knownBySub[snap.SubscriptionID] = byCard
		}
		byCard[snap.CardID] = snap
	}

	var jobs []pushJob
	for _, sub := range idle {
		known := knownBySub[sub.ID]
		var silentSnapshots []store.PushCardSnapshot
		for _, update := range updates {
			cardID, err := uuid.Parse(update.ID)
			if err != nil {
				continue
			}
			previous, seen := known[cardID]
			if seen && previous.Revision == update.Revision {
				continue
			}
			// 订阅从未见过该卡片时（例如订阅后新建了卡片）：
			// 若卡片尚无任何可推消息，则只建立基线；若已有正式消息或工具审批（如新建 Todo 并已产生首条回复），立即投递。
			if !seen && update.MessagePreview == "" {
				silentSnapshots = append(silentSnapshots, cardUpdateToPushSnapshot(sub.ID, cardID, update))
				continue
			}
			// 置顶、改标题、日程、审批处理完成回落到旧消息等都会改 revision，但不是新通知。记下来避免重复推送。
			if seen && !newCardMessage(update, previous) {
				silentSnapshots = append(silentSnapshots, cardUpdateToPushSnapshot(sub.ID, cardID, update))
				continue
			}
			// 单卡片静音或已被监督回调链路静默收口时跳过实际 Web Push 投递，但工具审批待确认穿透静音；
			// 跳过时推进该订阅快照，避免后续解除静音或状态回落时把静音期间的历史消息补推出来。
			if shouldSuppressCardNotification(update) {
				silentSnapshots = append(silentSnapshots, cardUpdateToPushSnapshot(sub.ID, cardID, update))
				continue
			}
			if update.MessagePreview == "" {
				silentSnapshots = append(silentSnapshots, cardUpdateToPushSnapshot(sub.ID, cardID, update))
				continue
			}
			payload, err := buildPushPayload(workspaceID, update)
			if err != nil {
				return err
			}
			jobs = append(jobs, pushJob{
				sub:     sub,
				cardID:  cardID,
				update:  update,
				payload: payload,
				topic:   strings.ReplaceAll(update.ID, "-", ""),
			})
		}
		if len(silentSnapshots) > 0 {
			if err := api.store.SavePushSubscriptionCardSnapshotsBatch(ctx, workspaceID, sub.ID, silentSnapshots); err != nil {
				return err
			}
		}
	}

	if len(jobs) == 0 {
		return nil
	}

	// 并发向各空闲端点投递，单个慢/失效端点绝不阻塞其他设备，且仅在对应端点发送成功时推进其快照。
	sem := make(chan struct{}, maxConcurrentPushes)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	recordErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
	}

	for _, job := range jobs {
		job := job
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			sendCtx, cancel := context.WithTimeout(ctx, singlePushTimeout)
			err := api.push.Send(sendCtx, job.sub.Endpoint, job.sub.P256dh, job.sub.AuthSecret, job.topic, job.payload)
			cancel()

			if errors.Is(err, webpush.ErrGone) {
				if delErr := api.store.DeletePushSubscriptionByEndpoint(ctx, job.sub.Endpoint); delErr != nil {
					recordErr(delErr)
				}
				return
			}
			if err != nil {
				// 保留该订阅的旧快照，下一轮仅对该失败端点重试，不影响其他已成功设备。
				if api.logger != nil {
					api.logger.Warn("card web push delivery failed",
						"workspace_id", workspaceID,
						"card_id", job.update.ID,
						"endpoint_host", pushEndpointHost(job.sub.Endpoint),
						"error", err,
					)
				}
				recordErr(err)
				return
			}
			if saveErr := api.store.SavePushSubscriptionCardSnapshot(
				ctx, workspaceID, job.sub.ID, cardUpdateToPushSnapshot(job.sub.ID, job.cardID, job.update),
			); saveErr != nil {
				recordErr(saveErr)
			}
		}()
	}
	wg.Wait()
	return firstErr
}

func cardUpdateToPushSnapshot(subscriptionID, cardID uuid.UUID, update cardUpdate) store.PushCardSnapshot {
	return store.PushCardSnapshot{
		SubscriptionID: subscriptionID,
		CardID:         cardID,
		Revision:       update.Revision,
		EventSequence:  update.EventSequence,
		MessagePreview: update.MessagePreview,
		AttemptStatus:  update.AttemptStatus,
		Title:          update.Title,
	}
}

type pushSeenRequest struct {
	Endpoint string `json:"endpoint"`
	Cards    []struct {
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
	if len(request.Cards) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	identity := identityFromRequest(r)

	var subID uuid.UUID
	var hasEndpoint bool
	if strings.TrimSpace(request.Endpoint) != "" {
		endpoint, ok := normalizePushEndpoint(w, request.Endpoint)
		if !ok {
			return
		}
		var err error
		subID, hasEndpoint, err = api.store.GetPushSubscriptionIDByEndpoint(
			r.Context(), identity.WorkspaceID, identity.HumanAccountID, endpoint,
		)
		if err != nil {
			api.handleError(w, err)
			return
		}
		if !hasEndpoint {
			// 订阅不存在（可能已退订或刚切换账号），直接返回 204 不影响其他设备。
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	updates, err := api.cardUpdates(r.Context(), identity.WorkspaceID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	byID := make(map[string]cardUpdate, len(updates))
	for _, update := range updates {
		byID[update.ID] = update
	}

	if hasEndpoint {
		snapshots := make([]store.PushCardSnapshot, 0, len(request.Cards))
		for _, card := range request.Cards {
			update, ok := byID[card.ID]
			if !ok || update.Revision != card.Revision {
				continue
			}
			cardID, err := uuid.Parse(card.ID)
			if err != nil {
				continue
			}
			snapshots = append(snapshots, cardUpdateToPushSnapshot(subID, cardID, update))
		}
		if err := api.store.SavePushSubscriptionCardSnapshotsBatch(r.Context(), identity.WorkspaceID, subID, snapshots); err != nil {
			api.handleError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 兼容未传 endpoint 的旧客户端：仅写旧表并同步到当前用户当前仍处于活跃态的订阅，绝不吞掉已锁屏/空闲的其他设备推送。
	subs, err := api.store.ListPushSubscriptions(r.Context(), identity.WorkspaceID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	idleSubs, err := api.store.ListIdlePushSubscriptions(r.Context(), identity.WorkspaceID)
	if err != nil {
		api.handleError(w, err)
		return
	}
	idleSet := make(map[uuid.UUID]struct{}, len(idleSubs))
	for _, sub := range idleSubs {
		idleSet[sub.ID] = struct{}{}
	}
	var activeSubs []store.PushSubscription
	for _, sub := range subs {
		if _, isIdle := idleSet[sub.ID]; !isIdle {
			activeSubs = append(activeSubs, sub)
		}
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
		if err := api.store.SavePushCardSnapshot(r.Context(), identity.WorkspaceID, cardUpdateToPushSnapshot(uuid.Nil, cardID, update)); err != nil {
			api.handleError(w, err)
			return
		}
		for _, sub := range activeSubs {
			if err := api.store.SavePushSubscriptionCardSnapshot(r.Context(), identity.WorkspaceID, sub.ID, cardUpdateToPushSnapshot(sub.ID, cardID, update)); err != nil {
				api.handleError(w, err)
				return
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func buildPushPayload(workspaceID uuid.UUID, update cardUpdate) ([]byte, error) {
	title := truncatePushText(update.Title, maxPushTitleRunes)
	if title == "" {
		title = "新消息"
	}
	section := cardNotificationSection(update.Kind)
	return json.Marshal(pushPayload{
		Type:        "card.updated",
		WorkspaceID: workspaceID.String(),
		CardID:      update.ID,
		Revision:    update.Revision,
		Title:       title,
		Body:        truncatePushText(cardPushBody(update), maxPushBodyRunes),
		URL:         "/" + section + "/" + url.PathEscape(update.ProjectID) + "/" + url.PathEscape(update.ID),
		Tag:         "card:" + workspaceID.String() + ":" + update.ID,
	})
}

func cardNotificationSection(kind string) string {
	switch kind {
	case "discussion":
		return "chat"
	case "ongoing":
		return "plans"
	default:
		return "cards"
	}
}

// newCardMessage 判断当前快照相比上次已处理快照是否产生了更新的通知事件。
// 使用递增序号比较（curr > prev），确保审批处理完成后 m.sequence 回落到历史旧消息时不会被误当成新消息重推。
func newCardMessage(current cardUpdate, previous store.PushCardSnapshot) bool {
	if current.MessagePreview == "" {
		return false
	}
	currSeq, currErr := strconv.ParseInt(current.EventSequence, 10, 64)
	prevSeq, prevErr := strconv.ParseInt(previous.EventSequence, 10, 64)
	if currErr == nil && prevErr == nil {
		return currSeq > prevSeq
	}
	return current.EventSequence != previous.EventSequence
}

func cardPushBody(current cardUpdate) string {
	return current.MessagePreview
}

func truncatePushText(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if maxRunes <= 0 || utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}

func pushEndpointHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "unknown"
	}
	return parsed.Host
}
