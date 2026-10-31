package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/auth"
	"github.com/uvwt/CycleDock/internal/domain"
	"github.com/uvwt/CycleDock/internal/store"
	"github.com/uvwt/CycleDock/internal/webpush"
)

type recordedPush struct {
	payload []byte
}

type recordingSender struct {
	publicKey string
	sent      []recordedPush
	gone      map[string]bool
}

func (s *recordingSender) PublicKey() string { return s.publicKey }

func (s *recordingSender) Send(_ context.Context, endpoint, _, _, _ string, payload []byte) error {
	if s.gone[endpoint] {
		return webpush.ErrGone
	}
	s.sent = append(s.sent, recordedPush{payload: append([]byte(nil), payload...)})
	return nil
}

func TestPushWaitsUntilThePageCloses(t *testing.T) {
	_, pool, dataStore := newHTTPIntegration(t)
	authService, err := auth.New(pool, auth.Config{AdminPassword: httpIntegrationPassword})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewRouter(pool, dataStore, authService, Config{})
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"password": httpIntegrationPassword}, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookie := responseCookie(t, login, sessionCookieName)
	var me identityResponse
	decodeRecorder(t, login, &me)
	ctx := context.Background()
	workspaceID := uuid.MustParse(me.Workspace.ID)
	userID := uuid.MustParse(me.HumanAccountID)
	sourceID := uuid.MustParse(me.Source.ID)
	project, err := dataStore.CreateProject(ctx, workspaceID, "Push", "", "")
	if err != nil {
		t.Fatal(err)
	}
	card, err := dataStore.CreateDiscussion(ctx, workspaceID, project.ID, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "https://push.example.test/subscription"
	if err := dataStore.SavePushSubscription(ctx, workspaceID, userID, store.PushSubscription{
		Endpoint: endpoint, P256dh: "key", AuthSecret: "auth",
	}); err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{publicKey: "test"}
	api := &API{pool: pool, store: dataStore, push: sender}
	if err := api.deliverPushes(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("baseline push = %d", len(sender.sent))
	}
	if _, err := dataStore.UpdateCardText(ctx, workspaceID, card.ID, "只是改标题", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetCardPinned(ctx, workspaceID, card.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.IdlePushSubscription(ctx, workspaceID, userID, endpoint); err != nil {
		t.Fatal(err)
	}
	if err := api.deliverPushes(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("pin or title was pushed: %+v", sender.sent)
	}
	if _, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID: workspaceID, CardID: card.ID, AuthorSourceID: sourceID,
		Content: "页面还开着", IdempotencyKey: "push-open", DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.TouchPushSubscription(ctx, workspaceID, userID, endpoint); err != nil {
		t.Fatal(err)
	}
	if err := api.deliverPushes(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("open page was pushed: %d", len(sender.sent))
	}
	// 页面还开着时不能把新消息记成已读。关掉之后，不用再发一条，也应把没确认的消息推出去。
	if err := dataStore.IdlePushSubscription(ctx, workspaceID, userID, endpoint); err != nil {
		t.Fatal(err)
	}
	if err := api.deliverPushes(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || !strings.Contains(string(sender.sent[0].payload), "页面还开着") {
		t.Fatalf("unacked push = %+v", sender.sent)
	}

	if _, err := dataStore.TouchPushSubscription(ctx, workspaceID, userID, endpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID: workspaceID, CardID: card.ID, AuthorSourceID: sourceID,
		Content: "页面确认看过", IdempotencyKey: "push-acked", DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	updates, err := api.cardUpdates(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	var seenRevision string
	for _, update := range updates {
		if update.ID == card.ID.String() {
			seenRevision = update.Revision
		}
	}
	if seenRevision == "" {
		t.Fatal("missing card revision")
	}
	acked := doJSON(t, handler, http.MethodPost, "/api/v1/notifications/push/seen", map[string]any{
		"cards": []map[string]string{{"id": card.ID.String(), "revision": seenRevision}},
	}, cookie, nil)
	if acked.Code != http.StatusNoContent {
		t.Fatalf("ack: %d %s", acked.Code, acked.Body.String())
	}
	if err := dataStore.IdlePushSubscription(ctx, workspaceID, userID, endpoint); err != nil {
		t.Fatal(err)
	}
	if err := api.deliverPushes(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("acked revision was pushed again: %+v", sender.sent)
	}

	sender.gone = map[string]bool{endpoint: true}
	if _, err := dataStore.SubmitSessionComment(ctx, store.SubmitSessionCommentParams{
		WorkspaceID: workspaceID, CardID: card.ID, AuthorSourceID: sourceID,
		Content: "订阅失效", IdempotencyKey: "push-gone", DispatchMode: domain.DispatchModeEnqueue,
	}); err != nil {
		t.Fatal(err)
	}
	if err := api.deliverPushes(ctx); err != nil {
		t.Fatal(err)
	}
	idle, err := dataStore.ListIdlePushSubscriptions(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(idle) != 0 {
		t.Fatalf("gone subscription remained: %+v", idle)
	}
}

func TestPushSubscriptionRequiresConfiguration(t *testing.T) {
	_, pool, dataStore := newHTTPIntegration(t)
	authService, err := auth.New(pool, auth.Config{AdminPassword: httpIntegrationPassword})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewRouter(pool, dataStore, authService, Config{})
	login := doJSON(t, handler, http.MethodPost, "/api/v1/auth/login", map[string]string{"password": httpIntegrationPassword}, nil, nil)
	cookie := responseCookie(t, login, sessionCookieName)
	unconfigured := doJSON(t, handler, http.MethodGet, "/api/v1/notifications/push", nil, cookie, nil)
	if unconfigured.Code != http.StatusOK || !strings.Contains(unconfigured.Body.String(), `"public_key":""`) {
		t.Fatalf("unconfigured key: %d %s", unconfigured.Code, unconfigured.Body.String())
	}
	rejected := doJSON(t, handler, http.MethodPut, "/api/v1/notifications/push/subscription", map[string]any{
		"endpoint": "https://push.example.test/one",
		"keys":     map[string]string{"p256dh": "key", "auth": "auth"},
	}, cookie, nil)
	if rejected.Code != http.StatusServiceUnavailable {
		t.Fatalf("save without keys: %d %s", rejected.Code, rejected.Body.String())
	}

	configured := NewRouter(pool, dataStore, authService, Config{Push: &recordingSender{publicKey: "public-test-key"}})
	key := doJSON(t, configured, http.MethodGet, "/api/v1/notifications/push", nil, cookie, nil)
	if key.Code != http.StatusOK || !strings.Contains(key.Body.String(), "public-test-key") {
		t.Fatalf("configured key: %d %s", key.Code, key.Body.String())
	}
	saved := doJSON(t, configured, http.MethodPut, "/api/v1/notifications/push/subscription", map[string]any{
		"endpoint": "https://push.example.test/one",
		"keys":     map[string]string{"p256dh": "key", "auth": "auth"},
	}, cookie, nil)
	if saved.Code != http.StatusNoContent {
		t.Fatalf("save subscription: %d %s", saved.Code, saved.Body.String())
	}
	insecure := doJSON(t, configured, http.MethodPut, "/api/v1/notifications/push/subscription", map[string]any{
		"endpoint": "http://push.example.test/one",
		"keys":     map[string]string{"p256dh": "key", "auth": "auth"},
	}, cookie, nil)
	if insecure.Code != http.StatusBadRequest {
		t.Fatalf("http endpoint: %d %s", insecure.Code, insecure.Body.String())
	}
}
